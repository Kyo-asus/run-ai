/*
 * SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 */

package chart_inspection

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/dsx-ai-factory/run-ai/control-plane/workloads/internal/generated/pkg/api"
	"github.com/dsx-ai-factory/run-ai/runai-common-packages/cache/v2"
	"github.com/dsx-ai-factory/run-ai/runai-common-packages/cache/v2/options"
	"github.com/dsx-ai-factory/run-ai/runai-common-packages/logging"
	"github.com/dsx-ai-factory/run-ai/runai-common-packages/runaictx"
)

const (
	inspectionParserVersion  = "questions-v2"
	cacheSaltBytes           = sha256.Size
	defaultMaxCacheSizeMB    = 20
	defaultMaxRecordSizeKB   = 4096
	cacheAccessTimeout       = time.Second
	maxConcurrentInspections = 4
)

// DefaultCacheTTL applies only to exact-version HTTPS NGC repository selections.
const DefaultCacheTTL = 10 * time.Minute

// Keep metadata encoded through the shared cache's JSON round trip; decoding
// generic question defaults there would turn integers into float64 values.
type chartInspectionMetadata struct {
	Data json.RawMessage `json:"data"`
}

// inspectionFlight tracks one acquisition and its result. Eligible NGC requests
// share it; each waiter can leave independently without cancelling the others.
type inspectionFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	data    []byte
	err     *api.Error
}

// Inspector caches successful NGC repository metadata and bounds all expensive inspections.
// Callers must authorize the chart source and credential on EVERY request, including cache hits.
// Scope must include tenant, caller and authorization-relevant groups, derived from trusted context.
type Inspector struct {
	mu               sync.Mutex
	fetch            FetchFunc
	cacheReady       bool
	salt             [cacheSaltBytes]byte
	flights          map[string]*inspectionFlight
	active           int
	uncachedSequence uint64
}

// NewInspector registers CP metadata storage and allows at most four concurrent acquisitions.
// A nil fetcher uses FetchRemote; a nonpositive TTL uses DefaultCacheTTL.
func NewInspector(fetch FetchFunc, ttl time.Duration) *Inspector {
	if fetch == nil {
		fetch = FetchRemote
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	cacheErr := cache.RegisterStore[chartInspectionMetadata](
		options.Ttl(ttl),
		options.MaxStoreSizeMB(defaultMaxCacheSizeMB),
		options.MaxRecordSizeKB(defaultMaxRecordSizeKB),
	)
	if cacheErr != nil {
		logging.Warnf("Failed to register chart inspection cache; inspections will run uncached")
	}
	inspector := &Inspector{
		fetch:      fetch,
		cacheReady: cacheErr == nil,
		flights:    make(map[string]*inspectionFlight),
	}
	if _, err := rand.Read(inspector.salt[:]); err != nil {
		panic(fmt.Sprintf("initialize chart inspection cache: %v", err))
	}
	return inspector
}

// Inspect returns normalized metadata using the configured fetcher. The service
// must authorize each request first and supply a scope derived from trusted identity.
func (i *Inspector) Inspect(ctx context.Context, selection Selection, scope, credential string) (*api.AiApplicationChartInspectionResponse, *api.Error) {
	return i.InspectWithFetch(ctx, selection, scope, credential, i.fetch)
}

// InspectWithFetch runs a service-provided fetcher inside the acquisition limit,
// allowing uploaded-chart reads to share the same bounds as remote downloads.
// Authorization is the caller's responsibility, including on cache hits.
func (i *Inspector) InspectWithFetch(ctx context.Context, selection Selection, scope, credential string, fetch FetchFunc) (*api.AiApplicationChartInspectionResponse, *api.Error) {
	if err := ctx.Err(); err != nil {
		return nil, inspectionError(err)
	}
	if err := selection.validate(); err != nil {
		return nil, err
	}
	if fetch == nil {
		return nil, api.NewError(http.StatusInternalServerError, "chart_inspection_failed: Chart inspection source is not configured.", nil)
	}
	if scope == "" {
		return nil, api.NewValidationError("invalid_inspection_scope: Chart inspection requires an authenticated caller scope.")
	}
	cacheable := cacheableNGCRepository(selection)
	key := ""
	if cacheable {
		key = i.key(selection, scope, credential)
		result := i.cachedMetadata(ctx, key)
		if err := ctx.Err(); err != nil {
			return nil, inspectionError(err)
		}
		if result != nil {
			return result, nil
		}
	}
	i.mu.Lock()
	if !cacheable {
		// Fresh sources must not reuse a cached result or another request's acquisition.
		i.uncachedSequence++
		key = fmt.Sprintf("uncached:%d", i.uncachedSequence)
	}
	flight := i.flights[key]
	if flight == nil {
		if i.active >= maxConcurrentInspections {
			i.mu.Unlock()
			return nil, api.NewError(http.StatusServiceUnavailable, "chart_inspection_capacity: Chart inspection capacity reached; retry shortly.", nil)
		}
		// One disconnected waiter must not cancel work still needed by others.
		// The shared deadline and releaseWaiter bound its lifetime instead.
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), downloadTimeout)
		flight = &inspectionFlight{done: make(chan struct{}), cancel: cancel}
		i.flights[key] = flight
		i.active++
		// Add the first waiter before starting work, so immediate completion can be cached.
		flight.waiters = 1
		go i.run(workCtx, key, flight, selection, credential, fetch, cacheable)
	} else {
		flight.waiters++
	}
	i.mu.Unlock()
	select {
	case <-ctx.Done():
		i.releaseWaiter(key, flight)
		return nil, inspectionError(ctx.Err())
	case <-flight.done:
		i.releaseWaiter(key, flight)
		if err := ctx.Err(); err != nil {
			return nil, inspectionError(err)
		}
		if flight.err != nil {
			return nil, flight.err
		}
		return decodeResult(flight.data)
	}
}

