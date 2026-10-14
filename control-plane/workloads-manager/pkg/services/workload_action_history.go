/*
 * SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 */

package services

import (
	"context"
	"net/http"
	"time"

	"github.com/cenkalti/backoff/v4"
	workloadsClient "github.com/dsx-ai-factory/run-ai/control-plane/workloads-manager/pkg/clients/workloads"
	ourConfig "github.com/dsx-ai-factory/run-ai/control-plane/workloads-manager/pkg/config"
	workloadsApi "github.com/dsx-ai-factory/run-ai/control-plane/workloads/pkg/client/generated"
	middlewares "github.com/dsx-ai-factory/run-ai/runai-application-packages/old-auth-token-middleware"
	"github.com/dsx-ai-factory/run-ai/runai-common-packages/config"
	"github.com/dsx-ai-factory/run-ai/runai-common-packages/logging"
	"github.com/dsx-ai-factory/run-ai/runai-common-packages/metrics"
	"github.com/dsx-ai-factory/run-ai/runai-common-packages/runaictx"
	"github.com/google/uuid"
)

const workloadActionDeliveryFailuresMetric = "workload_action_history_delivery_failures"

type workloadAction struct {
	ClusterID    string
	WorkloadID   string
	WorkloadName string
	Action       workloadsApi.WorkloadActionType
	AcceptedAt   time.Time
}

type workloadActionRecorder interface {
	Record(ctx context.Context, actions ...workloadAction)
}

type asyncWorkloadActionRecorder struct {
	client     func() workloadsClient.ClientInterface
	newBackOff func() backoff.BackOff
}

func newAsyncWorkloadActionRecorder() *asyncWorkloadActionRecorder {
	return &asyncWorkloadActionRecorder{
		client: workloadsClient.Get,
		newBackOff: func() backoff.BackOff {
			serverConfig := config.Get[*ourConfig.Server]()
			retry := backoff.NewExponentialBackOff()
			retry.MaxInterval = time.Duration(serverConfig.RetryMaxIntervalSec) * time.Second
			retry.MaxElapsedTime = time.Duration(serverConfig.RetryMaxIntervalSec) * time.Second
			return retry
		},
	}
}

func (r *asyncWorkloadActionRecorder) Record(ctx context.Context, actions ...workloadAction) {
	if len(actions) == 0 {
		return
	}
	request := workloadsApi.CreateWorkloadActionsRequest{Actions: make([]workloadsApi.WorkloadActionRecordInput, 0, len(actions))}
	for _, action := range actions {
		request.Actions = append(request.Actions, workloadsApi.WorkloadActionRecordInput{
			Id: uuid.NewString(), ClusterId: action.ClusterID, WorkloadId: action.WorkloadID,
			WorkloadName: action.WorkloadName, Action: action.Action, AcceptedAt: action.AcceptedAt,
		})
	}
	deliveryCtx := context.WithoutCancel(ctx)
	go func() {
		if err := r.deliver(deliveryCtx, request); err != nil {
			metrics.Counter(workloadActionDeliveryFailuresMetric).
				Register("Number of workload action-history batches that could not be delivered", nil).
				Inc()
			logging.WithCtx(runaictx.LoggerHook(deliveryCtx)).Errorf("failed to deliver workload action history: %v", err)
		}
	}()
}

func (r *asyncWorkloadActionRecorder) deliver(ctx context.Context, request workloadsApi.CreateWorkloadActionsRequest) *workloadsApi.Error {
	// The retry budget must also bound an HTTP call that never returns.
	timeout := time.Duration(config.Get[*ourConfig.Server]().RetryMaxIntervalSec) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deliveryCtx := context.WithValue(ctx, workloadsApi.ContextAccessToken, ctx.Value(middlewares.ContextJwtToken))
	ticker := backoff.NewTicker(backoff.WithContext(r.newBackOff(), deliveryCtx))
	defer ticker.Stop()
	var apiErr *workloadsApi.Error
	for range ticker.C {
		apiErr = r.client().CreateWorkloadActions(deliveryCtx, request)
		if apiErr == nil {
			return nil
		}
	}
	if err := deliveryCtx.Err(); err != nil {
		return workloadsApi.NewError(http.StatusServiceUnavailable, "Workload action history delivery stopped: "+err.Error())
	}
	return apiErr
}
