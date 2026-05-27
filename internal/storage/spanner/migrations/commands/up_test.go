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
package commands

import (
	"testing"

	dbadminpb "spanner/admin/database/v1/spanner_database_admin_go_proto"
	"cloud_spanner_emulator/public/go/emulator"
	"golang/cloud_google_com/go/spanner/v/v1/admin/database/apiv1/database"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	migdatabase "golang/github_com/golang_migrate/migrate/v/v4/database/database"
	"golang/github_com/golang_migrate/migrate/v/v4/database/stub/stub"
	"golang/github_com/golang_migrate/migrate/v/v4/migrate"
	"golang/github_com/golang_migrate/migrate/v/v4/source/source"
	stubs "golang/github_com/golang_migrate/migrate/v/v4/source/stub/stub"
	xmcemulator "xmanager_cloud/storage/spanner/emulator/emulator"
)

func TestUp(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name            string
		steps           *int
		initialVersion  int
		expectError     bool
		expectedVersion int
		emptyDatabase   bool
	}{
		{
			name:            "SchemaNotInitialized_EmptyDB",
			initialVersion:  migdatabase.NilVersion,
			expectError:     false,
			expectedVersion: 3,
			emptyDatabase:   true,
		},
		{
			name:            "SchemaNotInitialized_NonEmptyDB",
			initialVersion:  migdatabase.NilVersion,
			expectError:     true,
			expectedVersion: 0,
			emptyDatabase:   false,
		},
		{
			name:            "UpStep",
			steps:           ptr(1),
			initialVersion:  1,
			expectError:     false,
			expectedVersion: 2,
			emptyDatabase:   true,
		},
		{
			name:            "UpNegativeStep",
			steps:           ptr(-1),
			initialVersion:  1,
			expectError:     true,
			expectedVersion: 1,
			emptyDatabase:   true,
		},
		{
			name:            "UpAllMigrations",
			initialVersion:  1,
			expectError:     false,
			expectedVersion: 3,
			emptyDatabase:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setup stub source
			s := &stubs.Stub{
				Migrations: source.NewMigrations(),
			}
			s.Migrations.Append(&source.Migration{Version: 1, Identifier: "1", Direction: source.Up})
			s.Migrations.Append(&source.Migration{Version: 1, Identifier: "1", Direction: source.Down})
			s.Migrations.Append(&source.Migration{Version: 2, Identifier: "2", Direction: source.Up})
			s.Migrations.Append(&source.Migration{Version: 2, Identifier: "2", Direction: source.Down})
			s.Migrations.Append(&source.Migration{Version: 3, Identifier: "3", Direction: source.Up})
			s.Migrations.Append(&source.Migration{Version: 3, Identifier: "3", Direction: source.Down})
			dbStub := &stub.Stub{}

			// Initialize migrate instance
			m, err := migrate.NewWithInstance("stub", s, "stub", dbStub)
			if err != nil {
				t.Fatalf("Failed to create migrate instance: %v", err)
			}

			dbStub.CurrentVersion = tc.initialVersion

			_, dataClient, emu := xmcemulator.SetupEmpty(ctx, t)
			if !tc.emptyDatabase {
				updateDDL(t, dataClient, emu, []string{"CREATE TABLE NonEmptyTable (ID INT64) PRIMARY KEY (ID)"})
			}

			err = Up(ctx, m, dataClient, tc.steps)

			if tc.expectError && err == nil {
				t.Errorf("Up() expected error, got nil")
			}

			if !tc.expectError && err != nil {
				t.Errorf("Up() unexpected error: %v", err)
			}

			v, _, err := m.Version()
			if err != nil {
				// ErrNilVersion is expected for SchemaNotInitialized cases where migration hasn't run or failed
				if err != migrate.ErrNilVersion || tc.expectedVersion != 0 {
					t.Errorf("Version() unexpected error: %v", err)
				}
			} else {
				if int(v) != tc.expectedVersion {
					t.Errorf("Version() expected %d, got %d", tc.expectedVersion, v)
				}
			}
		})
	}
}

func updateDDL(t *testing.T, client *spanner.Client, emu *emulator.Emulator, statements []string) {
	t.Helper()
	ctx := t.Context()

	adminClient, err := database.NewDatabaseAdminClient(ctx, emu.ClientOptions()...)
	if err != nil {
		t.Fatalf("Failed to create database admin client: %v", err)
	}
	op, err := adminClient.UpdateDatabaseDdl(ctx, &dbadminpb.UpdateDatabaseDdlRequest{
		Database:   client.DatabaseName(),
		Statements: statements,
	})
	if err != nil {
		t.Fatalf("Failed to update database DDL: %v", err)
	}
	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Failed to wait for DDL operation: %v", err)
	}
}

func ptr(i int) *int {
	return &i
}
