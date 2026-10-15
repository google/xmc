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
package migrations

import (
	"os"
	"path/filepath"
	"testing"
)

func createTempFiles(t *testing.T, dir string, files []string) {
	t.Helper()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0644); err != nil {
			t.Fatalf("Failed to create temp file %q: %v", f, err)
		}
	}
}

func TestCreate(t *testing.T) {
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

			if !tc.dirNotExist {
				if len(tc.existingFiles) > 0 {
					createTempFiles(t, migDir, tc.existingFiles)
				}
			} else {
				// To simulate not existing, remove the one created by t.TempDir()
				if err := os.RemoveAll(migDir); err != nil {
					t.Fatalf("Failed to remove temp dir: %v", err)
				}
			}

			err := Create("", migDir, tc.migrationName)

			if (err != nil) != tc.wantErr {
				t.Fatalf("Create(%v, %v) = %v, want error presence %v", migDir, tc.migrationName, err, tc.wantErr)
			}

			if !tc.wantErr {
				for _, wf := range tc.wantFiles {
					if _, err := os.Stat(filepath.Join(migDir, wf)); os.IsNotExist(err) {
						t.Errorf("Create(%q, %q) did not create file %q", migDir, tc.migrationName, wf)
					}
				}
			}
		})
	}
}
