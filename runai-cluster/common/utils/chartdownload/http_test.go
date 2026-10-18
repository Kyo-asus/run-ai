// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package chartdownload

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestFetchResponseOwnership(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
	}{
		{name: "successful archive", code: http.StatusOK},
		{name: "empty response is not an archive", code: http.StatusNoContent},
		{name: "denied", code: http.StatusForbidden},
		{name: "unavailable", code: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader("chart bytes")}
			response := &http.Response{StatusCode: tc.code, Status: http.StatusText(tc.code), Body: body}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response, nil
			})}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://charts.example/chart.tgz", nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Fetch(client, req)
			if tc.code == http.StatusOK {
				if err != nil || got != response || body.closed {
					t.Fatalf("success lost response ownership: response=%v err=%v closed=%v", got, err, body.closed)
				}
				defer func() {
					if err := got.Body.Close(); err != nil {
						t.Errorf("close response: %v", err)
					}
				}()
				data, err := io.ReadAll(got.Body)
				if err != nil || string(data) != "chart bytes" {
					t.Fatalf("body changed: %q, %v", data, err)
				}
			} else {
				var statusErr *StatusError
				if got != nil || !errors.As(err, &statusErr) || statusErr.StatusCode != tc.code || statusErr.Status != response.Status || !body.closed {
					t.Fatalf("failure lost status or body cleanup: response=%v err=%v closed=%v", got, err, body.closed)
				}
				if err.Error() != "unexpected status "+response.Status {
					t.Fatalf("unexpected diagnostic: %v", err)
				}
			}
		})
	}
}

func TestFetchPreservesRequestAndTransportError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://charts.example/chart.tgz", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(NGCBasicAuthUsername, "<test-key>")
	wantErr := errors.New("transport failed")
	client := &http.Client{Transport: roundTripFunc(func(got *http.Request) (*http.Response, error) {
		username, password, ok := got.BasicAuth()
		if got.Context() != ctx || got.URL.String() != req.URL.String() || !ok || username != "$oauthtoken" || password != "<test-key>" {
			t.Fatal("caller context, URL or authentication changed")
		}
		return nil, wantErr
	})}
	got, err := Fetch(client, req)
	if got != nil || !errors.Is(err, wantErr) {
		t.Fatalf("transport cause lost: response=%v err=%v", got, err)
	}
}

func TestFetchPreservesRedirectPolicy(t *testing.T) {
	wantErr := errors.New("caller rejected redirect")
	body := &trackedBody{Reader: strings.NewReader("")}
	requests := 0
	client := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://other.example/chart.tgz"}}, Body: body}, nil
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return wantErr },
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://charts.example/chart.tgz", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Fetch(client, req)
	if got != nil || !errors.Is(err, wantErr) || requests != 1 || !body.closed {
		t.Fatalf("redirect policy changed: response=%v err=%v requests=%d closed=%v", got, err, requests, body.closed)
	}
}
