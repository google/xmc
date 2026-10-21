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

package postgrestest

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"xmanager_cloud/internal/storage/postgres/migrations/version"
)

// migrationRow is a row of golang-migrate's schema_migrations table.
type migrationRow struct {
	Version int64
	Dirty   bool
}

func TestSetup(t *testing.T) {
	ctx := t.Context()
	xidPool, essPool := Setup(ctx, t)
	// Representative tables of both schemas plus golang-migrate's version table, so that each
	// database is checked both for its own tables and for the absence of the other schema's.
	probed := []string{"Xids", "Experiments", "WorkUnits", "Artifacts", "PauseSchedulingState", "ComponentOwnershipFence", "schema_migrations"}
	wantESSTables := []string{"Experiments", "WorkUnits", "Artifacts", "PauseSchedulingState", "ComponentOwnershipFence", "schema_migrations"}

	tests := []struct {
		name           string
		pool           *pgxpool.Pool
		wantTables     []string
		wantMigrations []migrationRow
	}{
		{
			name:           "xid",
			pool:           xidPool,
			wantTables:     []string{"Xids", "schema_migrations"},
			wantMigrations: []migrationRow{{Version: version.XidExpectedSchemaVersion(), Dirty: false}},
		},
		{
			name:           "ess",
			pool:           essPool,
			wantTables:     wantESSTables,
			wantMigrations: []migrationRow{{Version: version.EssExpectedSchemaVersion(), Dirty: false}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.wantTables, presentTables(ctx, t, tt.pool, probed)); diff != "" {
				t.Errorf("present tables mismatch (-want +got):\n%s", diff)
			}
			gotMigrations := queryRows(ctx, t, tt.pool, "SELECT version, dirty FROM schema_migrations", pgx.RowToStructByPos[migrationRow])
			if diff := cmp.Diff(tt.wantMigrations, gotMigrations); diff != "" {
				t.Errorf("schema_migrations rows mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSetup_ReturnsDistinctDatabasesPerCall(t *testing.T) {
	ctx := t.Context()
	firstXID, firstESS := Setup(ctx, t)
	secondXID, secondESS := Setup(ctx, t)

	tests := []struct {
		name          string
		first, second *pgxpool.Pool
	}{
		{name: "xid", first: firstXID, second: secondXID},
		{name: "ess", first: firstESS, second: secondESS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, second := currentDatabase(ctx, t, tt.first), currentDatabase(ctx, t, tt.second)
			if first == second {
				t.Errorf("both Setup calls returned database %q, want distinct databases", first)
			}
		})
	}
}

func TestSetup_CleanupClosesPoolsAndDropsDatabases(t *testing.T) {
	ctx := t.Context()
	var xidPool, essPool *pgxpool.Pool
	var databases []string
	ok := t.Run("setup", func(t *testing.T) {
		xidPool, essPool = Setup(ctx, t)
		databases = []string{currentDatabase(ctx, t, xidPool), currentDatabase(ctx, t, essPool)}
	})
	if !ok {
		t.Fatal("setup subtest failed, so its cleanup cannot be verified")
	}

	srv, err := sharedServer()
	if err != nil {
		t.Fatalf("sharedServer() failed: %v", err)
	}
	for _, db := range databases {
		if queryValue[bool](ctx, t, srv.admin, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", db) {
			t.Errorf("database %q still exists after test cleanup, want dropped", db)
		}
	}
	for _, pool := range []*pgxpool.Pool{xidPool, essPool} {
		if err := pool.Ping(ctx); err == nil {
			t.Error("Ping() on a pool after test cleanup succeeded, want closed pool error")
		}
	}
}

func TestSetupWithPools(t *testing.T) {
	ctx := t.Context()
	pools := SetupWithPools(ctx, t)
	xidDB := currentDatabase(ctx, t, pools.XIDPool)
	essDB := currentDatabase(ctx, t, pools.ESSPool)

	match := regexp.MustCompile(fmt.Sprintf(`^test_xid_db_([0-9a-f]{%d})$`, 2*suffixBytes)).FindStringSubmatch(xidDB)
	if match == nil {
		t.Fatalf("XID database name = %q, want test_xid_db_<%d hex digits>", xidDB, 2*suffixBytes)
	}
	if want := "test_ess_db_" + match[1]; essDB != want {
		t.Errorf("ESS database name = %q, want %q", essDB, want)
	}

	tests := []struct {
		name   string
		dsn    string
		wantDB string
	}{
		{name: "XIDDSN", dsn: pools.XIDDSN, wantDB: xidDB},
		{name: "ESSDSN", dsn: pools.ESSDSN, wantDB: essDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := openPool(ctx, tt.dsn)
			if err != nil {
				t.Fatalf("openPool(%s) failed: %v", tt.name, err)
			}
			t.Cleanup(pool.Close)
			if got := currentDatabase(ctx, t, pool); got != tt.wantDB {
				t.Errorf("current_database() via %s = %q, want %q", tt.name, got, tt.wantDB)
			}
		})
	}
}

func TestSetupEmpty(t *testing.T) {
	ctx := t.Context()
	pools := SetupEmpty(ctx, t)

	tests := []struct {
		name string
		pool *pgxpool.Pool
	}{
		{name: "xid", pool: pools.XIDPool},
		{name: "ess", pool: pools.ESSPool},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tables := queryRows(ctx, t, tt.pool, "SELECT table_name::text FROM information_schema.tables WHERE table_schema = current_schema()", pgx.RowTo[string])
			if len(tables) != 0 {
				t.Errorf("SetupEmpty() created tables %v in the %s database, want none", tables, tt.name)
			}
		})
	}
}

