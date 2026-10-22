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
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/go-cmp/cmp"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"xmanager_cloud/internal/storage/migrations"
)

const testPostgresImage = "postgres:16-alpine"

var (
	postgresOnce            sync.Once
	sharedPostgresDSN       string
	sharedPostgresErr       error
	sharedPostgresContainer testcontainers.Container
	dbCounter               atomic.Uint64
)

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedPostgresContainer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = sharedPostgresContainer.Terminate(ctx)
		cancel()
	}
	os.Exit(code)
}

func ensurePostgresInstance() (string, error) {
	if dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN")); dsn != "" {
		return dsn, nil
	}
	postgresOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		req := testcontainers.ContainerRequest{
			Image:        testPostgresImage,
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_USER":     "postgres",
				"POSTGRES_PASSWORD": "postgres",
				"POSTGRES_DB":       "postgres",
			},
			Cmd: []string{
				"-c", "fsync=off",
				"-c", "synchronous_commit=off",
				"-c", "full_page_writes=off",
			},
			WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			// GenericContainer can return a created container together with an
			// error (e.g. when the wait strategy times out). Terminate it here
			// because TestMain only cleans up sharedPostgresContainer, and CI
			// runs without the Ryuk reaper.
			if termErr := testcontainers.TerminateContainer(c); termErr != nil {
				err = errors.Join(err, fmt.Errorf("terminate partially started container: %w", termErr))
			}
			sharedPostgresErr = fmt.Errorf("failed to start postgres testcontainer: %w", err)
			return
		}
		sharedPostgresContainer = c
		dsn, err := containerDSN(ctx, c)
		if err != nil {
			sharedPostgresErr = err
			return
		}
		if err := waitForPostgresReady(ctx, dsn); err != nil {
			sharedPostgresErr = err
			return
		}
		sharedPostgresDSN = dsn
	})
	return sharedPostgresDSN, sharedPostgresErr
}

func containerDSN(ctx context.Context, c testcontainers.Container) (string, error) {
	host, err := c.Host(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get container host: %w", err)
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return "", fmt.Errorf("failed to get mapped postgres port: %w", err)
	}
	addr := net.JoinHostPort(host, port.Port())
	return fmt.Sprintf("postgres://postgres:postgres@%s/postgres?sslmode=disable", addr), nil
}

func waitForPostgresReady(ctx context.Context, dsn string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		if err := db.PingContext(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for postgres readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func randomHexSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b)
}

func newTestDatabase(t *testing.T) string {
	t.Helper()
	baseDSN, err := ensurePostgresInstance()
	if err != nil {
		t.Fatalf("ensurePostgresInstance: %v", err)
	}
	adminDB, err := sql.Open("postgres", baseDSN)
	if err != nil {
		t.Fatalf("sql.Open(admin): %v", err)
	}
	defer adminDB.Close()

	dbName := fmt.Sprintf("test_db_%d_%s", dbCounter.Add(1), randomHexSuffix(t))
	if _, err := adminDB.ExecContext(t.Context(), fmt.Sprintf("CREATE DATABASE %s", dbName)); err != nil {
		t.Fatalf("failed to create test database %s: %v", dbName, err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cleanDB, err := sql.Open("postgres", baseDSN)
		if err != nil {
			return
		}
		defer cleanDB.Close()
		_, _ = cleanDB.ExecContext(cleanupCtx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName))
	})

	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("url.Parse(baseDSN): %v", err)
	}
	u.Path = "/" + dbName
	return u.String()
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
