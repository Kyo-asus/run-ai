//go:build integration
// +build integration

/*
 * SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 */

package tests

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/dsx-ai-factory/run-ai/runai-common-packages/runaictx"
	"github.com/dsx-ai-factory/run-ai/runai-common-packages/runaidb"
)

// initDaoDB points runaidb at the compose Postgres, which the metrics-service
// container has already migrated.
func initDaoDB(t *testing.T) {
	t.Helper()
	t.Setenv("DB_HOST", getEnvOrDefault("DB_HOST", "localhost"))
	t.Setenv("DB_PORT", getEnvOrDefault("DB_PORT", "5432"))
	t.Setenv("DB_NAME", getEnvOrDefault("DB_NAME", "runai"))
	t.Setenv("DB_SCHEMA", getEnvOrDefault("DB_SCHEMA", "metrics"))
	t.Setenv("SECRETS_MOUNT_PATH", getEnvOrDefault("SECRETS_MOUNT_PATH", "../secrets/"))
	if err := runaidb.Init(nil); err != nil {
		t.Fatalf("failed to init database: %v", err)
	}
}

func daoContext(tenantId int32) context.Context {
	return context.WithValue(
		context.WithValue(context.Background(), runaictx.CfTenantId, tenantId),
		runaictx.CfSubjectID,
		"user",
	)
}

func randomTenantID() int32 {
	return rand.New(rand.NewSource(time.Now().UnixNano())).Int31()
}
