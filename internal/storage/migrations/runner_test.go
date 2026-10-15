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

package migrations

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/database/stub"
)

func TestRunnerExecCommand(t *testing.T) {
	ctx := t.Context()
	src := setupSourceStub()
	dbStub := &stub.Stub{CurrentVersion: database.NilVersion}
	m, err := migrate.NewWithInstance("stub", src, "stub", dbStub)
	if err != nil {
		t.Fatalf("migrate.NewWithInstance: %v", err)
	}

	var out bytes.Buffer
	r := NewRunner(m, src, func(context.Context) (bool, error) { return true, nil }, SchemaESS, &out, strings.NewReader("y\n"))
	defer r.Close()

	if err := r.ExecCommand(ctx, VersionCmd, nil); err != nil {
		t.Fatalf("ExecCommand(version) on uninitialized DB failed: %v", err)
	}
	if !strings.Contains(out.String(), "Migrations are not yet initialized") {
		t.Errorf("ExecCommand(version) output = %q, want uninitialized message", out.String())
	}

	if err := r.ExecCommand(ctx, UpCmd, nil); err != nil {
		t.Fatalf("ExecCommand(up) failed: %v", err)
	}
	v, dirty, err := r.Version()
	if err != nil || v != 3 || dirty {
		t.Fatalf("Version() after up = (%d, %v, %v), want (3, false, nil)", v, dirty, err)
	}

	if err := r.ExecCommand(ctx, DownCmd, intPtr(1)); err != nil {
		t.Fatalf("ExecCommand(down, 1) failed: %v", err)
	}
	v, _, err = r.Version()
	if err != nil || v != 2 {
		t.Fatalf("Version() after down = (%d, %v), want (2, nil)", v, err)
	}

	if err := r.ExecCommand(ctx, ForceCmd, intPtr(1)); err != nil {
		t.Fatalf("ExecCommand(force, 1) failed: %v", err)
	}
	v, _, err = r.Version()
	if err != nil || v != 1 {
		t.Fatalf("Version() after force = (%d, %v), want (1, nil)", v, err)
	}

	if err := r.ExecCommand(ctx, ForceCmd, nil); err == nil {
		t.Errorf("ExecCommand(force, nil) succeeded, want error")
	}

	if err := r.ExecCommand(ctx, InitCmd, nil); err != nil {
		t.Fatalf("ExecCommand(init) failed: %v", err)
	}
	v, _, err = r.Version()
	if err != nil || v != 3 {
		t.Fatalf("Version() after init = (%d, %v), want (3, nil)", v, err)
	}

	out.Reset()
	if err := r.ExecCommand(ctx, VersionCmd, nil); err != nil {
		t.Fatalf("ExecCommand(version) after init failed: %v", err)
	}
	if !strings.Contains(out.String(), "Version: 3, Dirty: false") {
		t.Errorf("ExecCommand(version) output = %q, want 'Version: 3, Dirty: false'", out.String())
	}

	if err := r.ExecCommand(ctx, "unknown", nil); err == nil {
		t.Errorf("ExecCommand(unknown) succeeded, want error")
	}
}

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
			name: "NoArg",
			args: []string{},
		},
		{
			name:    "NumberArg",
			args:    []string{"123"},
			wantArg: intPtr(123),
		},
		{
			name:    "InvalidArg",
			args:    []string{"abc"},
			wantErr: true,
		},
		{
			name:    "TooManyArgs",
			args:    []string{"123", "456"},
			wantErr: true,
		},
		{
			name:              "Create",
			cmd:               CreateCmd,
			args:              []string{"new_migration"},
			wantMigrationName: "new_migration",
		},
		{
			name:              "Create_NoArg",
			cmd:               CreateCmd,
			args:              []string{},
			wantMigrationName: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotArg, gotName, err := ParseArg(tc.cmd, tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseArg(%q, %v) err = %v, wantErr %v", tc.cmd, tc.args, err, tc.wantErr)
			}
			if (gotArg == nil) != (tc.wantArg == nil) {
				t.Errorf("ParseArg(%v) arg nil mismatch: got %v, want %v", tc.args, gotArg, tc.wantArg)
			} else if gotArg != nil && *gotArg != *tc.wantArg {
				t.Errorf("ParseArg(%v) arg = %d, want %d", tc.args, *gotArg, *tc.wantArg)
			}
			if gotName != tc.wantMigrationName {
				t.Errorf("ParseArg(%v) name = %q, want %q", tc.args, gotName, tc.wantMigrationName)
			}
		})
	}
}
