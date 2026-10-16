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
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"testing/fstest"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/stub"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"k8s.io/utils/ptr"
)

func TestParseArg(t *testing.T) {
	tests := []struct {
		name              string
		cmd               string
		args              []string
		wantArg           *int
		wantMigrationName string
		wantErr           bool
	}{
		{
			name:    "NoArg",
			args:    []string{},
			wantArg: nil,
			wantErr: false,
		},
		{
			name:    "NumberArg",
			args:    []string{"123"},
			wantArg: ptr.To(123),
			wantErr: false,
		},
		{
			name:    "InvalidArg",
			args:    []string{"abc"},
			wantArg: nil,
			wantErr: true,
		},
		{
			name:    "TooManyArgs",
			args:    []string{"123", "456"},
			wantArg: nil,
			wantErr: true,
		},
		{
			name:              "Create",
			cmd:               "create",
			args:              []string{"new_migration"},
			wantMigrationName: "new_migration",
			wantErr:           false,
		},
		{
			name:              "Create_Uppercase",
			cmd:               "CREATE",
			args:              []string{"new_migration"},
			wantMigrationName: "new_migration",
			wantErr:           false,
		},
		{
			name:              "Create_TooManyArgs",
			cmd:               "create",
			args:              []string{"new_migration", "extra"},
			wantMigrationName: "",
			wantErr:           true,
		},
		{
			name:              "Create_NoArg",
			cmd:               "create",
			args:              []string{},
			wantMigrationName: "",
			wantErr:           false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			arg, migrationName, err := ParseArg(tc.cmd, tc.args)

			if (err != nil) != tc.wantErr {
				t.Errorf("ParseArg(%v) = %v, want error presence %v", tc.args, err, tc.wantErr)
			}

			if (arg == nil) != (tc.wantArg == nil) {
				t.Errorf("ParseArg(%v).arg nil mismatch: got %v, want %v", tc.args, arg, tc.wantArg)
			} else if arg != nil && *arg != *tc.wantArg {
				t.Errorf("ParseArg(%v).arg = %d, want %d", tc.args, *arg, *tc.wantArg)
			}

			if migrationName != tc.wantMigrationName {
				t.Errorf("ParseArg(%v).migrationName = %q, want %q", tc.args, migrationName, tc.wantMigrationName)
			}
		})
	}
}

// newStubRunner returns a Runner over golang-migrate's in-memory stub database and one migration.
func newStubRunner(t *testing.T) *Runner {
	t.Helper()
	src, err := iofs.New(fstest.MapFS{
		"1_baseline.up.sql":   {Data: []byte("SELECT 1;")},
		"1_baseline.down.sql": {Data: []byte("SELECT 1;")},
	}, ".")
	if err != nil {
		t.Fatalf("iofs.New() failed: %v", err)
	}
	db, err := stub.WithInstance(nil, &stub.Config{})
	if err != nil {
		t.Fatalf("stub.WithInstance() failed: %v", err)
	}
	migrator, err := migrate.NewWithInstance("iofs", src, "stub", db)
	if err != nil {
		t.Fatalf("migrate.NewWithInstance() failed: %v", err)
	}
	r := NewRunner(migrator, src, func(context.Context) (bool, error) { return true, nil })
	t.Cleanup(r.Close)
	return r
}

func TestExecCommand_WritesOutputToStdout(t *testing.T) {
	r := newStubRunner(t)
	var stdout bytes.Buffer
	r.Stdout = &stdout

	// The steps share the runner and run in order: init sets the version, version reports it,
	// and force overrides it.
	steps := []struct {
		name string
		cmd  string
		arg  *int
		want string
	}{
		{name: "VersionNotInitialized", cmd: VersionCmd, want: "Migrations are not yet initialized\n"},
		{name: "Init", cmd: InitCmd, want: "Schema version initialized to: 1\n"},
		{name: "Version", cmd: VersionCmd, want: "Version: 1, Dirty: false\n"},
		{name: "Force", cmd: ForceCmd, arg: ptr.To(1), want: "Forced version to 1\n"},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			stdout.Reset()
			if err := r.ExecCommand(t.Context(), s.cmd, s.arg); err != nil {
				t.Fatalf("ExecCommand(%q) failed: %v", s.cmd, err)
			}
			if got := stdout.String(); got != s.want {
				t.Errorf("ExecCommand(%q) wrote %q to Stdout, want %q", s.cmd, got, s.want)
			}
		})
	}
}

func TestRunner_Version(t *testing.T) {
	r := newStubRunner(t)
	r.Stdout = io.Discard

	if _, _, err := r.Version(); !errors.Is(err, migrate.ErrNilVersion) {
		t.Fatalf("Version() before force error = %v, want %v", err, migrate.ErrNilVersion)
	}
	if err := r.ExecCommand(t.Context(), ForceCmd, ptr.To(1)); err != nil {
		t.Fatalf("ExecCommand(force, 1) failed: %v", err)
	}
	v, dirty, err := r.Version()
	if err != nil || v != 1 || dirty {
		t.Errorf("Version() = (%d, %v, %v), want (1, false, nil)", v, dirty, err)
	}
}

func TestExecCommand_ForceMissingArg(t *testing.T) {
	r := newStubRunner(t)
	r.Stdout = io.Discard

	if err := r.ExecCommand(t.Context(), ForceCmd, nil); err == nil {
		t.Errorf("ExecCommand(force, nil) expected error, got nil")
	}
}
