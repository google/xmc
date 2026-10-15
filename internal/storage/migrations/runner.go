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

package migrations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source"
	"xmanager_cloud/internal/logging"
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

	if command == CreateCmd {
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
	Migrator *migrate.Migrate
	Source   source.Driver
	// IsDatabaseEmpty reports whether the database contains no user-defined tables.
	IsDatabaseEmpty func(context.Context) (bool, error)
	Stdout          io.Writer
	Stdin           io.Reader
}

// Close closes the source and database drivers.
func (r *Runner) Close() {
	err, dbErr := r.Migrator.Close()
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
		return Up(ctx, r.Migrator, r.IsDatabaseEmpty, arg)
	case DownCmd:
		return Down(r.Migrator, r.Source, arg, r.Stdout, r.Stdin)
	case ForceCmd:
		if arg == nil {
			return fmt.Errorf("missing argument for %s command", ForceCmd)
		}
		if err := r.Migrator.Force(*arg); err != nil {
			return err
		}
		fmt.Printf("Forced version to %d\n", *arg)
	case VersionCmd:
		v, dirty, err := r.Migrator.Version()
		if err != nil {
			if errors.Is(err, migrate.ErrNilVersion) {
				fmt.Printf("Migrations are not yet initialized\n")
				return nil
			}
			return err
		}
		fmt.Printf("Version: %d, Dirty: %v\n", v, dirty)
	case InitCmd:
		return Init(r.Migrator, r.Source)
	default:
		return fmt.Errorf("unknown command: %q", cmd)
	}
	return nil
}
