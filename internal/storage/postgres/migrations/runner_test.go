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
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/go-cmp/cmp"
	"xmanager_cloud/internal/storage/migrations"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
)

// newTestDatabase creates an empty database on the shared postgrestest server, dropped when the
// test completes, and returns its DSN.
func newTestDatabase(t *testing.T) string {
	t.Helper()
	_, dsn := postgrestest.NewEmptyDatabase(t.Context(), t)
	return dsn
}

// applySchema runs the full ../schema/<migrationsDir>-schema.sql on the database at dsn without
// initializing schema_migrations.
func applySchema(ctx context.Context, t *testing.T, dsn, migrationsDir string) {
	t.Helper()
	ddl, err := os.ReadFile(filepath.Join("..", "schema", migrationsDir+"-schema.sql"))
	if err != nil {
		t.Fatalf("failed to read %s schema: %v", migrationsDir, err)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, string(ddl)); err != nil {
		t.Fatalf("failed to apply %s schema: %v", migrationsDir, err)
	}
}

func intPtr(v int) *int {
	return &v
}

func TestIsDatabaseEmpty(t *testing.T) {
	ctx := t.Context()

	dsn := newTestDatabase(t)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	empty, err := isDatabaseEmpty(ctx, db)
	if err != nil || !empty {
		t.Fatalf("isDatabaseEmpty(empty db) = (%v, %v), want (true, nil)", empty, err)
	}

	if _, err := db.ExecContext(ctx, "CREATE TABLE schema_migrations (version bigint not null primary key, dirty boolean not null)"); err != nil {
		t.Fatalf("failed to create schema_migrations: %v", err)
	}
	empty, err = isDatabaseEmpty(ctx, db)
	if err != nil || !empty {
		t.Fatalf("isDatabaseEmpty(only schema_migrations) = (%v, %v), want (true, nil)", empty, err)
	}

	if _, err := db.ExecContext(ctx, "CREATE TABLE experiments (experiment_id bigint primary key)"); err != nil {
		t.Fatalf("failed to create experiments table: %v", err)
	}
	empty, err = isDatabaseEmpty(ctx, db)
	if err != nil || empty {
		t.Fatalf("isDatabaseEmpty(with user table) = (%v, %v), want (false, nil)", empty, err)
	}
}

func TestIsDatabaseEmpty_CustomSearchPath(t *testing.T) {
	ctx := t.Context()

	dsn := newTestDatabase(t)
	db, err := sql.Open("postgres", dsn+"&search_path=custom_schema")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(ctx, "CREATE SCHEMA custom_schema; CREATE TABLE custom_schema.experiments (experiment_id bigint primary key)"); err != nil {
		t.Fatalf("failed to create custom_schema table: %v", err)
	}
	empty, err := isDatabaseEmpty(ctx, db)
	if err != nil || empty {
		t.Fatalf("isDatabaseEmpty(custom_schema with user table) = (%v, %v), want (false, nil)", empty, err)
	}
}

func TestRun(t *testing.T) {
	ctx := t.Context()
	schemas := []string{schemaESS, schemaXID}

	t.Run("Unsupported", func(t *testing.T) {
		for _, schema := range schemas {
			t.Run(schema, func(t *testing.T) {
				cfg := &config{
					dsn:           newTestDatabase(t),
					migrationsDir: schema,
					command:       "unknown",
				}
				if err := run(ctx, cfg); err == nil {
					t.Errorf("run(unknown) succeeded for %s, want error", schema)
				}
			})
		}
	})

	t.Run("Version", func(t *testing.T) {
		for _, schema := range schemas {
			t.Run(schema, func(t *testing.T) {
				cfg := &config{
					dsn:           newTestDatabase(t),
					migrationsDir: schema,
					command:       migrations.VersionCmd,
				}
				if err := run(ctx, cfg); err != nil {
					t.Fatalf("run(version) failed: %v", err)
				}
				verifyRunnerVersion(ctx, t, cfg, -1)
			})
		}
	})

	t.Run("Force", func(t *testing.T) {
		for _, schema := range schemas {
			t.Run(schema, func(t *testing.T) {
				runForceSubtests(ctx, t, schema)
			})
		}
	})

	t.Run("Up_MigrationsNotInitialized", func(t *testing.T) {
		for _, schema := range schemas {
			t.Run(schema, func(t *testing.T) {
				runUpUninitializedTest(ctx, t, schema)
			})
		}
	})

	t.Run("Up", func(t *testing.T) {
		for _, schema := range schemas {
			t.Run(schema, func(t *testing.T) {
				cfg := &config{
					dsn:           newTestDatabase(t),
					migrationsDir: schema,
					command:       migrations.UpCmd,
				}
				if err := run(ctx, cfg); err != nil {
					t.Fatalf("run(up) failed: %v", err)
				}
				if err := run(ctx, cfg); err != nil {
					t.Errorf("run(up) second time failed: %v", err)
				}
			})
		}
	})

	t.Run("Down", func(t *testing.T) {
		for _, schema := range schemas {
			t.Run(schema, func(t *testing.T) {
				runDownBaselineTest(ctx, t, schema)
			})
		}
	})

	t.Run("Init", func(t *testing.T) {
		for _, schema := range schemas {
			t.Run(schema, func(t *testing.T) {
				runInitTest(ctx, t, schema)
			})
		}
	})
}

