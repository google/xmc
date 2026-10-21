// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package testdb

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"xmanager_cloud/internal/storage/postgres/migrations/version"
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	defer func() {
		if sharedPostgresContainer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = sharedPostgresContainer.Terminate(ctx)
		}
	}()
	return m.Run()
}

func tableExists(ctx context.Context, t *testing.T, pool *pgxpool.Pool, tableName string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(
		ctx,
		`SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = current_schema()
			  AND LOWER(table_name) = LOWER($1)
		)`,
		tableName,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("failed to query information_schema.tables for %s: %v", tableName, err)
	}
	return exists
}

func listTables(ctx context.Context, t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(
		ctx,
		`SELECT table_name
		 FROM information_schema.tables
		 WHERE table_schema = current_schema()
		   AND table_type = 'BASE TABLE'
		 ORDER BY table_name`,
	)
	if err != nil {
		t.Fatalf("failed to list tables: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("failed to scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration failed: %v", err)
	}
	return tables
}

func currentDatabase(ctx context.Context, t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var dbName string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		t.Fatalf("failed to query current_database(): %v", err)
	}
	return dbName
}

type migrationState struct {
	Version int64
	Dirty   bool
}

func fetchMigrationState(ctx context.Context, t *testing.T, pool *pgxpool.Pool) migrationState {
	t.Helper()
	var state migrationState
	if err := pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&state.Version, &state.Dirty); err != nil {
		t.Fatalf("failed to query schema_migrations: %v", err)
	}
	return state
}

func TestSetup(t *testing.T) {
	ctx := t.Context()
	xidPool, essPool := Setup(ctx, t)

	xidTables := []string{"Xids", "schema_migrations"}
	for _, table := range xidTables {
		if !tableExists(ctx, t, xidPool, table) {
			t.Errorf("XID table %s does not exist after Setup", table)
		}
	}

	essTables := []string{
		"Experiments",
		"Artifacts",
		"WorkUnits",
		"Operations",
		"WorkUnitExecutions",
		"StatusMessages",
		"Dashboards",
		"Charts",
		"DashboardCharts",
		"ChartPlots",
		"ArtifactCharts",
		"Components",
		"ComponentDependencies",
		"ComponentOwnershipFence",
		"ExperimentComponentSnapshots",
		"WorkUnitStatusHistory",
		"PauseSchedulingState",
		"PausedTasks",
		"schema_migrations",
	}
	for _, table := range essTables {
		if !tableExists(ctx, t, essPool, table) {
			t.Errorf("ESS table %s does not exist after Setup", table)
		}
	}

	wantXIDMigration := migrationState{Version: version.XidExpectedSchemaVersion(), Dirty: false}
	if diff := cmp.Diff(wantXIDMigration, fetchMigrationState(ctx, t, xidPool)); diff != "" {
		t.Errorf("XID schema_migrations mismatch (-want +got):\n%s", diff)
	}

	wantESSMigration := migrationState{Version: version.EssExpectedSchemaVersion(), Dirty: false}
	if diff := cmp.Diff(wantESSMigration, fetchMigrationState(ctx, t, essPool)); diff != "" {
		t.Errorf("ESS schema_migrations mismatch (-want +got):\n%s", diff)
	}
}

