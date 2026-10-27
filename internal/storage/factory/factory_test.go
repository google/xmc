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

package factory

import (
	"context"
	"path/filepath"
	"testing"

	"xmanager_cloud/internal/storage/dbconfig"
	pgversion "xmanager_cloud/internal/storage/postgres/migrations/version"
	spannerversion "xmanager_cloud/internal/storage/spanner/migrations/version"
)

func TestOpen_InvalidRequests(t *testing.T) {
	ctx := context.Background()
	essOnlySpanner := dbconfig.Config{
		Backend: dbconfig.BackendSpanner,
		Spanner: dbconfig.Spanner{EssDatabase: "projects/p/instances/i/databases/ess"},
	}
	unreadablePasswordPostgres := dbconfig.Config{
		Backend: dbconfig.BackendPostgres,
		Postgres: dbconfig.Postgres{
			Host:         "127.0.0.1",
			Port:         5432,
			User:         "postgres",
			PasswordFile: filepath.Join(t.TempDir(), "missing"),
			EssDatabase:  "ess",
			XidDatabase:  "xid",
			SSLMode:      "disable",
		},
	}

	tests := []struct {
		name string
		cfg  dbconfig.Config
		db   Database
	}{
		{name: "unknown database", cfg: essOnlySpanner, db: "metrics"},
		{name: "unsupported backend", cfg: dbconfig.Config{Backend: "mysql"}, db: ESS},
		{name: "unset backend", cfg: dbconfig.Config{}, db: ESS},
		{name: "spanner database not configured", cfg: essOnlySpanner, db: XID},
		{name: "postgres password file unreadable", cfg: unreadablePasswordPostgres, db: ESS},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, err := NewStorage(ctx, tc.cfg, tc.db)
			if err == nil {
				st.Close()
				t.Fatalf("NewStorage(%q) succeeded, want error", tc.db)
			}
		})
	}
}

func TestSpannerDatabase(t *testing.T) {
	cfg := dbconfig.Spanner{EssDatabase: "ess-path", XidDatabase: "xid-path"}
	tests := []struct {
		db          Database
		wantPath    string
		wantVersion int64
	}{
		{db: ESS, wantPath: "ess-path", wantVersion: spannerversion.EssExpectedSchemaVersion()},
		{db: XID, wantPath: "xid-path", wantVersion: spannerversion.XidExpectedSchemaVersion()},
	}

	for _, tc := range tests {
		t.Run(string(tc.db), func(t *testing.T) {
			gotPath, gotVersion := spannerDatabase(cfg, tc.db)
			if gotPath != tc.wantPath || gotVersion != tc.wantVersion {
				t.Errorf("spannerDatabase(%q) = (%q, %d), want (%q, %d)", tc.db, gotPath, gotVersion, tc.wantPath, tc.wantVersion)
			}
		})
	}
}

func TestPostgresDatabase(t *testing.T) {
	cfg := dbconfig.Postgres{Host: "db", Port: 5432, User: "xmc", EssDatabase: "ess_db", XidDatabase: "xid_db", SSLMode: "disable"}
	tests := []struct {
		db          Database
		wantDSN     string
		wantVersion int64
	}{
		{db: ESS, wantDSN: mustDSN(t, cfg.EssDSN), wantVersion: pgversion.EssExpectedSchemaVersion()},
		{db: XID, wantDSN: mustDSN(t, cfg.XidDSN), wantVersion: pgversion.XidExpectedSchemaVersion()},
	}

	for _, tc := range tests {
		t.Run(string(tc.db), func(t *testing.T) {
			gotDSN, gotVersion, err := postgresDatabase(cfg, tc.db)
			if err != nil {
				t.Fatalf("postgresDatabase(%q) failed: %v", tc.db, err)
			}
			if gotDSN != tc.wantDSN || gotVersion != tc.wantVersion {
				t.Errorf("postgresDatabase(%q) = (%q, %d), want (%q, %d)", tc.db, gotDSN, gotVersion, tc.wantDSN, tc.wantVersion)
			}
		})
	}
}

// mustDSN returns the connection URL that dsn builds, failing the test if it cannot.
func mustDSN(t *testing.T, dsn func() (string, error)) string {
	t.Helper()
	got, err := dsn()
	if err != nil {
		t.Fatalf("failed to build the connection URL: %v", err)
	}
	return got
}

// fakeSchemaVersionClient reports a fixed schema version.
type fakeSchemaVersionClient struct {
	version int64
}

func (f fakeSchemaVersionClient) QuerySchemaVersion(context.Context) (int64, bool, error) {
	return f.version, false, nil
}

func TestStorage_CheckReady(t *testing.T) {
	tests := []struct {
		name    string
		version int64
		wantErr bool
	}{
		{name: "expected version", version: 3},
		{name: "older version", version: 2, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &Storage{schemaVersion: fakeSchemaVersionClient{version: tc.version}, expectedVersion: 3}
			if err := st.CheckReady(context.Background()); (err != nil) != tc.wantErr {
				t.Errorf("CheckReady() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestStorage_Close(t *testing.T) {
	calls := 0
	st := &Storage{closeFn: func() { calls++ }}
	st.Close()
	st.Close()
	if calls != 1 {
		t.Errorf("Close() twice released the connections %d times, want 1", calls)
	}

	// A Storage that was not opened through NewStorage, such as a test double, has nothing to release.
	(&Storage{}).Close()
}
