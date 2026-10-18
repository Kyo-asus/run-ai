//go:build integration
// +build integration

/*
 * SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 */

package tests

import (
	"net/http"
	"testing"
	"time"

	"github.com/dsx-ai-factory/run-ai/control-plane/metrics-service/internal/database/dao"
	"github.com/dsx-ai-factory/run-ai/control-plane/metrics-service/internal/database/models"
	"github.com/dsx-ai-factory/run-ai/control-plane/metrics-service/internal/generated/pkg/api"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
)

const reportsDaoPageSize = 1000

func newReportFixture() models.Report {
	return models.Report{
		Name:    "testing",
		Start:   time.Now().Add(-1 * time.Hour),
		End:     time.Now(),
		Metrics: []*models.Metric{{DisplayName: "Metric A", Query: "metric_a"}},
		Status:  string(api.REPORTSTATUS_PENDING),
		Labels: models.LabelArray{
			{Name: "labelA", DisplayName: "Label A"},
		},
		AdditionalFields: models.AdditionalFields{"a": "B"},
	}
}

// createDaoReport creates a report for a fresh tenant and deletes it when the
// test ends.
func createDaoReport(t *testing.T, reportsDao *dao.PostgresReportsDao, tenantId int32, name string) models.Report {
	t.Helper()
	input := newReportFixture()
	input.Name = name
	input.TenantId = tenantId
	ctx := daoContext(tenantId)

	created, err := reportsDao.CreateReport(ctx, tenantId, input)
	require.Nil(t, err)
	t.Cleanup(func() {
		if err := reportsDao.DeleteReport(ctx, tenantId, created.ID.String()); err != nil {
			t.Errorf("failed to delete report %s: %v", created.ID, err)
		}
	})
	return created
}

func newReportsDaoFixture(t *testing.T) (*dao.PostgresReportsDao, int32) {
	t.Helper()
	initDaoDB(t)
	return dao.NewPostgresReportsDao(), randomTenantID()
}

func TestReportsDaoCreateAndGet(t *testing.T) {
	reportsDao, tenantId := newReportsDaoFixture(t)
	ctx := daoContext(tenantId)
	created := createDaoReport(t, reportsDao, tenantId, "create-and-get")

	require.NotNil(t, created.ID)
	got, err := reportsDao.GetReport(ctx, tenantId, created.ID.String())
	require.Nil(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, tenantId, got.TenantId)
	require.Equal(t, string(api.REPORTSTATUS_PENDING), got.Status)
	require.Equal(t, "", got.StatusMessage)
	require.NotNil(t, got.StatusUpdatedAt)
	require.NotNil(t, got.CreatedAt)
	require.LessOrEqual(t, got.CreatedAt.Unix(), got.StatusUpdatedAt.Unix())
	require.Greater(t, got.CreatedAt.Unix(), got.StatusUpdatedAt.Unix()-5000)
	require.Equal(t, "user", got.CreatedBy)
	require.Len(t, got.Metrics, 1)
	require.Len(t, got.Labels, 1)
	require.Equal(t, created.Labels[0].Name, got.Labels[0].Name)
	require.Equal(t, created.Labels[0].DisplayName, got.Labels[0].DisplayName)
	require.NotNil(t, got.AdditionalFields)
}

func TestReportsDaoGetReportsByIdsIsTenantScoped(t *testing.T) {
	reportsDao, tenantId := newReportsDaoFixture(t)
	otherTenantId := tenantId + 1
	ctx := daoContext(tenantId)
	mine := createDaoReport(t, reportsDao, tenantId, "get-reports-by-ids-tenant-one")
	other := createDaoReport(t, reportsDao, otherTenantId, "get-reports-by-ids-other-tenant")

	got, err := reportsDao.GetReportsByIds(ctx, tenantId, []string{mine.ID.String(), other.ID.String()})

	require.Nil(t, err)
	require.Len(t, got, 1)
	require.Equal(t, mine.ID, got[0].ID)
}

func TestReportsDaoGetMissingReport(t *testing.T) {
	reportsDao, tenantId := newReportsDaoFixture(t)

	_, err := reportsDao.GetReport(daoContext(tenantId), tenantId, uuid.NewString())

	require.NotNil(t, err)
	require.Equal(t, "requested resource does not exist", err.Details)
	require.Equal(t, http.StatusNotFound, int(err.Code))
}

func TestReportsDaoDelete(t *testing.T) {
	reportsDao, tenantId := newReportsDaoFixture(t)
	ctx := daoContext(tenantId)
	created := createDaoReport(t, reportsDao, tenantId, "delete-report-test")

	require.Nil(t, reportsDao.DeleteReport(ctx, tenantId, created.ID.String()))

	_, err := reportsDao.GetReport(ctx, tenantId, created.ID.String())
	require.NotNil(t, err)
	require.Equal(t, "Failed to get report", err.Message)
}

func TestReportsDaoGetPendingReports(t *testing.T) {
	reportsDao, tenantId := newReportsDaoFixture(t)
	created := createDaoReport(t, reportsDao, tenantId, "pending")

	reports, err := reportsDao.GetPendingReports(daoContext(tenantId), reportsDaoPageSize)

	require.Nil(t, err)
	var found *models.Report
	for i := range reports {
		if reports[i].ID == created.ID {
			found = &reports[i]
		}
	}
	require.NotNil(t, found, "created pending report missing from result")
	require.Equal(t, tenantId, found.TenantId)
	require.Equal(t, string(api.REPORTSTATUS_PENDING), found.Status)
	require.Equal(t, "", found.StatusMessage)
	require.NotNil(t, found.StatusUpdatedAt)
	require.Len(t, found.Metrics, 1)
	require.Len(t, found.Labels, 1)
}

