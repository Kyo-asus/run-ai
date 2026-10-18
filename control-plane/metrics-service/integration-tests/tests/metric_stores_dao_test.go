//go:build integration
// +build integration

/*
 * SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 */

package tests

import (
	"testing"

	"github.com/dsx-ai-factory/run-ai/control-plane/metrics-service/internal/database/dao"
	"github.com/dsx-ai-factory/run-ai/control-plane/metrics-service/internal/database/models"
	metricerrors "github.com/dsx-ai-factory/run-ai/control-plane/metrics-service/internal/errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newMetricStoreFixture(t *testing.T) (*dao.MetricStoresDao, models.MetricStore) {
	t.Helper()
	initDaoDB(t)
	tenantId := randomTenantID()
	ctx := daoContext(tenantId)
	metricStoresDao := dao.NewMetricStoresDao()
	endpoint := models.MetricStoreEndpoint{
		Url: "some_url",
		Auth: &models.MetricStoreEndpointAuth{
			Type:  models.ATBasic,
			Basic: &models.AuthBasic{Username: "test", Password: "secret"},
		},
	}
	metricStore := models.MetricStore{
		ID:           uuid.New(),
		TenantId:     tenantId,
		DatasourceId: "mimir_12345",
		MetricStoreDetails: models.MetricStoreDetails{
			Type:  models.MSTMimir,
			Read:  endpoint,
			Write: endpoint,
		},
	}
	t.Cleanup(func() { _ = metricStoresDao.Delete(ctx, tenantId) })
	return metricStoresDao, metricStore
}

func TestMetricStoresDaoCreateDuplicateTenant(t *testing.T) {
	metricStoresDao, metricStore := newMetricStoreFixture(t)
	ctx := daoContext(metricStore.TenantId)

	created, err := metricStoresDao.Create(ctx, metricStore)
	require.NoError(t, err)
	require.NotNil(t, created)
	require.NotEqual(t, uuid.Nil, created.ID)

	duplicate, err := metricStoresDao.Create(ctx, metricStore)
	require.Error(t, err)
	require.Nil(t, duplicate)
	require.ErrorIs(t, err, metricerrors.ErrMetricStoreAlreadyExists)
	require.Equal(t, "metric store already exists", err.Error())
}

func TestMetricStoresDaoCreateSavesAllFields(t *testing.T) {
	metricStoresDao, metricStore := newMetricStoreFixture(t)
	ctx := daoContext(metricStore.TenantId)
	want := metricStore.MetricStoreDetails.Read

	existing, err := metricStoresDao.GetByTenantId(ctx, metricStore.TenantId)
	require.NoError(t, err)
	require.Nil(t, existing)

	created, err := metricStoresDao.Create(ctx, metricStore)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, created.ID)

	got, err := metricStoresDao.GetByTenantId(ctx, metricStore.TenantId)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, metricStore.TenantId, got.TenantId)
	require.Equal(t, models.MSTMimir, got.MetricStoreDetails.Type)
	for name, endpoint := range map[string]models.MetricStoreEndpoint{
		"read":  got.MetricStoreDetails.Read,
		"write": got.MetricStoreDetails.Write,
	} {
		require.Equal(t, want.Url, endpoint.Url, name)
		require.Equal(t, want.Auth.Type, endpoint.Auth.Type, name)
		require.Equal(t, want.Auth.Basic.Username, endpoint.Auth.Basic.Username, name)
		require.Equal(t, want.Auth.Basic.Password, endpoint.Auth.Basic.Password, name)
	}
}

func TestMetricStoresDaoGetAfterDeleteReturnsNil(t *testing.T) {
	metricStoresDao, metricStore := newMetricStoreFixture(t)
	ctx := daoContext(metricStore.TenantId)
	_, err := metricStoresDao.Create(ctx, metricStore)
	require.NoError(t, err)

	got, err := metricStoresDao.GetByTenantId(ctx, metricStore.TenantId)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, metricStore.TenantId, got.TenantId)

	require.NoError(t, metricStoresDao.Delete(ctx, metricStore.TenantId))

	got, err = metricStoresDao.GetByTenantId(ctx, metricStore.TenantId)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestMetricStoresDaoGetMissingReturnsNil(t *testing.T) {
	metricStoresDao, metricStore := newMetricStoreFixture(t)

	got, err := metricStoresDao.GetByTenantId(daoContext(metricStore.TenantId), metricStore.TenantId)

	require.NoError(t, err)
	require.Nil(t, got)
}

func TestMetricStoresDaoDeleteMissingIsNoop(t *testing.T) {
	metricStoresDao, metricStore := newMetricStoreFixture(t)

	require.NoError(t, metricStoresDao.Delete(daoContext(metricStore.TenantId), metricStore.TenantId))
}