// Stop abandoned work when its last waiter leaves. run retains the active slot
// until the fetcher exits, so cancellation cannot bypass the concurrency limit.
func (i *Inspector) releaseWaiter(key string, flight *inspectionFlight) {
	i.mu.Lock()
	defer i.mu.Unlock()
	flight.waiters--
	if flight.waiters == 0 {
		flight.cancel()
		if i.flights[key] == flight {
			delete(i.flights, key)
		}
	}
}

// run completes an acquisition for its waiters and releases its capacity slot.
// Only successful metadata still needed by a live waiter is eligible for caching.
func (i *Inspector) run(ctx context.Context, key string, flight *inspectionFlight, selection Selection, credential string, fetch FetchFunc, cacheable bool) {
	defer flight.cancel()
	var result *api.AiApplicationChartInspectionResponse
	if cacheable {
		// Close the gap between a caller's cache miss and acquiring its flight:
		// another flight may have populated the cache and completed meanwhile.
		result = i.cachedMetadata(ctx, key)
	}
	fromCache := result != nil
	var err *api.Error
	if !fromCache {
		data, fetchErr := fetch(ctx, selection, credential)
		err = inspectionError(fetchErr)
		if err == nil {
			result, err = ParseArchive(data)
		}
	}
	if err == nil && result == nil {
		err = api.NewError(http.StatusInternalServerError, "chart inspection returned no result", nil)
	}
	if err == nil && selection.Repo != "" && (result.ChartName != selection.ChartName || result.ChartVersion != selection.ChartVersion) {
		err = api.NewValidationError("chart_identity_mismatch: Downloaded chart identity does not match the selected name and version.")
	}
	var encoded []byte
	if err == nil {
		var marshalErr error
		encoded, marshalErr = json.Marshal(result)
		if marshalErr != nil {
			err = api.NewError(http.StatusInternalServerError, "chart inspection metadata could not be encoded", nil)
		}
	}
	// Cache I/O must not hold the coordination lock. Losing the last waiter
	// cancels ctx, including any pending write; cache failures do not fail inspection.
	if cacheable && !fromCache && err == nil && ctx.Err() == nil && i.cacheReady {
		cacheCtx, cancel := context.WithTimeout(ctx, cacheAccessTimeout)
		if cacheErr := cache.Set(cacheCtx, key, chartInspectionMetadata{Data: encoded}); cacheErr != nil {
			logging.WithCtx(runaictx.LoggerHook(ctx)).Warnf("Failed to cache chart inspection metadata; returning uncached result")
		}
		cancel()
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	flight.data, flight.err = encoded, err
	if i.flights[key] == flight {
		delete(i.flights, key)
	}
	i.active--
	close(flight.done)
}

// Fetchers may return wrapped API errors or standard context errors. Normalize
// them at the inspection boundary without exposing private callback diagnostics.
func inspectionError(err error) *api.Error {
	if err == nil {
		return nil
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return api.NewError(http.StatusRequestTimeout, "chart inspection request cancelled or expired", nil)
	}
	return api.NewError(http.StatusInternalServerError, "chart inspection failed unexpectedly", nil)
}

func cacheableNGCRepository(selection Selection) bool {
	if selection.Repo == "" || selection.ChartName == "" || selection.ChartVersion == "" {
		return false
	}
	u, err := validateSourceURL(selection.Repo)
	return err == nil && u.Scheme == httpsScheme && ngcHost(u.Hostname())
}

// Decode a fresh object per caller so response mutation cannot alter the cache.
// UseNumber preserves large integer defaults across the JSON round trip.
func decodeResult(data []byte) (*api.AiApplicationChartInspectionResponse, *api.Error) {
	var result api.AiApplicationChartInspectionResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, api.NewError(http.StatusInternalServerError, "could not decode cached chart inspection metadata", nil)
	}
	return &result, nil
}

// Include caller scope and a salted credential fingerprint so different access
// contexts cannot share metadata and raw key material is not retained in cache keys.
func (i *Inspector) key(selection Selection, scope, credential string) string {
	fingerprint := hmac.New(sha256.New, i.salt[:])
	_, _ = fingerprint.Write([]byte(credential))
	data, _ := json.Marshal([]any{scope, selection, hex.EncodeToString(fingerprint.Sum(nil)), inspectionParserVersion})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// Cache availability must not become a prerequisite for inspecting a chart.
func (i *Inspector) cachedMetadata(ctx context.Context, key string) *api.AiApplicationChartInspectionResponse {
	if !i.cacheReady {
		return nil
	}
	cacheCtx, cancel := context.WithTimeout(ctx, cacheAccessTimeout)
	defer cancel()
	entry, err := cache.Get[chartInspectionMetadata](cacheCtx, key)
	if err != nil {
		logging.WithCtx(runaictx.LoggerHook(ctx)).Warnf("Failed to read chart inspection cache; inspecting source instead")
		return nil
	}
	if entry == nil {
		return nil
	}
	result, apiErr := decodeResult(entry.Data)
	if apiErr != nil {
		logging.WithCtx(runaictx.LoggerHook(ctx)).Warnf("Invalid chart inspection cache entry; inspecting source instead")
		return nil
	}
	return result
}
