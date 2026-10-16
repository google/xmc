// Copyright 2026 Google LLC
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

// Package migrations provides the schema migration runner shared by the storage backends.
package migrations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/migrations/commands"
)

// Commands supported by the runner.
const (
	UpCmd      = "up"
	DownCmd    = "down"
	ForceCmd   = "force"
	VersionCmd = "version"
	InitCmd    = "init"
	CreateCmd  = "create"
)

// ParseArg parses the optional positional argument of a command. It returns the migration name
// for the create command and the integer argument for all other commands.
func ParseArg(command string, args []string) (*int, string, error) {
	if len(args) == 0 {
		return nil, "", nil
	}
	if len(args) > 1 {
		return nil, "", fmt.Errorf("too many arguments")
	}

	if strings.EqualFold(command, CreateCmd) {
		return nil, args[0], nil
	}

	argVal, err := strconv.Atoi(args[0])
	if err != nil {
		return nil, "", fmt.Errorf("invalid argument: %q is not an integer", args[0])
	}
	return &argVal, "", nil
}

// Runner executes migration commands with a golang-migrate instance.
type Runner struct {
	migrator *migrate.Migrate
	source   source.Driver
	// isDatabaseEmpty reports whether the database contains no user-defined tables.
	isDatabaseEmpty func(context.Context) (bool, error)
	// Stdout receives the command output and Stdin the confirmation that down asks for. NewRunner
	// sets them to os.Stdout and os.Stdin; tests may replace them.
	Stdout io.Writer
	Stdin  io.Reader
}

// NewRunner returns a Runner that executes commands with migrator, whose migrations come from src.
// isDatabaseEmpty reports whether the database contains no user-defined tables.
func NewRunner(migrator *migrate.Migrate, src source.Driver, isDatabaseEmpty func(context.Context) (bool, error)) *Runner {
	return &Runner{
		migrator:        migrator,
		source:          src,
		isDatabaseEmpty: isDatabaseEmpty,
		Stdout:          os.Stdout,
		Stdin:           os.Stdin,
	}
}

func (r *Runner) stdout() io.Writer {
	if r.Stdout != nil {
		return r.Stdout
	}
	return os.Stdout
}

func (r *Runner) stdin() io.Reader {
	if r.Stdin != nil {
		return r.Stdin
	}
	return os.Stdin
}

// Version returns the current migration version and whether the database is dirty.
func (r *Runner) Version() (uint, bool, error) {
	return r.migrator.Version()
}

// Close closes the source and database drivers.
func (r *Runner) Close() {
	err, dbErr := r.migrator.Close()
	if err != nil {
		logging.Error(context.Background(), "Failed to close source driver", "error", err)
	}
	if dbErr != nil {
		logging.Error(context.Background(), "Failed to close database driver", "error", dbErr)
	}
}

// ExecCommand executes the given migration command.
func (r *Runner) ExecCommand(ctx context.Context, cmd string, arg *int) error {
	logging.Info(ctx, "Executing command", "command", cmd)
	switch cmd {
	case UpCmd:
		return commands.Up(ctx, r.migrator, r.isDatabaseEmpty, arg, r.stdout())
	case DownCmd:
		return commands.Down(r.migrator, r.source, arg, r.stdout(), r.stdin())
	case ForceCmd:
		if arg == nil {
			return fmt.Errorf("missing argument for %s command", ForceCmd)
		}
		if err := r.migrator.Force(*arg); err != nil {
			return err
		}
		fmt.Fprintf(r.stdout(), "Forced version to %d\n", *arg)
	case VersionCmd:
		v, dirty, err := r.migrator.Version()
		if err != nil {
			if errors.Is(err, migrate.ErrNilVersion) {
				fmt.Fprintf(r.stdout(), "Migrations are not yet initialized\n")
				return nil
			}
			return err
		}
		fmt.Fprintf(r.stdout(), "Version: %d, Dirty: %v\n", v, dirty)
	case InitCmd:
		return commands.Init(r.migrator, r.source, r.stdout())
	default:
		return fmt.Errorf("unknown command: %q", cmd)
	}
	return nil
}
