// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"golang/cloud_google_com/go/spanner/v/v1/admin/database/apiv1/database"
	"golang/cmp/cmp"
	"golang/cmp/cmpopts/cmpopts"
	"golang/google_api/option/option"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/storage/spanner/migrations/version"

	databasepb "spanner/admin/database/v1/spanner_database_admin_go_proto"
)

// TestSchemaSynchronization verifies that the schemas in the *-schema.sql files are consistent
// with the schemas evolved by the migrations.
func TestSchemaSynchronization(t *testing.T) {
	ctx := t.Context()

	// SetupWithEmulator creates the ESS and XID databases using their respective schemas.
	emu := emulator.SetupWithEmulator(ctx, t).Emulator
	// SetupEmpty creates empty databases.
	emptyDBEmu := emulator.SetupEmpty(ctx, t).Emulator

	tests := []testRunCase{
		{
			name:          "ess",
			migrationsDir: "ess",
			databaseID:    "test-ess-db-" + emulator.RandomSuffix(),
			getDB: func(emu *emulator.Emulator) string {
				return emu.ESSDatabase
			},
		},
		{
			name:          "xid",
			migrationsDir: "xid",
			databaseID:    "test-xid-db-" + emulator.RandomSuffix(),
			getDB: func(emu *emulator.Emulator) string {
				return emu.XIDDatabase
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tcEmpty := withEmuDB(tc, emptyDBEmu)
			tcEmu := withEmuDB(tc, emu)

			// 1. Run all migrations up on an empty database and get the DDL.
			cfg := mustCreateConfig(t, tcEmpty, "up", nil)

			baselineDBNameMigrations := fmt.Sprintf("projects/%s/instances/%s/databases/%s", cfg.projectID, cfg.instanceID, tcEmpty.databaseID)

			if err := run(cfg, emptyDBEmu.ClientOptions()...); err != nil {
				t.Fatalf("run(up) failed: %v", err)
			}
			adminClientMigrations, err := database.NewDatabaseAdminClient(ctx, emptyDBEmu.ClientOptions()...)
			if err != nil {
				t.Fatalf("Failed to create admin client: %v", err)
			}
			defer adminClientMigrations.Close()
			ddlAfterMigrations, err := fetchDDL(ctx, adminClientMigrations, baselineDBNameMigrations)
			if err != nil {
				t.Fatalf("Failed to get DDL for empty DB after migrations: %v", err)
			}

			// 2. Get DDL from applying the full *-schema.sql DDL and run init to initialize SchemaMigrations table.
			cfgInit := mustCreateConfig(t, tcEmu, "init", nil)
			baselineDBNameFullSchema := fmt.Sprintf("projects/%s/instances/%s/databases/%s", cfgInit.projectID, cfgInit.instanceID, tcEmu.databaseID)
			if err := run(cfgInit, emu.ClientOptions()...); err != nil {
				t.Fatalf("run(init) failed: %v", err)
			}

			adminClient, err := database.NewDatabaseAdminClient(ctx, emu.ClientOptions()...)
			if err != nil {
				t.Fatalf("Failed to create admin client: %v", err)
			}
			defer adminClient.Close()
			ddlFromFullSchema, err := fetchDDL(ctx, adminClient, baselineDBNameFullSchema)
			if err != nil {
				t.Fatalf("Failed to get DDL for baseline DB: %v", err)
			}

			// 3. Compare schema versions.
			cfgVersionMigrations := mustCreateConfig(t, tcEmpty, "version", nil)
			cfgVersionBaseline := mustCreateConfig(t, tcEmu, "version", nil)
			versionMigrations, dirtyMigrations := mustFetchVersion(ctx, t, cfgVersionMigrations, emptyDBEmu.ClientOptions()...)
			versionBaseline, dirtyBaseline := mustFetchVersion(ctx, t, cfgVersionBaseline, emu.ClientOptions()...)

			if versionMigrations != versionBaseline {
				t.Errorf("Schema version mismatch: migrations=%d, baseline=%d", versionMigrations, versionBaseline)
			}
			if dirtyMigrations || dirtyBaseline {
				t.Errorf("Schema is dirty: migrations=%v, baseline=%v", dirtyMigrations, dirtyBaseline)
			}

			var expectedVersion int64
			switch tc.name {
			case "ess":
				expectedVersion = version.EssExpectedSchemaVersion()
			case "xid":
				expectedVersion = version.XidExpectedSchemaVersion()
			default:
				t.Fatalf("Unknown test case name: %s", tc.name)
			}
			if int64(versionMigrations) != expectedVersion {
				t.Errorf("Actual migration version %d does not match expected version %d", versionMigrations, expectedVersion)
			}

			// 4. Compare schemas.
			if diff := diffSchemas(ddlFromFullSchema, ddlAfterMigrations); diff != "" {
				t.Errorf("Schema mismatch (-baseline +migration):\n%s", diff)
			}
		})
	}
}

