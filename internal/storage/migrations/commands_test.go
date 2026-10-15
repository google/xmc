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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/database/stub"
	"github.com/golang-migrate/migrate/v4/source"
	stubs "github.com/golang-migrate/migrate/v4/source/stub"
	"github.com/google/go-cmp/cmp"
)

type customSourceStub struct{ *stubs.Stub }

func (s *customSourceStub) ReadDown(v uint) (io.ReadCloser, string, error) {
	m, ok := s.Migrations.Down(v)
	if !ok {
		return nil, "", &os.PathError{Op: "read down", Path: s.Url, Err: os.ErrNotExist}
	}
	return io.NopCloser(strings.NewReader(m.Raw)), m.Identifier, nil
}

const testLicenseHeader = `-- Copyright 2026 Google LLC
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--      http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

`

func setupSourceStub() *stubs.Stub {
	s := &stubs.Stub{Migrations: source.NewMigrations()}
	for i := 1; i <= 3; i++ {
		v := uint(i)
		s.Migrations.Append(&source.Migration{
			Version:    v,
			Identifier: fmt.Sprintf("%d.up.stub", v),
			Direction:  source.Up,
			Raw:        fmt.Sprintf("%sCREATE TABLE foo%d (id INT);", testLicenseHeader, v),
		})
		s.Migrations.Append(&source.Migration{
			Version:    v,
			Identifier: fmt.Sprintf("%d.down.stub", v),
			Direction:  source.Down,
			Raw:        fmt.Sprintf("%sDROP TABLE foo%d;", testLicenseHeader, v),
		})
	}
	return s
}

func intPtr(v int) *int {
	return &v
}

func TestUp(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name            string
		steps           *int
		initialVersion  int
		emptyDatabase   bool
		expectError     bool
		expectedVersion int
		wantOutput      string
	}{
		{
			name:            "SchemaNotInitialized_EmptyDB",
			initialVersion:  database.NilVersion,
			emptyDatabase:   true,
			expectedVersion: 3,
			wantOutput:      "Database is empty, proceeding with initial migration up.",
		},
		{
			name:            "SchemaNotInitialized_NonEmptyDB",
			initialVersion:  database.NilVersion,
			emptyDatabase:   false,
			expectError:     true,
			expectedVersion: 0,
		},
		{
			name:            "UpStep",
			steps:           intPtr(1),
			initialVersion:  1,
			emptyDatabase:   true,
			expectedVersion: 2,
			wantOutput:      "Migration up 1 steps completed successfully.",
		},
		{
			name:            "UpNegativeStep",
			steps:           intPtr(-1),
			initialVersion:  1,
			emptyDatabase:   true,
			expectError:     true,
			expectedVersion: 1,
		},
		{
			name:            "UpZeroStep",
			steps:           intPtr(0),
			initialVersion:  1,
			emptyDatabase:   true,
			expectError:     true,
			expectedVersion: 1,
		},
		{
			name:            "UpAllMigrations",
			initialVersion:  1,
			emptyDatabase:   true,
			expectedVersion: 3,
			wantOutput:      "Migration up completed successfully.",
		},
		{
			name:            "UpAlreadyUpToDate",
			initialVersion:  3,
			emptyDatabase:   true,
			expectedVersion: 3,
			wantOutput:      "No migrations to apply. Schema is already up to date.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := setupSourceStub()
			dbStub := &stub.Stub{CurrentVersion: tc.initialVersion}
			m, err := migrate.NewWithInstance("stub", src, "stub", dbStub)
			if err != nil {
				t.Fatalf("migrate.NewWithInstance: %v", err)
			}

			var out bytes.Buffer
			isEmpty := func(context.Context) (bool, error) {
				return tc.emptyDatabase, nil
			}
			err = Up(ctx, m, isEmpty, tc.steps, &out)
			if (err != nil) != tc.expectError {
				t.Fatalf("Up() error = %v, wantErr %v", err, tc.expectError)
			}
			if !tc.expectError && tc.wantOutput != "" && !strings.Contains(out.String(), tc.wantOutput) {
				t.Errorf("Up() output = %q, want substring %q", out.String(), tc.wantOutput)
			}
			verifyStubVersion(t, m, tc.expectedVersion)
		})
	}
}

