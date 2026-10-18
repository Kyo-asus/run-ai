//go:build integration
// +build integration

/*
 * SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 */

package tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/dsx-ai-factory/run-ai/control-plane/metrics-service/internal/database/dao"
	"github.com/dsx-ai-factory/run-ai/control-plane/metrics-service/internal/database/models"
	"github.com/dsx-ai-factory/run-ai/runai-application-packages/queryapi"
	"github.com/dsx-ai-factory/run-ai/runai-common-packages/runaidb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Ascending id order, so a leaked "ORDER BY id" tiebreaker is distinguishable
// from any requested sort.
func reportIDFor(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", n))
}

func reportNames(reports []models.Report) []string {
	names := make([]string, 0, len(reports))
	for _, report := range reports {
		names = append(names, report.Name)
	}
	return names
}

func reportIDs(reports []models.Report) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(reports))
	for _, report := range reports {
		ids = append(ids, report.ID)
	}
	return ids
}

// newSortingFixture returns a DAO, tenant and insert helper for a fresh tenant
// whose reports are removed when the test ends.
func newSortingFixture(t *testing.T) (*dao.PostgresReportsDao, int32, func(models.Report)) {
	t.Helper()
	initDaoDB(t)
	tenantId := randomTenantID()
	ctx := daoContext(tenantId)

	t.Cleanup(func() {
		_, err := runaidb.DB().WithContext(ctx).Where("tenant_id = ?", tenantId).Delete(&models.Report{})
		require.NoError(t, err)
	})

	insert := func(report models.Report) {
		t.Helper()
		report.TenantId = tenantId
		_, err := runaidb.DB().WithContext(ctx).Create(&report)
		require.NoError(t, err)
	}
	return dao.NewPostgresReportsDao(), tenantId, insert
}

func TestReportsDaoGetReportsSorting(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)

	for _, tc := range []struct {
		name      string
		reports   []models.Report
		sortBy    string
		sortOrder string
		wantNames []string
	}{
		{
			name: "name ascending rather than by id",
			reports: []models.Report{
				{ID: reportIDFor(1), Name: "charlie"},
				{ID: reportIDFor(2), Name: "bravo"},
				{ID: reportIDFor(3), Name: "alpha"},
			},
			sortBy: "name", sortOrder: "asc",
			wantNames: []string{"alpha", "bravo", "charlie"},
		},
		{
			name: "name descending rather than by id",
			reports: []models.Report{
				{ID: reportIDFor(1), Name: "alpha"},
				{ID: reportIDFor(2), Name: "bravo"},
				{ID: reportIDFor(3), Name: "charlie"},
			},
			sortBy: "name", sortOrder: "desc",
			wantNames: []string{"charlie", "bravo", "alpha"},
		},
		{
			name: "status rather than by id",
			reports: []models.Report{
				{ID: reportIDFor(1), Name: "one", Status: "Ready"},
				{ID: reportIDFor(2), Name: "two", Status: "Failed"},
				{ID: reportIDFor(3), Name: "three", Status: "Deleting"},
			},
			sortBy: "status", sortOrder: "asc",
			wantNames: []string{"three", "two", "one"},
		},
		{
			name: "created_by rather than by id",
			reports: []models.Report{
				{ID: reportIDFor(1), Name: "one", CreatedBy: "zoe@run.ai"},
				{ID: reportIDFor(2), Name: "two", CreatedBy: "mia@run.ai"},
				{ID: reportIDFor(3), Name: "three", CreatedBy: "ada@run.ai"},
			},
			sortBy: "created_by", sortOrder: "asc",
			wantNames: []string{"three", "two", "one"},
		},
		{
			name: "created_at rather than by id",
			reports: []models.Report{
				{ID: reportIDFor(1), Name: "one", CreatedAt: base.Add(2 * time.Hour)},
				{ID: reportIDFor(2), Name: "two", CreatedAt: base.Add(time.Hour)},
				{ID: reportIDFor(3), Name: "three", CreatedAt: base},
			},
			sortBy: "created_at", sortOrder: "asc",
			wantNames: []string{"three", "two", "one"},
		},
	} {
		t.Run("sorts by "+tc.name, func(t *testing.T) {
			reportsDao, tenantId, insert := newSortingFixture(t)
			for _, report := range tc.reports {
				insert(report)
			}

			reports, apiErr := reportsDao.GetReports(daoContext(tenantId), tenantId, []queryapi.Filter{}, tc.sortBy, tc.sortOrder, 0, 0, "")

			require.Nil(t, apiErr)
			require.Equal(t, tc.wantNames, reportNames(reports))
		})
	}
}

func TestReportsDaoGetReportsSortingTiebreaker(t *testing.T) {
	reportsDao, tenantId, insert := newSortingFixture(t)
	insert(models.Report{ID: reportIDFor(3), Name: "charlie", Status: "Ready"})
	insert(models.Report{ID: reportIDFor(1), Name: "alpha", Status: "Ready"})
	insert(models.Report{ID: reportIDFor(2), Name: "bravo", Status: "Ready"})

	reports, apiErr := reportsDao.GetReports(daoContext(tenantId), tenantId, []queryapi.Filter{}, "status", "asc", 0, 0, "")

	require.Nil(t, apiErr)
	require.Equal(t, []uuid.UUID{reportIDFor(1), reportIDFor(2), reportIDFor(3)}, reportIDs(reports))
}

func TestReportsDaoGetReportsSortingStableAcrossPages(t *testing.T) {
	reportsDao, tenantId, insert := newSortingFixture(t)
	insert(models.Report{ID: reportIDFor(1), Name: "charlie"})
	insert(models.Report{ID: reportIDFor(2), Name: "bravo"})
	insert(models.Report{ID: reportIDFor(3), Name: "alpha"})
	ctx := daoContext(tenantId)

	firstPage, apiErr := reportsDao.GetReports(ctx, tenantId, []queryapi.Filter{}, "name", "asc", 0, 2, "")
	require.Nil(t, apiErr)
	require.Equal(t, []string{"alpha", "bravo"}, reportNames(firstPage))

	secondPage, apiErr := reportsDao.GetReports(ctx, tenantId, []queryapi.Filter{}, "name", "asc", 2, 2, "")
	require.Nil(t, apiErr)
	require.Equal(t, []string{"charlie"}, reportNames(secondPage))
}

func TestReportsDaoGetReportsDefaultsToIDOrder(t *testing.T) {
	reportsDao, tenantId, insert := newSortingFixture(t)
	insert(models.Report{ID: reportIDFor(3), Name: "charlie"})
	insert(models.Report{ID: reportIDFor(1), Name: "alpha"})
	insert(models.Report{ID: reportIDFor(2), Name: "bravo"})

	reports, apiErr := reportsDao.GetReports(daoContext(tenantId), tenantId, []queryapi.Filter{}, "", "", 0, 0, "")

	require.Nil(t, apiErr)
	require.Equal(t, []uuid.UUID{reportIDFor(1), reportIDFor(2), reportIDFor(3)}, reportIDs(reports))
}