func TestResolveAdminURL_UsesPostgresTestDSN(t *testing.T) {
	const dsn = "postgres://user:secret@external-db:5432/postgres?sslmode=disable"
	t.Setenv(adminDSNEnv, " "+dsn+"\n")

	got, err := resolveAdminURL(t.Context())
	if err != nil {
		t.Fatalf("resolveAdminURL() failed: %v", err)
	}
	if got.String() != dsn {
		t.Errorf("resolveAdminURL() = %q, want %q", got, dsn)
	}
}

func TestParseAdminDSN(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		wantErr bool
	}{
		{name: "postgres scheme", dsn: "postgres://user:secret@db:5432/postgres?sslmode=disable"},
		{name: "postgresql scheme", dsn: "postgresql://db/postgres"},
		{name: "keyword/value DSN", dsn: "host=db user=postgres dbname=postgres", wantErr: true},
		{name: "invalid port", dsn: "postgres://db:port/postgres", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAdminDSN(tt.dsn)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseAdminDSN(%q) error = %v, wantErr %v", tt.dsn, err, tt.wantErr)
			}
			if err == nil && got.String() != tt.dsn {
				t.Errorf("parseAdminDSN(%q) = %q, want %q", tt.dsn, got, tt.dsn)
			}
		})
	}
}

func TestServerDatabaseDSN(t *testing.T) {
	tests := []struct {
		name     string
		adminDSN string
		want     string
	}{
		{
			name:     "keeps credentials and query",
			adminDSN: "postgres://user:secret@db:5432/postgres?sslmode=disable",
			want:     "postgres://user:secret@db:5432/test_db?sslmode=disable",
		},
		{
			name:     "replaces escaped path",
			adminDSN: "postgres://db/admin%2Fdb",
			want:     "postgres://db/test_db",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminURL, err := url.Parse(tt.adminDSN)
			if err != nil {
				t.Fatalf("url.Parse(%q) failed: %v", tt.adminDSN, err)
			}
			srv := &server{adminURL: adminURL}
			if got := srv.databaseDSN("test_db"); got != tt.want {
				t.Errorf("databaseDSN(%q) = %q, want %q", "test_db", got, tt.want)
			}
			if got := adminURL.String(); got != tt.adminDSN {
				t.Errorf("admin URL after databaseDSN() = %q, want unchanged %q", got, tt.adminDSN)
			}
		})
	}
}

// currentDatabase returns the name of the database that pool is connected to.
func currentDatabase(ctx context.Context, t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	return queryValue[string](ctx, t, pool, "SELECT current_database()")
}

// presentTables returns the tables among candidates that exist in pool's database, in candidate
// order. Unquoted names resolve case-insensitively, as in the schema files.
func presentTables(ctx context.Context, t *testing.T, pool *pgxpool.Pool, candidates []string) []string {
	t.Helper()
	var present []string
	for _, table := range candidates {
		if queryValue[bool](ctx, t, pool, "SELECT to_regclass($1) IS NOT NULL", table) {
			present = append(present, table)
		}
	}
	return present
}

// queryValue runs query on pool and scans its single-row, single-column result.
func queryValue[T any](ctx context.Context, t *testing.T, pool *pgxpool.Pool, query string, args ...any) T {
	t.Helper()
	var v T
	if err := pool.QueryRow(ctx, query, args...).Scan(&v); err != nil {
		t.Fatalf("QueryRow(%q) failed: %v", query, err)
	}
	return v
}

// queryRows runs query on pool and collects every result row with rowTo.
func queryRows[T any](ctx context.Context, t *testing.T, pool *pgxpool.Pool, query string, rowTo pgx.RowToFunc[T]) []T {
	t.Helper()
	rows, err := pool.Query(ctx, query)
	if err != nil {
		t.Fatalf("Query(%q) failed: %v", query, err)
	}
	got, err := pgx.CollectRows(rows, rowTo)
	if err != nil {
		t.Fatalf("CollectRows(%q) failed: %v", query, err)
	}
	return got
}
