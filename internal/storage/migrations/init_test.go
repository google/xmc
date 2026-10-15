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
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/stub"
	"github.com/golang-migrate/migrate/v4/source"
	stubs "github.com/golang-migrate/migrate/v4/source/stub"
)

func TestInit(t *testing.T) {
	tests := []struct {
		name            string
		migrations      []*source.Migration
		expectError     bool
		errorContains   string
		expectedVersion int
	}{
		{
			name:            "NoMigrations",
			migrations:      []*source.Migration{},
			expectError:     true,
			errorContains:   "failed to get latest migration version",
			expectedVersion: 0,
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
			expectError:     false,
			expectedVersion: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &stubs.Stub{
				Migrations: source.NewMigrations(),
			}

			// Add migrations
			for _, m := range tc.migrations {
				s.Migrations.Append(m)
			}

			dbStub := &stub.Stub{}

			m, err := migrate.NewWithInstance("stub", s, "stub", dbStub)
			if err != nil {
				t.Fatalf("Failed to create migrate instance: %v", err)
			}
			defer m.Close()

			err = Init(m, s)

			if tc.expectError {
				if err == nil {
					t.Fatalf("Init() = nil, want error containing %q", tc.errorContains)
				}
				if !strings.Contains(err.Error(), tc.errorContains) {
					t.Errorf("Init() = %v, want error containing %q", err, tc.errorContains)
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