func verifyStubVersion(t *testing.T, m *migrate.Migrate, expectedVersion int) {
	t.Helper()
	v, _, err := m.Version()
	if err != nil {
		if err != migrate.ErrNilVersion || expectedVersion != 0 {
			t.Errorf("Version() unexpected error: %v", err)
		}
		return
	}
	if int(v) != expectedVersion {
		t.Errorf("Version() = %d, want %d", v, expectedVersion)
	}
}

func TestDown(t *testing.T) {
	tests := []struct {
		name           string
		steps          *int
		initialVersion int
		userInput      string
		wantErr        bool
		wantVersion    int
		wantOutput     string
	}{
		{
			name:           "Success",
			steps:          intPtr(1),
			initialVersion: 3,
			userInput:      "y\n",
			wantVersion:    2,
			wantOutput:     "Migration down 1 step completed successfully",
		},
		{
			name:           "DefaultStepsNil",
			steps:          nil,
			initialVersion: 3,
			userInput:      "y\n",
			wantVersion:    2,
			wantOutput:     "Migration down 1 step completed successfully",
		},
		{
			name:           "DownTwoSteps",
			steps:          intPtr(2),
			initialVersion: 3,
			userInput:      "yes\n",
			wantVersion:    1,
			wantOutput:     "Migration down 2 steps completed successfully",
		},
		{
			name:           "Aborted",
			steps:          intPtr(1),
			initialVersion: 2,
			userInput:      "n\n",
			wantVersion:    2,
			wantOutput:     "Migration aborted by user",
		},
		{
			name:           "EmptyInputEOF",
			steps:          intPtr(1),
			initialVersion: 2,
			userInput:      "",
			wantVersion:    2,
			wantOutput:     "Migration aborted by user",
		},
		{
			name:           "NoMigrations",
			steps:          intPtr(1),
			initialVersion: database.NilVersion,
			userInput:      "Y\n",
			wantVersion:    database.NilVersion,
			wantOutput:     "No migrations applied",
		},
		{
			name:           "InvalidStep",
			steps:          intPtr(-1),
			initialVersion: 1,
			wantVersion:    1,
			wantErr:        true,
		},
		{
			name:           "MissingMigration",
			steps:          intPtr(1),
			initialVersion: 4,
			userInput:      "y\n",
			wantVersion:    4,
			wantErr:        true,
			wantOutput:     "no migration found for current version 4",
		},
		{
			name:           "TooManySteps",
			steps:          intPtr(5),
			initialVersion: 3,
			userInput:      "y\n",
			wantVersion:    3,
			wantErr:        true,
			wantOutput:     "requested to revert 5 steps, but only 3 migrations are available to revert",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := setupSourceStub()
			db := &stub.Stub{CurrentVersion: tc.initialVersion}
			m, err := migrate.NewWithInstance("stub", src, "stub", db)
			if err != nil {
				t.Fatalf("migrate.NewWithInstance: %v", err)
			}

			var out bytes.Buffer
			err = Down(m, src, tc.steps, &out, strings.NewReader(tc.userInput))
			if (err != nil) != tc.wantErr {
				t.Fatalf("Down() = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && tc.wantOutput != "" && !strings.Contains(err.Error(), tc.wantOutput) {
				t.Errorf("Down() error = %v, want substring %q", err, tc.wantOutput)
			}
			verifyDownVersionAndOutput(t, m, tc.wantVersion, tc.wantErr, out.String(), tc.wantOutput)
		})
	}
}

