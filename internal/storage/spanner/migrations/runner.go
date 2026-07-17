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
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"cloud.google.com/go/spanner/admin/database/apiv1"
	"flag"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"google.golang.org/api/option"
	"log"
	"xmanager_cloud/storage/spanner/migrations/commands/commands"
	"xmanager_cloud/storage/spanner/migrations/driver/spannerdriver"
)

//go:embed xmanager_cloud/experiment_state_server/proto/ess_messages.pb
var essMessagesDescriptor []byte

//go:embed xmanager_cloud/xid_service/proto/xid_messages.pb
var xidMessagesDescriptor []byte

//go:embed ess/*.sql xid/*.sql
var migrationsFS embed.FS

// Commands supported by the runner.
const (
	upCmd      = "up"
	downCmd    = "down"
	forceCmd   = "force"
	versionCmd = "version"
	initCmd    = "init"
	createCmd  = "create"
)

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

func parseArg(cfg *config, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) > 1 {
		return fmt.Errorf("too many arguments")
	}

	if cfg.command == createCmd {
		cfg.migrationName = args[0]
		return nil
	}

	argVal, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid argument: %q is not an integer", args[0])
	}
	cfg.arg = &argVal
	return nil
}

func parseConfig() *config {
	cfg := &config{}
	flag.StringVar(&cfg.projectID, "project", "", "Google Cloud Project ID")
	flag.StringVar(&cfg.instanceID, "instance", "", "Spanner Instance ID")
	flag.StringVar(&cfg.databaseID, "database", "", "Spanner Database ID")
	flag.StringVar(&cfg.migrationsDir, "migrations_dir", "", "Name of the migrations subdirectory (e.g., 'ess' or 'xid')")
	flag.StringVar(&cfg.command, "command", "", fmt.Sprintf("Command to run: %s, %s, %s, %s, %s", upCmd, downCmd, forceCmd, versionCmd, initCmd))
	flag.Parse()
	if err := parseArg(cfg, flag.Args()); err != nil {
		log.Fatal(err)
	}

	cfg.migrationsDir = strings.ToLower(cfg.migrationsDir)
	cfg.command = strings.ToLower(cfg.command)

	return cfg
}

func main() {
	cfg := parseConfig()
	if err := run(cfg); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
}

func run(cfg *config, opts ...option.ClientOption) error {
	if cfg.command == "" {
		return fmt.Errorf("missing required flag: --command")
	}

	if cfg.command == createCmd {
		if cfg.migrationsDir == "" {
			return fmt.Errorf("missing required flag: --migrations_dir (required for %q)", createCmd)
		}
		return commands.Create(cfg.migrationsDir, cfg.migrationName)
	}

	if cfg.projectID == "" || cfg.instanceID == "" || cfg.databaseID == "" || cfg.migrationsDir == "" {
		return fmt.Errorf("missing required flags: --project, --instance, --database, --migrations_dir")
	}

	ctx := context.Background()
	runner, err := newRunner(ctx, cfg, opts...)
	if err != nil {
		return err
	}
	defer runner.close()

	return runner.execCommand(ctx, cfg.command, cfg.arg)
}

type runner struct {
	migrator      *migrate.Migrate
	source        source.Driver
	dataClient    *spanner.Client
	migrationsDir string
	stdout        io.Writer
	stdin         io.Reader
}

func newRunner(ctx context.Context, cfg *config, opts ...option.ClientOption) (*runner, error) {
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

	return &runner{
		migrator:      migrator,
		source:        sourceDriver,
		dataClient:    dataClient,
		migrationsDir: cfg.migrationsDir,
		stdout:        os.Stdout,
		stdin:         os.Stdin,
	}, nil
}

func (r *runner) close() {
	err, dbErr := r.migrator.Close()
	if err != nil {
		log.Printf("Failed to close source driver: %v", err)
	}
	if dbErr != nil {
		log.Printf("Failed to close database driver: %v", dbErr)
	}
}

func (r *runner) execCommand(ctx context.Context, cmd string, arg *int) error {
	log.Printf("Executing command: %q", cmd)
	switch cmd {
	case upCmd:
		return commands.Up(ctx, r.migrator, r.dataClient, arg)
	case downCmd:
		return commands.Down(r.migrator, r.source, arg, r.stdout, r.stdin)
	case forceCmd:
		if arg == nil {
			return fmt.Errorf("missing argument for %s command", forceCmd)
		}
		if err := r.migrator.Force(*arg); err != nil {
			return err
		}
		fmt.Printf("Forced version to %d\n", *arg)
	case versionCmd:
		v, dirty, err := r.migrator.Version()
		if err != nil {
			if errors.Is(err, migrate.ErrNilVersion) {
				fmt.Printf("Migrations are not yet initialized\n")
				return nil
			}
			return err
		}
		fmt.Printf("Version: %d, Dirty: %v\n", v, dirty)
	case initCmd:
		return commands.Init(r.migrator, r.source)
	default:
		return fmt.Errorf("unknown command: %q", cmd)
	}
	return nil
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
