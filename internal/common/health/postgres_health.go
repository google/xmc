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

// postgresSchemaVersionQuery reads the table that the PostgreSQL migration runner (golang-migrate)
// keeps in every migrated database.
const postgresSchemaVersionQuery = "SELECT version, dirty FROM schema_migrations LIMIT 1"

// rowQuerier is the subset of *pgxpool.Pool that the PostgreSQL health client uses.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ rowQuerier = (*pgxpool.Pool)(nil)

type postgresHealthClient struct {
	db rowQuerier
}

// NewPostgresHealthClient creates a SchemaVersionClient that reads the schema_migrations table
// through pool. The caller keeps ownership of pool and closes it.
func NewPostgresHealthClient(pool *pgxpool.Pool) SchemaVersionClient {
	if pool == nil {
		return &postgresHealthClient{}
	}
	return &postgresHealthClient{db: pool}
}

// QuerySchemaVersion implements SchemaVersionClient by querying the schema_migrations table.
func (c *postgresHealthClient) QuerySchemaVersion(ctx context.Context) (version int64, dirty bool, err error) {
	if c.db == nil {
		return 0, false, ErrNilClient
	}

	err = c.db.QueryRow(ctx, postgresSchemaVersionQuery).Scan(&version, &dirty)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, errors.New("no migration records found in schema_migrations table")
	}
	if err != nil {
		return 0, false, fmt.Errorf("failed to query schema_migrations: %w", err)
	}
	return version, dirty, nil
}