func runForceSubtests(ctx context.Context, t *testing.T, schema string) {
	t.Helper()
	forceTests := []struct {
		name        string
		arg         *int
		wantErr     bool
		wantVersion int
		errContains string
	}{
		{
			name:        "valid version",
			arg:         intPtr(5),
			wantVersion: 5,
		},
		{
			name:        "reset version",
			arg:         intPtr(-1),
			wantVersion: -1,
		},
		{
			name:        "nil arg",
			arg:         nil,
			wantErr:     true,
			errContains: "missing argument for force command",
		},
	}

	dsn := newTestDatabase(t)
	for _, ft := range forceTests {
		t.Run(ft.name, func(t *testing.T) {
			cfg := &config{
				dsn:           dsn,
				migrationsDir: schema,
				command:       migrations.ForceCmd,
				arg:           ft.arg,
			}
			err := run(ctx, cfg)
			if (err != nil) != ft.wantErr {
				t.Fatalf("run(force) error = %v, wantErr %v", err, ft.wantErr)
			}
			if ft.wantErr {
				if !strings.Contains(err.Error(), ft.errContains) {
					t.Errorf("run(force) error = %v, want substring %q", err, ft.errContains)
				}
				return
			}
			verifyRunnerVersion(ctx, t, cfg, ft.wantVersion)
		})
	}
}

func verifyRunnerVersion(ctx context.Context, t *testing.T, cfg *config, wantVersion int) {
	t.Helper()
	r, err := newRunner(ctx, cfg)
	if err != nil {
		t.Fatalf("newRunner() failed: %v", err)
	}
	defer r.Close()

	v, dirty, err := r.Version()
	if wantVersion == -1 {
		if !errors.Is(err, migrate.ErrNilVersion) {
			t.Errorf("Version() error = %v, want %v", err, migrate.ErrNilVersion)
		}
		return
	}
	if err != nil {
		t.Fatalf("Version() failed: %v", err)
	}
	if int(v) != wantVersion {
		t.Errorf("Version() = %d, want %d", v, wantVersion)
	}
	if dirty {
		t.Errorf("Version() dirty = true, want false")
	}
}

func runUpUninitializedTest(ctx context.Context, t *testing.T, schema string) {
	t.Helper()
	dsn := newTestDatabase(t)
	applySchema(ctx, t, dsn, schema)

	cfg := &config{
		dsn:           dsn,
		migrationsDir: schema,
		command:       migrations.UpCmd,
	}
	verifyRunnerVersion(ctx, t, cfg, -1)

	err := run(ctx, cfg)
	if err == nil || !strings.Contains(err.Error(), "schema migrations table is not yet initialized") {
		t.Fatalf("run(up) on non-empty uninitialized DB = %v, want uninitialized error", err)
	}
	verifyRunnerVersion(ctx, t, cfg, -1)
}

func runDownBaselineTest(ctx context.Context, t *testing.T, schema string) {
	t.Helper()
	dsn := newTestDatabase(t)
	cfg := &config{
		dsn:           dsn,
		migrationsDir: schema,
		command:       migrations.UpCmd,
	}
	if err := run(ctx, cfg); err != nil {
		t.Fatalf("run(up) failed: %v", err)
	}

	r, err := newRunner(ctx, cfg)
	if err != nil {
		t.Fatalf("newRunner() failed: %v", err)
	}
	defer r.Close()
	r.Stdin = strings.NewReader("y\n")
	var stdout bytes.Buffer
	r.Stdout = &stdout

	vBefore, _, err := r.Version()
	if err != nil {
		t.Fatalf("Version() before down failed: %v", err)
	}
	if err := r.ExecCommand(ctx, migrations.DownCmd, intPtr(1)); err != nil {
		t.Fatalf("ExecCommand(down, 1) failed: %v", err)
	}

	vAfter, _, err := r.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		t.Fatalf("Version() after down failed: %v", err)
	}
	if errors.Is(err, migrate.ErrNilVersion) {
		assertDatabaseEmptyState(ctx, t, dsn, true)
	} else {
		if vAfter >= vBefore {
			t.Errorf("%s: Version() after down = %d, want < %d", schema, vAfter, vBefore)
		}
		assertDatabaseEmptyState(ctx, t, dsn, false)
	}
}

