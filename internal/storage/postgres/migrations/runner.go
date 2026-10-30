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

// Package main is the CLI entry point for the PostgreSQL migration runner.
package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/dbconfig"
	"xmanager_cloud/internal/storage/migrations"
	"xmanager_cloud/internal/storage/migrations/commands"
)

//go:embed ess/*.sql xid/*.sql
var migrationsFS embed.FS

// Supported schema names.
const (
	schemaESS = "ess"
	schemaXID = "xid"
)

const (
	// migrationsBaseDir is the workspace-relative directory that contains the migrations subdirectories.
	migrationsBaseDir = "internal/storage/postgres/migrations"

	initialPingBackoff = 500 * time.Millisecond
	maxPingBackoff     = 5 * time.Second
	pingAttemptTimeout = 5 * time.Second

	checkNonEmptyDBQuery = `
SELECT 1
FROM information_schema.tables
WHERE table_schema = current_schema()
  AND table_name != 'schema_migrations'
LIMIT 1
`
)

type config struct {
	// dsn is the PostgreSQL connection string. Settings it leaves out are taken by the lib/pq driver
	// from the standard PG* environment variables (PGHOST, PGUSER, PGSSLMODE, ...).
	dsn            string
	migrationsDir  string
	command        string
	connectTimeout time.Duration
	arg            *int
	migrationName  string
}

// parseConfig parses CLI arguments into a config, falling back to the POSTGRES_DSN environment variable.
func parseConfig(args []string) (*config, error) {
	cfg := &config{}
	fs := flag.NewFlagSet("migrations", flag.ContinueOnError)
	fs.StringVar(&cfg.dsn, "postgres_dsn", "", "PostgreSQL connection DSN (defaults to POSTGRES_DSN or dbconfig)")
	fs.StringVar(&cfg.migrationsDir, "migrations_dir", "", "Migrations directory ('ess' or 'xid')")
	fs.StringVar(&cfg.command, "command", "", "Command to run: up, down, force, version, init, create")
	fs.DurationVar(&cfg.connectTimeout, "connect_timeout", 0, "Maximum time to wait for PostgreSQL connection readiness (0 disables retry)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg.migrationsDir = strings.ToLower(cfg.migrationsDir)
	cfg.command = strings.ToLower(cfg.command)
	arg, migrationName, err := migrations.ParseArg(cfg.command, fs.Args())
	if err != nil {
		return nil, err
	}
	cfg.arg = arg
	cfg.migrationName = migrationName
	if cfg.dsn == "" {
		cfg.dsn = os.Getenv("POSTGRES_DSN")
	}
	return cfg, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		logging.Fatal(ctx, "failed to parse arguments", "error", err)
	}
	if err := run(ctx, cfg); err != nil {
		logging.Fatal(ctx, "migration failed", "error", err)
	}
}

func run(ctx context.Context, cfg *config) error {
	if cfg.command == "" {
		return fmt.Errorf("missing required flag: --command")
	}
	if cfg.migrationsDir == "" {
		return fmt.Errorf("missing required flag: --migrations_dir")
	}
	if cfg.command == migrations.CreateCmd {
		return commands.Create(filepath.Join(migrationsBaseDir, cfg.migrationsDir), cfg.migrationName)
	}

	runner, err := newRunner(ctx, cfg)
	if err != nil {
		return err
	}
	defer runner.Close()

	return runner.ExecCommand(ctx, cfg.command, cfg.arg)
}

// resolveDSN returns the trimmed DSN from cfg, or loads it from dbconfig when unset.
func resolveDSN(cfg *config) (string, error) {
	if dsn := strings.TrimSpace(cfg.dsn); dsn != "" {
		return dsn, nil
	}
	dbCfg, err := dbconfig.Load()
	if err != nil {
		return "", fmt.Errorf("failed to load database configuration: %w", err)
	}
	if dbCfg.Backend != dbconfig.BackendPostgres {
		return "", fmt.Errorf("postgres_dsn is not set and DB_BACKEND is %q (expected %q)", dbCfg.Backend, dbconfig.BackendPostgres)
	}
	if cfg.migrationsDir == schemaXID {
		return dbCfg.Postgres.XidDSN()
	}
	return dbCfg.Postgres.EssDSN()
}

func pingOnce(ctx context.Context, ping func(context.Context) error) error {
	pingCtx, cancel := context.WithTimeout(ctx, pingAttemptTimeout)
	defer cancel()
	return ping(pingCtx)
}

// pingWithRetry pings the database once when timeout <= 0, or retries with exponential backoff until timeout expires.
func pingWithRetry(ctx context.Context, ping func(context.Context) error, timeout time.Duration) error {
	if timeout <= 0 {
		return pingOnce(ctx, ping)
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	backoff := initialPingBackoff
	for {
		lastErr := pingOnce(deadlineCtx, ping)
		if lastErr == nil {
			return nil
		}
		timer := time.NewTimer(backoff)
		select {
		case <-deadlineCtx.Done():
			timer.Stop()
			return lastErr
		case <-timer.C:
		}
		backoff = min(backoff*2, maxPingBackoff)
	}
}

// newRunner opens a dedicated *sql.DB from cfg and returns a migration runner that uses it.
// Closing the runner also closes the connection.
func newRunner(ctx context.Context, cfg *config) (*migrations.Runner, error) {
	if cfg.migrationsDir != schemaESS && cfg.migrationsDir != schemaXID {
		return nil, fmt.Errorf("unknown migrations_dir %q: expected %q or %q", cfg.migrationsDir, schemaESS, schemaXID)
	}
	dsn, err := resolveDSN(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}
	if err := pingWithRetry(ctx, db.PingContext, cfg.connectTimeout); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping postgres database: %w", err)
	}
	migrator, sourceDriver, err := createMigrator(db, cfg.migrationsDir)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return migrations.NewRunner(migrator, sourceDriver, func(ctx context.Context) (bool, error) {
		return isDatabaseEmpty(ctx, db)
	}), nil
}

func createMigrator(db *sql.DB, migrationsDir string) (*migrate.Migrate, source.Driver, error) {
	dbDriver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize postgres migration driver: %w", err)
	}
	sourceDriver, err := iofs.New(migrationsFS, migrationsDir)
	if err != nil {
		_ = dbDriver.Close()
		return nil, nil, fmt.Errorf("failed to create source driver: %w", err)
	}
	migrator, err := migrate.NewWithInstance("iofs", sourceDriver, "postgres", dbDriver)
	if err != nil {
		_ = sourceDriver.Close()
		_ = dbDriver.Close()
		return nil, nil, fmt.Errorf("failed to create migrator: %w", err)
	}
	return migrator, sourceDriver, nil
}

// isDatabaseEmpty checks whether the current PostgreSQL schema contains any user tables other than schema_migrations.
func isDatabaseEmpty(ctx context.Context, db *sql.DB) (bool, error) {
	var dummy int
	err := db.QueryRowContext(ctx, checkNonEmptyDBQuery).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}