func verifyDownVersionAndOutput(t *testing.T, m *migrate.Migrate, wantVersion int, wantErr bool, gotOut, wantOut string) {
	t.Helper()
	v, _, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		t.Errorf("m.Version() error = %v", err)
	}
	if int(v) != wantVersion && !(wantVersion == database.NilVersion && err == migrate.ErrNilVersion) {
		t.Errorf("Version() = %d, want %d", v, wantVersion)
	}
	if wantErr {
		return
	}
	if !strings.Contains(gotOut, wantOut) {
		t.Errorf("Down() output = %q, want substring %q", gotOut, wantOut)
	}
	if strings.Contains(gotOut, "Copyright") || strings.Contains(gotOut, "Licensed under") {
		t.Errorf("Down() preview output contains license header: %q", gotOut)
	}
}

func TestReadMigrationPreviews(t *testing.T) {
	src := &customSourceStub{Stub: setupSourceStub()}
	previews, err := ReadMigrationPreviews(src, 3, 2)
	if err != nil {
		t.Fatalf("ReadMigrationPreviews: %v", err)
	}

	want := []MigrationPreview{
		{Identifier: "3.down.stub", Content: "DROP TABLE foo3;"},
		{Identifier: "2.down.stub", Content: "DROP TABLE foo2;"},
	}

	if diff := cmp.Diff(want, previews); diff != "" {
		t.Errorf("ReadMigrationPreviews() mismatch (-want +got):\n%s", diff)
	}
}

func TestStripLeadingLicense(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "StandardLicenseHeader",
			input: testLicenseHeader + "DROP TABLE users;\nDROP TABLE orders;",
			want:  "DROP TABLE users;\nDROP TABLE orders;",
		},
		{
			name:  "NoComments",
			input: "DROP TABLE customers;",
			want:  "DROP TABLE customers;",
		},
		{
			name:  "LeadingEmptyLinesBeforeComments",
			input: "\n\n-- Copyright 2026 Google LLC\n-- Licensed under Apache\n\nDROP TABLE products;",
			want:  "DROP TABLE products;",
		},
		{
			name:  "PreservesInternalComments",
			input: "-- Copyright 2026 Google LLC\n\nDROP TABLE table_a;\n\n-- Internal comment\nDROP TABLE table_b;",
			want:  "DROP TABLE table_a;\n\n-- Internal comment\nDROP TABLE table_b;",
		},
		{
			name:  "EmptyContent",
			input: "",
			want:  "",
		},
		{
			name:  "WhitespaceOnly",
			input: "   \n\t\n  ",
			want:  "",
		},
		{
			name:  "OnlyComments",
			input: "-- Copyright 2026 Google LLC\n-- Licensed under Apache",
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := StripLeadingLicense(tc.input)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("StripLeadingLicense() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestInit(t *testing.T) {
	tests := []struct {
		name            string
		migrations      []*source.Migration
		expectError     bool
		errorContains   string
		expectedVersion int
	}{
		{
			name:          "NoMigrations",
			migrations:    []*source.Migration{},
			expectError:   true,
			errorContains: "failed to get latest migration version",
		},
		{
			name: "WithMigrations",
			migrations: []*source.Migration{
				{Version: 1, Identifier: "1", Direction: source.Up},
				{Version: 1, Identifier: "1", Direction: source.Down},
				{Version: 2, Identifier: "2", Direction: source.Up},
				{Version: 2, Identifier: "2", Direction: source.Down},
				{Version: 3, Identifier: "3", Direction: source.Up},
				{Version: 3, Identifier: "3", Direction: source.Down},
			},
			expectedVersion: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &stubs.Stub{Migrations: source.NewMigrations()}
			for _, m := range tc.migrations {
				s.Migrations.Append(m)
			}
			dbStub := &stub.Stub{}
			m, err := migrate.NewWithInstance("stub", s, "stub", dbStub)
			if err != nil {
				t.Fatalf("migrate.NewWithInstance: %v", err)
			}
			defer m.Close()

			var out bytes.Buffer
			err = Init(m, s, &out)
			if tc.expectError {
				if err == nil || !strings.Contains(err.Error(), tc.errorContains) {
					t.Fatalf("Init() = %v, want error containing %q", err, tc.errorContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("Init() = %v, want nil", err)
			}
			v, _, err := m.Version()
			if err != nil {
				t.Fatalf("Version() = %v, want nil", err)
			}
			if int(v) != tc.expectedVersion {
				t.Errorf("Version() = %d, want %d", v, tc.expectedVersion)
			}
		})
	}
}

func createTempFiles(t *testing.T, dir string, files []string) {
	t.Helper()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(""), defaultFileMode); err != nil {
			t.Fatalf("failed to create temp file %q: %v", f, err)
		}
	}
}