func assertDatabaseEmptyState(ctx context.Context, t *testing.T, dsn string, wantEmpty bool) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	empty, err := isDatabaseEmpty(ctx, db)
	if err != nil {
		t.Fatalf("isDatabaseEmpty failed: %v", err)
	}
	if empty != wantEmpty {
		t.Errorf("isDatabaseEmpty() = %v, want %v", empty, wantEmpty)
	}
}

func runInitTest(ctx context.Context, t *testing.T, schema string) {
	t.Helper()
	cfg := &config{
		dsn:           newTestDatabase(t),
		migrationsDir: schema,
		command:       migrations.InitCmd,
	}
	if err := run(ctx, cfg); err != nil {
		t.Fatalf("run(init) failed: %v", err)
	}

	r, err := newRunner(ctx, cfg)
	if err != nil {
		t.Fatalf("newRunner() failed: %v", err)
	}
	defer r.Close()

	versionAfterInit, dirty, err := r.Version()
	if err != nil {
		t.Fatalf("Version() after init failed: %v", err)
	}
	if dirty {
		t.Errorf("Version() dirty = true, want false")
	}

	// Init sets the version to the latest migration, so up is a no-op.
	cfg.command = migrations.UpCmd
	if err := run(ctx, cfg); err != nil {
		t.Fatalf("run(up) after init failed: %v", err)
	}
	versionAfterUp, dirtyAfterUp, err := r.Version()
	if err != nil {
		t.Fatalf("Version() after up failed: %v", err)
	}
	if versionAfterUp != versionAfterInit {
		t.Errorf("Version() after up = %d, want %d", versionAfterUp, versionAfterInit)
	}
	if dirtyAfterUp {
		t.Errorf("Version() dirty after up = true, want false")
	}
}

func TestRun_Validation(t *testing.T) {
	ctx := t.Context()
	baseCfg := config{
		dsn:           "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable",
		migrationsDir: schemaESS,
		command:       migrations.UpCmd,
	}

	tests := []struct {
		name    string
		mutator func(*config)
	}{
		{"missing migrations_dir", func(c *config) { c.migrationsDir = "" }},
		{"unknown migrations_dir", func(c *config) { c.migrationsDir = "invalid" }},
		{"missing command", func(c *config) { c.command = "" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseCfg
			tc.mutator(&cfg)
			if err := run(ctx, &cfg); err == nil {
				t.Errorf("run() with %s: expected error, got nil", tc.name)
			}
		})
	}
}

func TestParseConfig(t *testing.T) {
	const envDSN = "postgres://env-user@env-host:5433/env-db"
	const flagDSN = "postgres://flag-user@flag-host/flag-db"
	t.Setenv("POSTGRES_DSN", envDSN)

	tests := []struct {
		name    string
		args    []string
		want    *config
		wantErr bool
	}{
		{
			name: "normalizes names and falls back to POSTGRES_DSN",
			args: []string{"--migrations_dir=ESS", "--command=UP", "2"},
			want: &config{dsn: envDSN, migrationsDir: schemaESS, command: migrations.UpCmd, arg: intPtr(2)},
		},
		{
			name: "flag overrides POSTGRES_DSN",
			args: []string{"--postgres_dsn=" + flagDSN, "--migrations_dir=xid", "--command=version"},
			want: &config{dsn: flagDSN, migrationsDir: schemaXID, command: migrations.VersionCmd},
		},
		{
			name: "create takes a migration name",
			args: []string{"--migrations_dir=ess", "--command=create", "add_index"},
			want: &config{dsn: envDSN, migrationsDir: schemaESS, command: migrations.CreateCmd, migrationName: "add_index"},
		},
		{name: "non-integer argument", args: []string{"--command=up", "not_an_int"}, wantErr: true},
		{name: "too many arguments", args: []string{"--command=up", "1", "2"}, wantErr: true},
		{name: "unknown flag", args: []string{"--unknown_flag"}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseConfig(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseConfig(%q) error = %v, wantErr %v", tc.args, err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(config{})); diff != "" {
				t.Errorf("parseConfig(%q) mismatch (-want +got):\n%s", tc.args, diff)
			}
		})
	}
}