func TestReportsDaoGetTotalReportsByStatus(t *testing.T) {
	reportsDao, tenantId := newReportsDaoFixture(t)
	ctx := daoContext(tenantId)

	before, err := reportsDao.GetTotalReportsByStatus(ctx)
	require.Nil(t, err)

	createDaoReport(t, reportsDao, tenantId, "total-by-status")

	after, err := reportsDao.GetTotalReportsByStatus(ctx)
	require.Nil(t, err)
	pending := string(api.REPORTSTATUS_PENDING)
	require.Equal(t, before[pending]+1, after[pending])
}

func TestReportsDaoUpdateReportStatus(t *testing.T) {
	reportsDao, tenantId := newReportsDaoFixture(t)
	created := createDaoReport(t, reportsDao, tenantId, "update-status")
	id := created.ID.String()

	require.Nil(t, reportsDao.UpdateReportStatus(tenantId, id, api.REPORTSTATUS_FAILED, nil))
	require.Nil(t, reportsDao.UpdateReportStatus(tenantId, id, api.REPORTSTATUS_PROCESSING, ptr.To(api.REPORTSTATUS_FAILED)))
}

func TestReportsDaoUpdateReportStatusWithMessage(t *testing.T) {
	reportsDao, tenantId := newReportsDaoFixture(t)
	created := createDaoReport(t, reportsDao, tenantId, "update-status-message")
	id := created.ID.String()

	require.Nil(t, reportsDao.UpdateReportStatusWithMessage(tenantId, id, api.REPORTSTATUS_FAILED, "failed", nil))

	got, err := reportsDao.GetReport(daoContext(tenantId), tenantId, id)
	require.Nil(t, err)
	require.Equal(t, string(api.REPORTSTATUS_FAILED), got.Status)
	require.Equal(t, "failed", got.StatusMessage)
}

func TestReportsDaoUpdateReportStatusToProcessingTwiceFails(t *testing.T) {
	reportsDao, tenantId := newReportsDaoFixture(t)
	created := createDaoReport(t, reportsDao, tenantId, "processing-twice")
	id := created.ID.String()

	require.Nil(t, reportsDao.UpdateReportStatus(tenantId, id, api.REPORTSTATUS_PROCESSING, ptr.To(api.REPORTSTATUS_PENDING)))
	require.NotNil(t, reportsDao.UpdateReportStatus(tenantId, id, api.REPORTSTATUS_PROCESSING, ptr.To(api.REPORTSTATUS_PENDING)))
}

func TestReportsDaoUpdatePendingDelete(t *testing.T) {
	t.Run("marks non-processing reports as pending delete and fetches them", func(t *testing.T) {
		reportsDao, tenantId := newReportsDaoFixture(t)
		ctx := daoContext(tenantId)
		created := createDaoReport(t, reportsDao, tenantId, "pending-delete")
		id := created.ID.String()

		updated, apiErr := reportsDao.UpdatePendingDelete(ctx, tenantId, []string{id, uuid.NewString()})
		require.Nil(t, apiErr)
		require.ElementsMatch(t, []string{id}, updated)

		marked, getErr := reportsDao.GetReport(ctx, tenantId, id)
		require.Nil(t, getErr)
		require.Equal(t, string(api.REPORTSTATUS_PENDING_DELETE), marked.Status)

		pendingDelete, getPendingDeleteErr := reportsDao.GetPendingDeleteReports(ctx, reportsDaoPageSize)
		require.Nil(t, getPendingDeleteErr)
		ids := make([]uuid.UUID, 0, len(pendingDelete))
		for _, report := range pendingDelete {
			ids = append(ids, report.ID)
		}
		require.Contains(t, ids, created.ID)

		require.Nil(t, reportsDao.UpdateReportStatus(tenantId, id, api.REPORTSTATUS_DELETING, ptr.To(api.REPORTSTATUS_PENDING_DELETE)))
	})

	t.Run("skips processing reports", func(t *testing.T) {
		reportsDao, tenantId := newReportsDaoFixture(t)
		ctx := daoContext(tenantId)
		created := createDaoReport(t, reportsDao, tenantId, "pending-delete-skip-processing")
		id := created.ID.String()
		require.Nil(t, reportsDao.UpdateReportStatus(tenantId, id, api.REPORTSTATUS_PROCESSING, ptr.To(api.REPORTSTATUS_PENDING)))

		updated, apiErr := reportsDao.UpdatePendingDelete(ctx, tenantId, []string{id})
		require.Nil(t, apiErr)
		require.Empty(t, updated)

		got, getErr := reportsDao.GetReport(ctx, tenantId, id)
		require.Nil(t, getErr)
		require.Equal(t, string(api.REPORTSTATUS_PROCESSING), got.Status)
	})

	t.Run("skips reports already in the deletion workflow", func(t *testing.T) {
		reportsDao, tenantId := newReportsDaoFixture(t)
		ctx := daoContext(tenantId)
		created := createDaoReport(t, reportsDao, tenantId, "deleting-skip-test")
		id := created.ID.String()

		_, apiErr := reportsDao.UpdatePendingDelete(ctx, tenantId, []string{id})
		require.Nil(t, apiErr)
		require.Nil(t, reportsDao.UpdateReportStatus(tenantId, id, api.REPORTSTATUS_DELETING, nil))

		updated, apiErr := reportsDao.UpdatePendingDelete(ctx, tenantId, []string{id})
		require.Nil(t, apiErr)
		require.Empty(t, updated)

		got, getErr := reportsDao.GetReport(ctx, tenantId, id)
		require.Nil(t, getErr)
		require.Equal(t, string(api.REPORTSTATUS_DELETING), got.Status)
	})
}