func TestCreateInDir(t *testing.T) {
	tests := []struct {
		name          string
		migrationName string
		existingFiles []string
		wantErr       bool
		wantFiles     []string
		dirNotExist   bool
	}{
		{
			name:          "sequential_files",
			migrationName: "third_migration",
			existingFiles: []string{"001_init.up.sql", "001_init.down.sql", "002_second.up.sql", "002_second.down.sql"},
			wantFiles:     []string{"003_third_migration.up.sql", "003_third_migration.down.sql"},
		},
		{
			name:          "gap_in_files",
			migrationName: "another",
			existingFiles: []string{"001_init.up.sql", "003_third.up.sql"},
			wantFiles:     []string{"004_another.up.sql", "004_another.down.sql"},
		},
		{
			name:          "ignore_other_files",
			migrationName: "new_feature",
			existingFiles: []string{"001_init.up.sql", "README.md", "002_second.up.sql"},
			wantFiles:     []string{"003_new_feature.up.sql", "003_new_feature.down.sql"},
		},
		{
			name:          "empty_name",
			migrationName: "",
			wantErr:       true,
		},
		{
			name:          "empty_dir",
			migrationName: "new_migration",
			wantErr:       true,
		},
		{
			name:          "dir_not_exist",
			migrationName: "test_migration",
			wantErr:       true,
			dirNotExist:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			migDir := t.TempDir()
			if tc.dirNotExist {
				if err := os.RemoveAll(migDir); err != nil {
					t.Fatalf("failed to remove temp dir: %v", err)
				}
			} else if len(tc.existingFiles) > 0 {
				createTempFiles(t, migDir, tc.existingFiles)
			}

			var out bytes.Buffer
			err := CreateInDir("internal/storage/postgres/migrations", migDir, tc.migrationName, &out)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CreateInDir(%q, %q) = %v, wantErr %v", migDir, tc.migrationName, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			for _, wf := range tc.wantFiles {
				if _, err := os.Stat(filepath.Join(migDir, wf)); os.IsNotExist(err) {
					t.Errorf("CreateInDir(%q, %q) did not create file %q", migDir, tc.migrationName, wf)
				}
			}
		})
	}
}

func TestResolveMigrationsPath(t *testing.T) {
	t.Setenv("TEST_TMPDIR", "")
	t.Setenv("BUILD_WORKSPACE_DIRECTORY", "")
	if _, err := ResolveMigrationsPath("internal/storage/postgres/migrations", "ess"); err == nil {
		t.Errorf("ResolveMigrationsPath without workspace or TEST_TMPDIR succeeded, want error")
	}

	t.Setenv("BUILD_WORKSPACE_DIRECTORY", "/workspace/root")
	got, err := ResolveMigrationsPath("internal/storage/postgres/migrations", "ess")
	if err != nil {
		t.Fatalf("ResolveMigrationsPath with workspace failed: %v", err)
	}
	want := "/workspace/root/internal/storage/postgres/migrations/ess"
	if got != want {
		t.Errorf("ResolveMigrationsPath() = %q, want %q", got, want)
	}
}
