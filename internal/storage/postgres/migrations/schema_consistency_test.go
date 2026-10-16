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

package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/go-cmp/cmp"
	"xmanager_cloud/internal/storage/migrations"
	"xmanager_cloud/internal/storage/postgres/migrations/version"
)

type postgresSchemaSnapshot struct {
	Tables  []string
	Columns []string
	Views   []string
	Indexes []string
}

const (
	inspectTablesQuery = `
SELECT table_name, table_type
FROM information_schema.tables
WHERE table_schema = current_schema()
  AND table_name != 'schema_migrations'
ORDER BY table_name
`

	inspectColumnsQuery = `
SELECT table_name, column_name, data_type, udt_name, is_nullable,
       COALESCE(column_default, ''), COALESCE(is_generated, ''), COALESCE(generation_expression, '')
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name != 'schema_migrations'
ORDER BY table_name, column_name
`

	inspectViewsQuery = `
SELECT table_name, COALESCE(view_definition, '')
FROM information_schema.views
WHERE table_schema = current_schema()
ORDER BY table_name
`

	inspectIndexesQuery = `
SELECT tablename, indexname, indexdef
FROM pg_catalog.pg_indexes
WHERE schemaname = current_schema()
  AND tablename != 'schema_migrations'
ORDER BY tablename, indexname
`
)

// TestSchemaSynchronization verifies that the schemas in the *-schema.sql files are consistent
// with the schemas evolved by the migrations.
func TestSchemaSynchronization(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name            string
		schemaDir       string
		expectedVersion int64
	}{
		{
			name:            schemaESS,
			schemaDir:       schemaESS,
			expectedVersion: version.EssExpectedSchemaVersion(),
		},
		{
			name:            schemaXID,
			schemaDir:       schemaXID,
			expectedVersion: version.XidExpectedSchemaVersion(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			migrationsDSN := newTestDatabase(t)
			baselineDSN := newTestDatabase(t)

			// 1. Run all migrations up on an empty database and inspect the resulting schema.
			upCfg := &config{dsn: migrationsDSN, migrationsDir: tc.schemaDir, command: migrations.UpCmd}
			if err := run(ctx, upCfg); err != nil {
				t.Fatalf("run(up) failed: %v", err)
			}
			schemaAfterMigrations := mustFetchPostgresSchema(ctx, t, migrationsDSN)

			// 2. Apply full *-schema.sql on a second database, run init, and inspect the resulting schema.
			applySchema(ctx, t, baselineDSN, tc.schemaDir)
			initCfg := &config{dsn: baselineDSN, migrationsDir: tc.schemaDir, command: migrations.InitCmd}
			if err := run(ctx, initCfg); err != nil {
				t.Fatalf("run(init) failed: %v", err)
			}
			schemaFromFullDDL := mustFetchPostgresSchema(ctx, t, baselineDSN)

			// 3. Compare schema versions and verify they match expected build-time versions.
			versionMigrations, dirtyMigrations := mustFetchMigrationVersion(ctx, t, migrationsDSN, tc.schemaDir)
			versionBaseline, dirtyBaseline := mustFetchMigrationVersion(ctx, t, baselineDSN, tc.schemaDir)

			if versionMigrations != versionBaseline {
				t.Errorf("schema version mismatch: migrations=%d, baseline=%d", versionMigrations, versionBaseline)
			}
			if dirtyMigrations || dirtyBaseline {
				t.Errorf("schema is dirty: migrations=%v, baseline=%v", dirtyMigrations, dirtyBaseline)
			}
			if int64(versionMigrations) != tc.expectedVersion {
				t.Errorf("migration version = %d, want expected version %d", versionMigrations, tc.expectedVersion)
			}

			// 4. Compare schemas.
			if len(schemaAfterMigrations.Tables) == 0 {
				t.Errorf("expected non-empty table list for %s schema", tc.schemaDir)
			}
			if diff := cmp.Diff(schemaFromFullDDL, schemaAfterMigrations); diff != "" {
				t.Errorf("schema mismatch (-baseline +migrations):\n%s", diff)
			}
		})
	}
}

