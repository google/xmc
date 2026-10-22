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
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresHealthClient defines the subset of PostgreSQL functionality needed for health checks.
type PostgresHealthClient = SchemaVersionClient

var _ PostgresHealthClient = (*realPostgresHealthClient)(nil)

type realPostgresHealthClient struct {
	pool *pgxpool.Pool
}

// NewPostgresHealthClient creates a new PostgresHealthClient wrapping a concrete pgxpool.Pool.
func NewPostgresHealthClient(pool *pgxpool.Pool) PostgresHealthClient {
	return &realPostgresHealthClient{pool: pool}
}

// QuerySchemaVersion implements PostgresHealthClient by querying the schema_migrations table.
func (c *realPostgresHealthClient) QuerySchemaVersion(ctx context.Context) (version int64, dirty bool, err error) {
	if c.pool == nil {
		return 0, false, ErrNilClient
	}

	var versionVal int64
	var dirtyVal bool
	err = c.pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations LIMIT 1").Scan(&versionVal, &dirtyVal)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("no migration records found in schema_migrations table")
	}
	if err != nil {
		return 0, false, fmt.Errorf("failed to query schema_migrations: %w", err)
	}

	return versionVal, dirtyVal, nil
}

// Close closes the underlying PostgreSQL connection pool if non-nil.
func (c *realPostgresHealthClient) Close() error {
	if c.pool != nil {
		c.pool.Close()
	}
	return nil
}
