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

// Package migrations provides database-agnostic migration runner and command primitives
// shared across storage backends (Cloud Spanner and PostgreSQL).
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
)

// Supported migration commands and schema names.
const (
	UpCmd      = "up"
	DownCmd    = "down"
	ForceCmd   = "force"
	VersionCmd = "version"
	InitCmd    = "init"
	CreateCmd  = "create"

	SchemaESS = "ess"
	SchemaXID = "xid"
)

// EmptyCheckFunc reports whether the target database contains no user-defined tables.
type EmptyCheckFunc func(ctx context.Context) (bool, error)

// Runner coordinates a golang-migrate instance and executes common migration commands.
type Runner struct {
	Migrator      *migrate.Migrate
	Source        source.Driver
	IsEmpty       EmptyCheckFunc
	MigrationsDir string
	Stdout        io.Writer
	Stdin         io.Reader
}

// NewRunner constructs a common migration Runner.
func NewRunner(
	migrator *migrate.Migrate,
	src source.Driver,
	isEmpty EmptyCheckFunc,
	migrationsDir string,
	stdout io.Writer,
	stdin io.Reader,
) *Runner {
	return &Runner{
		Migrator:      migrator,
		Source:        src,
		IsEmpty:       isEmpty,
		MigrationsDir: migrationsDir,
		Stdout:        DefaultWriter(stdout),
		Stdin:         DefaultReader(stdin),
	}
}

// DefaultWriter returns w if non-nil, or os.Stdout.
func DefaultWriter(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return os.Stdout
}

// DefaultReader returns r if non-nil, or os.Stdin.
func DefaultReader(r io.Reader) io.Reader {
	if r != nil {
		return r
	}
	return os.Stdin
}

// Close closes the underlying golang-migrate source and database drivers.
func (r *Runner) Close() {
	srcErr, dbErr := r.Migrator.Close()
	if srcErr != nil {
		logging.Error(context.Background(), "failed to close source driver", "error", srcErr)
	}
	if dbErr != nil {
		logging.Error(context.Background(), "failed to close database driver", "error", dbErr)
	}
}

// Version returns the currently applied schema version and dirty state.
func (r *Runner) Version() (uint, bool, error) {
	return r.Migrator.Version()
}

// ExecCommand executes a migration command ("up", "down", "force", "version", "init").
func (r *Runner) ExecCommand(ctx context.Context, cmd string, arg *int) error {
	logging.Info(ctx, "Executing migration command", "command", cmd, "schema", r.MigrationsDir)
	out := DefaultWriter(r.Stdout)
	in := DefaultReader(r.Stdin)
	switch strings.ToLower(strings.TrimSpace(cmd)) {
	case UpCmd:
		return Up(ctx, r.Migrator, r.IsEmpty, arg, out)
	case DownCmd:
		return Down(r.Migrator, r.Source, arg, out, in)
	case ForceCmd:
		return Force(r.Migrator, arg, out)
	case VersionCmd:
		return PrintVersion(r.Migrator, out)
	case InitCmd:
		return Init(r.Migrator, r.Source, out)
	default:
		return fmt.Errorf("unknown command: %q", cmd)
	}
}

// Force sets the schema migration version to the specified integer argument.
func Force(migrator *migrate.Migrate, arg *int, out io.Writer) error {
	out = DefaultWriter(out)
	if arg == nil {
		return fmt.Errorf("missing argument for %s command", ForceCmd)
	}
	if err := migrator.Force(*arg); err != nil {
		return err
	}
	fmt.Fprintf(out, "Forced version to %d\n", *arg)
	return nil
}

// PrintVersion writes the current migration version and dirty state to out.
func PrintVersion(migrator *migrate.Migrate, out io.Writer) error {
	out = DefaultWriter(out)
	v, dirty, err := migrator.Version()
	if err != nil {
		if errors.Is(err, migrate.ErrNilVersion) {
			fmt.Fprintln(out, "Migrations are not yet initialized")
			return nil
		}
		return err
	}
	fmt.Fprintf(out, "Version: %d, Dirty: %v\n", v, dirty)
	return nil
}

// ParseArg parses the optional positional CLI argument (step count, or migration name for create).
func ParseArg(cmd string, args []string) (*int, string, error) {
	if len(args) == 0 {
		return nil, "", nil
	}
	if len(args) > 1 {
		return nil, "", fmt.Errorf("too many arguments")
	}
	if strings.ToLower(strings.TrimSpace(cmd)) == CreateCmd {
		return nil, args[0], nil
	}
	argVal, err := strconv.Atoi(args[0])
	if err != nil {
		return nil, "", fmt.Errorf("invalid argument: %q is not an integer", args[0])
	}
	return &argVal, "", nil
}
