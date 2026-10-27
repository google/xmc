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
	"fmt"

	"xmanager_cloud/internal/common/health"
	"xmanager_cloud/internal/storage/dbconfig"
	"xmanager_cloud/internal/storage/postgres"
	pgversion "xmanager_cloud/internal/storage/postgres/migrations/version"
)

// openPostgres opens a connection pool for db and wraps it in a Store that owns the pool. Errors
// name the database rather than the connection URL, which may contain the password.
func openPostgres(ctx context.Context, cfg dbconfig.Postgres, db Database) (*Store, error) {
	dsn, expectedVersion, err := postgresDatabase(cfg, db)
	if err != nil {
		return nil, fmt.Errorf("failed to build the postgres %s connection URL: %w", db, err)
	}
	pool, err := postgres.NewPool(ctx, dsn, cfg.MaxConns)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the postgres %s database: %w", db, err)
	}
	st, err := postgres.NewStorage(pool)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to create postgres storage for %s: %w", db, err)
	}
	return &Store{
		Storage:         st,
		schemaVersion:   health.NewPostgresHealthClient(pool),
		expectedVersion: expectedVersion,
		closeFn:         pool.Close,
	}, nil
}

// postgresDatabase returns the connection URL of db and the PostgreSQL schema version that this
// binary expects in it.
func postgresDatabase(cfg dbconfig.Postgres, db Database) (string, int64, error) {
	if db == XID {
		dsn, err := cfg.XidDSN()
		return dsn, pgversion.XidExpectedSchemaVersion(), err
	}
	dsn, err := cfg.EssDSN()
	return dsn, pgversion.EssExpectedSchemaVersion(), err
}
