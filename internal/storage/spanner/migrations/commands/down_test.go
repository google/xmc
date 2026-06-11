// Copyright 2025 Google LLC
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
package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"golang/cmp/cmp"
	"golang/github_com/golang_migrate/migrate/v/v4/database/database"
	"golang/github_com/golang_migrate/migrate/v/v4/database/stub/stub"
	"golang/github_com/golang_migrate/migrate/v/v4/migrate"
	"golang/github_com/golang_migrate/migrate/v/v4/source/source"
	stubs "golang/github_com/golang_migrate/migrate/v/v4/source/stub/stub"
)

// customSourceStub is only used in TestReadMigrationPreviews to verify that
// we correctly read the 'Raw' content of a migration.
type customSourceStub struct{ *stubs.Stub }

func (s *customSourceStub) ReadDown(v uint) (io.ReadCloser, string, error) {
	m, ok := s.Migrations.Down(v)
	if !ok {
		return nil, "", &os.PathError{Op: "read down", Path: s.Url, Err: os.ErrNotExist}
	}
	return io.NopCloser(strings.NewReader(m.Raw)), m.Identifier, nil
}

func setupSourceStub() *stubs.Stub {
	s := &stubs.Stub{Migrations: source.NewMigrations()}
	for i := 1; i <= 3; i++ {
		v := uint(i)
		s.Migrations.Append(&source.Migration{
			Version:    v,
			Identifier: fmt.Sprintf("%d.down.stub", v),
			Direction:  source.Down,
			Raw:        fmt.Sprintf("DROP TABLE FOO%d;", v),
		})
	}
	return s
}

func TestDown(t *testing.T) {
	tests := []struct {
		name           string
		steps          int
		initialVersion int
		userInput      string
		wantErr        bool
		wantVersion    int
		wantOutput     string
	}{
		{
			name:           "Success",
			steps:          1,
			initialVersion: 3,
			userInput:      "y",
			wantVersion:    2,
			wantOutput:     "Migration down 1 step completed successfully",
		},
		{
			name:           "DownTwoSteps",
			steps:          2,
			initialVersion: 3,
			userInput:      "yes",
			wantVersion:    1,
			wantOutput:     "Migration down 2 steps completed successfully",
		},
		{
			name:           "Aborted",
			steps:          1,
			initialVersion: 2,
			userInput:      "n",
			wantVersion:    2,
			wantOutput:     "Migration aborted by user",
		},
		{
			name:           "NoMigrations",
			steps:          1,
			initialVersion: database.NilVersion,
			userInput:      "Y",
			wantVersion:    database.NilVersion,
			wantOutput:     "No migrations applied",
		},
		{
			name:           "InvalidStep",
			steps:          -1,
			initialVersion: 1,
			wantVersion:    1,
			wantErr:        true,
		},
		{
			name:  "MissingMigration",
			steps: 1,
			// This version does not exist but it is possible to end up in this state e.g. by forcing the state.
			initialVersion: 4,
			userInput:      "y",
			wantVersion:    4,
			wantErr:        true,
			wantOutput:     "no migration found for current version 4",
		},
		{
			name:           "TooManySteps",
			steps:          5,
			initialVersion: 3,
			userInput:      "y",
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
			in := strings.NewReader(tc.userInput)
			err = Down(m, src, &tc.steps, &out, in)

			if (err != nil) != tc.wantErr {
				t.Errorf("Down() = %v, want error presence = %v", err, tc.wantErr)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), tc.wantOutput) {
				t.Errorf("Down() error = %v, want contains %q", err, tc.wantOutput)
			}

			v, _, err := m.Version()
			if err != nil && err != migrate.ErrNilVersion {
				t.Errorf("m.Version() error = %v", err)
			}
			if int(v) != tc.wantVersion {
				if !(tc.wantVersion == database.NilVersion && err == migrate.ErrNilVersion) {
					t.Errorf("Version() = %d, want %d", v, tc.wantVersion)
				}
			}

			if !tc.wantErr && !strings.Contains(out.String(), tc.wantOutput) {
				t.Errorf("Down() output = %q, want contains %q", out.String(), tc.wantOutput)
			}
		})
	}
}

func TestReadMigrationPreviews(t *testing.T) {
	src := &customSourceStub{Stub: setupSourceStub()}
	previews, err := readMigrationPreviews(src, 3, 2)
	if err != nil {
		t.Fatalf("readMigrationPreviews: %v", err)
	}

	want := []migrationPreview{
		{identifier: "3.down.stub", content: "DROP TABLE FOO3;"},
		{identifier: "2.down.stub", content: "DROP TABLE FOO2;"},
	}

	if diff := cmp.Diff(want, previews, cmp.AllowUnexported(migrationPreview{})); diff != "" {
		t.Errorf("readMigrationPreviews() mismatch (-want +got):\n%s", diff)
	}
}
