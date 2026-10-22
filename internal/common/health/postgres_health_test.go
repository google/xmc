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

package health

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	pgversion "xmanager_cloud/internal/storage/postgres/migrations/version"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
)

func TestCheckPostgres(t *testing.T) {
	ctx := t.Context()

	t.Run("nil pool returns ErrNilClient", func(t *testing.T) {
		if err := CheckPostgres(ctx, nil); !errors.Is(err, ErrNilClient) {
			t.Errorf("CheckPostgres(ctx, nil) = %v, want %v", err, ErrNilClient)
		}
	})

	t.Run("healthy pool returns no error", func(t *testing.T) {
		xidPool, _ := postgrestest.Setup(ctx, t)
		if err := CheckPostgres(ctx, xidPool); err != nil {
			t.Errorf("CheckPostgres(ctx, healthyPool) = %v, want nil", err)
		}
	})

	t.Run("closed pool returns error", func(t *testing.T) {
		pool, _ := postgrestest.NewEmptyDatabase(ctx, t)
		pool.Close()

		if err := CheckPostgres(ctx, pool); err == nil {
			t.Error("CheckPostgres(ctx, closedPool) succeeded, want error")
		}
	})

	t.Run("realPostgresHealthClient Close closes underlying pool", func(t *testing.T) {
		pool, _ := postgrestest.NewEmptyDatabase(ctx, t)
		healthCl := NewPostgresHealthClient(pool)
		closer, ok := healthCl.(interface{ Close() error })
		if !ok {
			t.Fatal("expected realPostgresHealthClient to implement Close()")
		}
		if err := closer.Close(); err != nil {
			t.Fatalf("unexpected error on Close: %v", err)
		}
		if err := CheckPostgres(ctx, pool); err == nil {
			t.Error("CheckPostgres(ctx, pool) after Close() succeeded, want error")
		}
	})
}

func TestPostgresHealthClientWithLiveDatabase(t *testing.T) {
	ctx := t.Context()

	t.Run("nil pool returns ErrNilClient", func(t *testing.T) {
		_, _, err := NewPostgresHealthClient(nil).QuerySchemaVersion(ctx)
		if !errors.Is(err, ErrNilClient) {
			t.Errorf("QuerySchemaVersion(nil pool) error = %v, want %v", err, ErrNilClient)
		}
	})

	t.Run("missing schema_migrations table returns error", func(t *testing.T) {
		pool, _ := postgrestest.NewEmptyDatabase(ctx, t)
		_, _, err := NewPostgresHealthClient(pool).QuerySchemaVersion(ctx)
		if err == nil || !strings.Contains(err.Error(), "failed to query schema_migrations") {
			t.Errorf("QuerySchemaVersion(missing table) error = %v, want query error", err)
		}
	})

	xidPool, essPool := postgrestest.Setup(ctx, t)
	services := []struct {
		name            string
		pool            *pgxpool.Pool
		checkFn         func(context.Context, PostgresHealthClient) error
		expectedVersion int64
	}{
		{
			name:            "ESS",
			pool:            essPool,
			checkFn:         CheckPostgresESS,
			expectedVersion: pgversion.EssExpectedSchemaVersion(),
		},
		{
			name:            "XID",
			pool:            xidPool,
			checkFn:         CheckPostgresXID,
			expectedVersion: pgversion.XidExpectedSchemaVersion(),
		},
	}

	for _, svc := range services {
		t.Run(svc.name, func(t *testing.T) {
			runLiveServiceChecks(ctx, t, svc.pool, svc.checkFn, svc.expectedVersion)
		})
	}
}

func runLiveServiceChecks(
	ctx context.Context,
	t *testing.T,
	pool *pgxpool.Pool,
	checkFn func(context.Context, PostgresHealthClient) error,
	expectedVersion int64,
) {
	t.Helper()
	client := NewPostgresHealthClient(pool)

	if err := checkFn(ctx, client); err != nil {
		t.Fatalf("checkFn(matching version) = %v, want nil", err)
	}

	if _, err := pool.Exec(ctx, "UPDATE schema_migrations SET dirty = true"); err != nil {
		t.Fatalf("failed to mark schema_migrations dirty: %v", err)
	}
	if err := checkFn(ctx, client); err == nil || !strings.Contains(err.Error(), "dirty state") {
		t.Errorf("checkFn(dirty=true) = %v, want dirty state error", err)
	}

	if _, err := pool.Exec(ctx, "UPDATE schema_migrations SET dirty = false, version = $1", expectedVersion+1); err != nil {
		t.Fatalf("failed to update schema_migrations version: %v", err)
	}
	if err := checkFn(ctx, client); err == nil || !strings.Contains(err.Error(), "does not match expected version") {
		t.Errorf("checkFn(version mismatch) = %v, want version mismatch error", err)
	}

	if _, err := pool.Exec(ctx, "DELETE FROM schema_migrations"); err != nil {
		t.Fatalf("failed to clear schema_migrations: %v", err)
	}
	err := checkFn(ctx, client)
	if err == nil || !strings.Contains(err.Error(), "no migration records found in schema_migrations table") {
		t.Errorf("checkFn(empty schema_migrations) = %v, want no migration records error", err)
	}
}

func TestCheckPostgresServices(t *testing.T) {
	ctx := t.Context()

	cases := []struct {
		name       string
		versionOff int64
		dirty      bool
		dbErr      error
		isNil      bool
		wantErr    bool
	}{
		{
			name:    "success when version matches",
			wantErr: false,
		},
		{
			name:       "fails when version mismatch (higher)",
			versionOff: 1,
			wantErr:    true,
		},
		{
			name:       "fails when version mismatch (lower)",
			versionOff: -1,
			wantErr:    true,
		},
		{
			name:    "fails when dirty",
			dirty:   true,
			wantErr: true,
		},
		{
			name:    "fails on postgres error",
			dbErr:   errors.New("postgres error"),
			wantErr: true,
		},
		{
			name:    "fails when client is nil",
			isNil:   true,
			wantErr: true,
		},
	}

	services := []struct {
		name            string
		checkFn         func(context.Context, PostgresHealthClient) error
		expectedVersion int64
	}{
		{
			name:            "ESS",
			checkFn:         CheckPostgresESS,
			expectedVersion: pgversion.EssExpectedSchemaVersion(),
		},
		{
			name:            "XID",
			checkFn:         CheckPostgresXID,
			expectedVersion: pgversion.XidExpectedSchemaVersion(),
		},
	}

	for _, svc := range services {
		t.Run(svc.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					var client PostgresHealthClient
					if !tc.isNil {
						client = mockSpannerHealthClient{
							version: svc.expectedVersion + tc.versionOff,
							dirty:   tc.dirty,
							err:     tc.dbErr,
						}
					}
					err := svc.checkFn(ctx, client)
					if (err != nil) != tc.wantErr {
						t.Errorf("checkFn() error = %v, wantErr %v", err, tc.wantErr)
					}
				})
			}
		})
	}

	t.Run("fails when expected version is non-positive", func(t *testing.T) {
		client := mockSpannerHealthClient{version: 1}
		if err := checkPostgresWithVersion(ctx, client, 0); err == nil {
			t.Error("checkPostgresWithVersion(ctx, client, 0) succeeded, want error")
		}
	})
}
