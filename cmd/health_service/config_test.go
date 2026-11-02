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

package main

import (
	"testing"

	"xmanager_cloud/internal/storage/dbconfig"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantDB  dbconfig.Config
		wantErr bool
	}{
		{
			name: "defaults to Spanner with empty database paths allowed for lazy failure",
			env:  map[string]string{},
			wantDB: dbconfig.Config{
				Backend: dbconfig.BackendSpanner,
			},
		},
		{
			name: "loads Spanner database settings from db-config env vars",
			env: map[string]string{
				"SPANNER_ESS_DATABASE":     "projects/p/instances/i/databases/ess",
				"SPANNER_XID_DATABASE":     "projects/p/instances/i/databases/xid",
				"SPANNER_QUOTA_PROJECT_ID": "quota-proj",
			},
			wantDB: dbconfig.Config{
				Backend: dbconfig.BackendSpanner,
				Spanner: dbconfig.Spanner{
					EssDatabase:    "projects/p/instances/i/databases/ess",
					XidDatabase:    "projects/p/instances/i/databases/xid",
					QuotaProjectID: "quota-proj",
				},
			},
		},
		{
			name: "loads Postgres backend with empty host allowed for lazy failure",
			env: map[string]string{
				"DB_BACKEND": "postgres",
			},
			wantDB: dbconfig.Config{
				Backend: dbconfig.BackendPostgres,
				Postgres: dbconfig.Postgres{
					Port:    5432,
					SSLMode: "verify-full",
				},
			},
		},
		{
			name: "fails fast on unsupported DB_BACKEND",
			env: map[string]string{
				"DB_BACKEND": "mysql",
			},
			wantErr: true,
		},
		{
			name: "fails fast on invalid numeric Postgres setting",
			env: map[string]string{
				"DB_BACKEND":    "postgres",
				"POSTGRES_PORT": "not-a-port",
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{
				"DB_BACKEND", "db_backend",
				"SPANNER_ESS_DATABASE", "spanner_ess_database",
				"SPANNER_XID_DATABASE", "spanner_xid_database",
				"SPANNER_QUOTA_PROJECT_ID", "spanner_quota_project_id",
				"POSTGRES_HOST", "postgres_host",
				"POSTGRES_PORT", "postgres_port",
			} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			got, err := LoadConfig()
			if tc.wantErr {
				if err == nil {
					t.Fatal("LoadConfig() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() unexpected error: %v", err)
			}
			if got.DB != tc.wantDB {
				t.Errorf("LoadConfig().DB = %+v, want %+v", got.DB, tc.wantDB)
			}
		})
	}
}

func TestGetBuildVersion(t *testing.T) {
	orig := buildVersion
	t.Cleanup(func() {
		buildVersion = orig
	})

	for _, placeholder := range []string{"", "redacted", "{STABLE_APP_VERSION}"} {
		buildVersion = placeholder
		if got := getBuildVersion(); got != "developer-build" {
			t.Errorf("getBuildVersion() with %q = %q, want %q", placeholder, got, "developer-build")
		}
	}

	buildVersion = "v1.2.3"
	if got := getBuildVersion(); got != "v1.2.3" {
		t.Errorf("getBuildVersion() = %q, want %q", got, "v1.2.3")
	}
}