// TestMigrationUpDownConsistency verifies that applying a migration and then reverting it
// results in the same database schema.
func TestMigrationUpDownConsistency(t *testing.T) {
	ctx := t.Context()

	emu := emulator.SetupEmpty(ctx, t).Emulator

	tests := []testRunCase{
		{
			name:          "ess",
			migrationsDir: "ess",
			databaseID:    "test-ess-db-" + emulator.RandomSuffix(),
			getDB: func(emu *emulator.Emulator) string {
				return emu.ESSDatabase
			},
		},
		{
			name:          "xid",
			migrationsDir: "xid",
			databaseID:    "test-xid-db-" + emulator.RandomSuffix(),
			getDB: func(emu *emulator.Emulator) string {
				return emu.XIDDatabase
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc = withEmuDB(tc, emu)
			cfg := mustCreateConfig(t, tc, "version", nil)
			runner, err := newRunner(ctx, cfg, emu.ClientOptions()...)
			if err != nil {
				t.Fatalf("Failed to create runner: %v", err)
			}
			defer runner.close()

			adminClient, err := database.NewDatabaseAdminClient(ctx, emu.ClientOptions()...)
			if err != nil {
				t.Fatalf("Failed to create admin client: %v", err)
			}
			defer adminClient.Close()

			dbPath := fmt.Sprintf("projects/%s/instances/%s/databases/%s", cfg.projectID, cfg.instanceID, cfg.databaseID)

			nextV, nextVErr := runner.source.First()
			for nextVErr == nil {
				// 1. Get DDL before moving to nextV.
				ddlBefore, err := fetchDDL(ctx, adminClient, dbPath)
				if err != nil {
					t.Fatalf("Failed to get DDL: %v", err)
				}

				// 2. Step UP to move to nextV.
				if err := runner.execCommand(ctx, "up", ptr(1)); err != nil {
					t.Fatalf("Failed to step up: %v", err)
				}

				// 3. Step DOWN to return to previous version.
				runner.stdin = strings.NewReader("y\n")
				if err := runner.execCommand(ctx, "down", ptr(1)); err != nil {
					t.Fatalf("Failed to step down from version %d: %v", nextV, err)
				}

				// 4. Get DDL after reverting.
				ddlAfter, err := fetchDDL(ctx, adminClient, dbPath)
				if err != nil {
					t.Fatalf("Failed to get DDL after revert: %v", err)
				}

				// 5. Compare schemas.
				if diff := diffSchemas(ddlBefore, ddlAfter); diff != "" {
					t.Errorf("Schema mismatch after up/down for migration version %d (-before +reverted):\n%s", nextV, diff)
				}

				// 6. Step UP again to move permanently to nextV and proceed to the next iteration.
				if err := runner.execCommand(ctx, "up", ptr(1)); err != nil {
					t.Fatalf("Failed to step up again to version %d: %v", nextV, err)
				}

				// Move to next version. If there is no next version, nextVErr will be os.ErrNotExist.
				nextV, nextVErr = runner.source.Next(nextV)
			}

			if !errors.Is(nextVErr, os.ErrNotExist) {
				t.Fatalf("Failed to get next migration version: %v", nextVErr)
			}
		})
	}
}

func fetchDDL(ctx context.Context, client *database.DatabaseAdminClient, db string) ([]string, error) {
	req := &databasepb.GetDatabaseDdlRequest{
		Database: db,
	}
	resp, err := client.GetDatabaseDdl(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Statements, nil
}

func mustFetchVersion(ctx context.Context, t *testing.T, cfg *config, opts ...option.ClientOption) (uint, bool) {
	t.Helper()
	r, err := newRunner(ctx, cfg, opts...)
	if err != nil {
		t.Fatalf("Failed to create runner: %v", err)
	}
	defer r.close()

	v, dirty, err := r.migrator.Version()
	if err != nil {
		t.Fatalf("Failed to get version: %v", err)
	}
	return v, dirty
}

func diffSchemas(expected, actual []string) string {
	return cmp.Diff(normalizeDDL(expected), normalizeDDL(actual), cmpopts.SortSlices(func(a, b string) bool {
		return a < b
	}))
}

func normalizeDDL(ddl []string) []string {
	normalized := make([]string, len(ddl))
	for i, stmt := range ddl {
		if strings.HasPrefix(stmt, "CREATE TABLE") {
			normalized[i] = normalizeCreateTable(stmt)
		} else {
			normalized[i] = stmt
		}
	}
	return normalized
}

// normalizeCreateTable is a helper function that allows comparing DDL statements while ignoring the
// order of columns.
func normalizeCreateTable(stmt string) string {
	start := strings.Index(stmt, "(")
	if start == -1 {
		return stmt
	}

	// Find the matching ')' for the opening '('
	end := findMatchingBracket(stmt, start)
	if end == -1 {
		return stmt
	}

	header := strings.TrimSpace(stmt[:start+1])
	body := stmt[start+1 : end]
	footer := strings.TrimSpace(stmt[end:])

	// Split at depth 0, sort, and reassemble
	parts := splitSQLColumns(body)
	sort.Strings(parts)

	return fmt.Sprintf("%s\n  %s\n%s", header, strings.Join(parts, ",\n  "), footer)
}

func findMatchingBracket(s string, start int) int {
	depth := 0
	for i := start; i < len(s); i++ {
		if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitSQLColumns(s string) []string {
	var res []string
	var start, depth int
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				res = append(res, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if final := strings.TrimSpace(s[start:]); final != "" {
		res = append(res, final)
	}
	return res
}