// TestMigrationUpDownConsistency verifies that applying each migration and then reverting it
// results in the exact same PostgreSQL database schema.
func TestMigrationUpDownConsistency(t *testing.T) {
	ctx := t.Context()

	for _, schemaDir := range []string{schemaESS, schemaXID} {
		t.Run(schemaDir, func(t *testing.T) {
			dsn := newTestDatabase(t)
			runner, err := newRunner(ctx, &config{dsn: dsn, migrationsDir: schemaDir})
			if err != nil {
				t.Fatalf("newRunner(%s) failed: %v", schemaDir, err)
			}
			defer runner.Close()
			runner.Stdout = io.Discard

			src, err := iofs.New(migrationsFS, schemaDir)
			if err != nil {
				t.Fatalf("iofs.New(%s) failed: %v", schemaDir, err)
			}
			defer src.Close()

			migrationCount := 0
			nextV, nextVErr := src.First()
			for nextVErr == nil {
				migrationCount++
				schemaBefore := mustFetchPostgresSchema(ctx, t, dsn)

				if err := runner.ExecCommand(ctx, migrations.UpCmd, intPtr(1)); err != nil {
					t.Fatalf("failed to step up to version %d: %v", nextV, err)
				}

				runner.Stdin = strings.NewReader("y\n")
				if err := runner.ExecCommand(ctx, migrations.DownCmd, intPtr(1)); err != nil {
					t.Fatalf("failed to step down from version %d: %v", nextV, err)
				}

				schemaAfter := mustFetchPostgresSchema(ctx, t, dsn)
				if diff := cmp.Diff(schemaBefore, schemaAfter); diff != "" {
					t.Errorf("schema mismatch after up/down for version %d (-before +reverted):\n%s", nextV, diff)
				}

				if err := runner.ExecCommand(ctx, migrations.UpCmd, intPtr(1)); err != nil {
					t.Fatalf("failed to step up again to version %d: %v", nextV, err)
				}

				nextV, nextVErr = src.Next(nextV)
			}

			if !errors.Is(nextVErr, os.ErrNotExist) {
				t.Fatalf("failed to advance migration version: %v", nextVErr)
			}
			if migrationCount == 0 {
				t.Fatalf("%s: no migrations were tested", schemaDir)
			}
		})
	}
}

func mustFetchMigrationVersion(ctx context.Context, t *testing.T, dsn, schemaDir string) (uint, bool) {
	t.Helper()
	r, err := newRunner(ctx, &config{dsn: dsn, migrationsDir: schemaDir})
	if err != nil {
		t.Fatalf("newRunner failed: %v", err)
	}
	defer r.Close()

	v, dirty, err := r.Version()
	if err != nil {
		t.Fatalf("Version() failed: %v", err)
	}
	return v, dirty
}

func mustFetchPostgresSchema(ctx context.Context, t *testing.T, dsn string) postgresSchemaSnapshot {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	tables, err := queryFormattedRows(ctx, db, inspectTablesQuery, 2)
	if err != nil {
		t.Fatalf("failed to query tables: %v", err)
	}
	columns, err := queryFormattedRows(ctx, db, inspectColumnsQuery, 8)
	if err != nil {
		t.Fatalf("failed to query columns: %v", err)
	}
	views, err := queryFormattedRows(ctx, db, inspectViewsQuery, 2)
	if err != nil {
		t.Fatalf("failed to query views: %v", err)
	}
	indexes, err := queryFormattedRows(ctx, db, inspectIndexesQuery, 3)
	if err != nil {
		t.Fatalf("failed to query indexes: %v", err)
	}

	return postgresSchemaSnapshot{
		Tables:  tables,
		Columns: columns,
		Views:   views,
		Indexes: indexes,
	}
}

func queryFormattedRows(ctx context.Context, db *sql.DB, query string, numCols int) ([]string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []string
	vals := make([]string, numCols)
	scanArgs := make([]any, numCols)
	for i := range vals {
		scanArgs[i] = &vals[i]
	}

	for rows.Next() {
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}
		result = append(result, strings.Join(vals, "|"))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