func TestSetup_IsolationAndCleanup(t *testing.T) {
	ctx := t.Context()

	poolsA := SetupWithPools(ctx, t)
	poolsB := SetupWithPools(ctx, t)

	if poolsA.XIDDSN == "" || poolsA.ESSDSN == "" {
		t.Fatalf("SetupWithPools returned empty DSNs: %+v", poolsA)
	}
	if poolsA.XIDDSN == poolsB.XIDDSN || poolsA.ESSDSN == poolsB.ESSDSN {
		t.Fatalf("expected distinct DSNs across SetupWithPools calls, got %+v and %+v", poolsA, poolsB)
	}

	xidNameA := currentDatabase(ctx, t, poolsA.XIDPool)
	essNameA := currentDatabase(ctx, t, poolsA.ESSPool)
	if !strings.HasPrefix(xidNameA, "test_xid_db_") {
		t.Errorf("XID database name = %q, want prefix %q", xidNameA, "test_xid_db_")
	}
	if !strings.HasPrefix(essNameA, "test_ess_db_") {
		t.Errorf("ESS database name = %q, want prefix %q", essNameA, "test_ess_db_")
	}

	_, err := poolsA.XIDPool.Exec(
		ctx,
		`INSERT INTO Xids (Xid, Creator, OriginatingSystem, Endpoint, Acls, CreateTime, UpdateTime)
		 VALUES (42, 'alice', '\x', 'endpoint', '\x', NOW(), NOW())`,
	)
	if err != nil {
		t.Fatalf("failed to insert into poolsA.XIDPool: %v", err)
	}

	var countB int
	if err := poolsB.XIDPool.QueryRow(ctx, "SELECT COUNT(*) FROM Xids").Scan(&countB); err != nil {
		t.Fatalf("failed to count rows in poolsB.XIDPool: %v", err)
	}
	if countB != 0 {
		t.Errorf("poolsB.XIDPool row count = %d, want 0", countB)
	}
}

func TestSetupEmpty(t *testing.T) {
	ctx := t.Context()
	pools := SetupEmpty(ctx, t)

	if got := listTables(ctx, t, pools.XIDPool); len(got) != 0 {
		t.Errorf("SetupEmpty XIDPool tables = %v, want empty", got)
	}
	if got := listTables(ctx, t, pools.ESSPool); len(got) != 0 {
		t.Errorf("SetupEmpty ESSPool tables = %v, want empty", got)
	}
}

func TestSetup_WithPostgresTestDSNEnv(t *testing.T) {
	ctx := t.Context()
	baseDSN, err := ensurePostgresInstance()
	if err != nil {
		t.Fatalf("ensurePostgresInstance() failed: %v", err)
	}

	t.Setenv("POSTGRES_TEST_DSN", baseDSN)
	xidPool, essPool := Setup(ctx, t)
	if !tableExists(ctx, t, xidPool, "Xids") {
		t.Error("expected Xids table when using POSTGRES_TEST_DSN")
	}
	if !tableExists(ctx, t, essPool, "Experiments") {
		t.Error("expected Experiments table when using POSTGRES_TEST_DSN")
	}
}

func TestReplaceDatabaseInDSN(t *testing.T) {
	tests := []struct {
		name    string
		baseDSN string
		dbName  string
		want    string
		wantErr bool
	}{
		{
			name:    "replaces database path preserving query params",
			baseDSN: "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable",
			dbName:  "test_xid_db_abc123",
			want:    "postgres://postgres:postgres@localhost:5432/test_xid_db_abc123?sslmode=disable",
		},
		{
			name:    "invalid URL returns error",
			baseDSN: "://invalid-url",
			dbName:  "test_db",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := replaceDatabaseInDSN(tt.baseDSN, tt.dbName)
			if (err != nil) != tt.wantErr {
				t.Fatalf("replaceDatabaseInDSN() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("replaceDatabaseInDSN() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestErrorPaths(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()

	t.Run("waitForPostgresReady returns error on canceled context", func(t *testing.T) {
		if err := waitForPostgresReady(canceledCtx, "postgres://127.0.0.1:1/postgres"); err == nil {
			t.Error("waitForPostgresReady() = nil, want error on canceled context")
		}
	})

	t.Run("execAdminSQL returns error on unreachable DSN", func(t *testing.T) {
		if err := execAdminSQL(canceledCtx, "postgres://127.0.0.1:1/postgres", "SELECT 1"); err == nil {
			t.Error("execAdminSQL() = nil, want error on canceled context")
		}
	})
}
