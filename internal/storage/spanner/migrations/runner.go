// Copyright 2025 Google LLC
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

// Runner is the main entry point for the Spanner migration runner.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"cloud.google.com/go/spanner"
	database "cloud.google.com/go/spanner/admin/database/apiv1"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/migrations"
	"xmanager_cloud/internal/storage/migrations/commands"
	"xmanager_cloud/internal/storage/spanner/migrations/driver"
)

//go:embed ess_messages.pb
var essMessagesDescriptor []byte

//go:embed xid_messages.pb
var xidMessagesDescriptor []byte

//go:embed ess/*.sql xid/*.sql
var migrationsFS embed.FS

// migrationsBaseDir is the workspace-relative directory that contains the migrations subdirectories.
const migrationsBaseDir = "internal/storage/spanner/migrations"

type config struct {
	projectID     string
	instanceID    string
	databaseID    string
	migrationsDir string
	command       string
	arg           *int
	migrationName string
	// customSourceDriver is defined to allow passing custom implementation for testing.
	// It can be removed once Spanner emulator issue in (internal issue) is resolved.
	customSourceDriver source.Driver
}

func parseConfig() *config {
	cfg := &config{}
	flag.StringVar(&cfg.projectID, "project", "", "Google Cloud Project ID")
	flag.StringVar(&cfg.instanceID, "instance", "", "Spanner Instance ID")
	flag.StringVar(&cfg.databaseID, "database", "", "Spanner Database ID")
	flag.StringVar(&cfg.migrationsDir, "migrations_dir", "", "Name of the migrations subdirectory (e.g., 'ess' or 'xid')")
	flag.StringVar(&cfg.command, "command", "", fmt.Sprintf("Command to run: %s, %s, %s, %s, %s", migrations.UpCmd, migrations.DownCmd, migrations.ForceCmd, migrations.VersionCmd, migrations.InitCmd))
	flag.Parse()

	arg, migrationName, err := migrations.ParseArg(cfg.command, flag.Args())
	if err != nil {
		logging.Fatal(context.Background(), "failed to parse arguments", "error", err)
	}
	cfg.arg = arg
	cfg.migrationName = migrationName

	cfg.migrationsDir = strings.ToLower(cfg.migrationsDir)
	cfg.command = strings.ToLower(cfg.command)

	return cfg
}

func main() {
	cfg := parseConfig()
	if err := run(cfg); err != nil {
		logging.Fatal(context.Background(), "migration failed", "error", err)
	}
}

func run(cfg *config, opts ...option.ClientOption) error {
	if cfg.command == "" {
		return fmt.Errorf("missing required flag: --command")
	}

	if cfg.command == migrations.CreateCmd {
		if cfg.migrationsDir == "" {
			return fmt.Errorf("missing required flag: --migrations_dir (required for %q)", migrations.CreateCmd)
		}
		return commands.Create(filepath.Join(migrationsBaseDir, cfg.migrationsDir), cfg.migrationName)
	}

	if cfg.projectID == "" || cfg.instanceID == "" || cfg.databaseID == "" || cfg.migrationsDir == "" {
		return fmt.Errorf("missing required flags: --project, --instance, --database, --migrations_dir")
	}

	ctx := context.Background()
	runner, err := newRunner(ctx, cfg, opts...)
	if err != nil {
		return err
	}
	defer runner.Close()

	return runner.ExecCommand(ctx, cfg.command, cfg.arg)
}

func newRunner(ctx context.Context, cfg *config, opts ...option.ClientOption) (*migrations.Runner, error) {
	dbName := fmt.Sprintf("projects/%s/instances/%s/databases/%s", cfg.projectID, cfg.instanceID, cfg.databaseID)
	clientOpts := append([]option.ClientOption{option.WithQuotaProject(cfg.projectID)}, opts...)

	descriptors, err := selectDescriptor(cfg.migrationsDir)
	if err != nil {
		return nil, err
	}

	adminClient, err := database.NewDatabaseAdminClient(ctx, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create admin client: %w", err)
	}

	dataClient, err := spanner.NewClient(ctx, dbName, clientOpts...)
	if err != nil {
		adminClient.Close()
		return nil, fmt.Errorf("failed to create data client: %w", err)
	}

	dbDriver, err := spannerdriver.New(spannerdriver.DriverOptions{
		DatabasePath:     dbName,
		ProtoDescriptors: descriptors,
		AdminClient:      adminClient,
		DataClient:       dataClient,
	})
	if err != nil {
		dataClient.Close()
		adminClient.Close()
		return nil, fmt.Errorf("failed to initialize database driver: %w", err)
	}

	sourceDriver := cfg.customSourceDriver
	if sourceDriver == nil {
		var err error
		sourceDriver, err = iofs.New(migrationsFS, cfg.migrationsDir)
		if err != nil {
			dbDriver.Close()
			return nil, fmt.Errorf("failed to create source driver: %w", err)
		}
	}

	migrator, err := migrate.NewWithInstance("iofs", sourceDriver, "spanner", dbDriver)
	if err != nil {
		sourceDriver.Close()
		dbDriver.Close()
		return nil, fmt.Errorf("failed to create migrator: %w", err)
	}

	return migrations.NewRunner(migrator, sourceDriver, func(ctx context.Context) (bool, error) {
		return isDatabaseEmpty(ctx, dataClient)
	}), nil
}

// isDatabaseEmpty checks if the Spanner database contains any user-defined tables.
func isDatabaseEmpty(ctx context.Context, dataClient *spanner.Client) (bool, error) {
	// Query the information schema to check for user-defined tables. We exclude the SchemaMigrations
	// table from the check because it can exist on empty db with nil version after running the "down" command.
	stmt := spanner.Statement{
		SQL: `SELECT 1 FROM INFORMATION_SCHEMA.TABLES
              WHERE TABLE_SCHEMA NOT IN ('INFORMATION_SCHEMA', 'SPANNER_SYS')
              AND TABLE_NAME != 'SchemaMigrations' LIMIT 1`,
	}
	iter := dataClient.Single().Query(ctx, stmt)
	defer iter.Stop()
	_, err := iter.Next()
	if err == iterator.Done {
		// No rows were returned, so the database is empty of user tables.
		return true, nil
	}
	if err != nil {
		return false, err
	}

	// A row was found, so the database is not empty.
	return false, nil
}

func selectDescriptor(migrationsDir string) ([]byte, error) {
	switch migrationsDir {
	case "ess":
		return essMessagesDescriptor, nil
	case "xid":
		return xidMessagesDescriptor, nil
	default:
		return nil, fmt.Errorf("unknown migrationsDir %q, no embedded descriptors found", migrationsDir)
	}
}
