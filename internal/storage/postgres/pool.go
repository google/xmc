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

package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

// NewPool parses the PostgreSQL DSN, configures the connection pool (applying maxConns when > 0),
// creates the pool with a context detached from caller cancellation, and verifies database
// connectivity via Ping using ctx.
func NewPool(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	return newPool(ctx, dsn, maxConns, (*pgxpool.Pool).Ping)
}

func newPool(ctx context.Context, dsn string, maxConns int32, ping func(*pgxpool.Pool, context.Context) error) (*pgxpool.Pool, error) {
	poolCfg, err := parsePoolConfig(dsn, maxConns)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(context.WithoutCancel(ctx), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres connection pool: %w", err)
	}
	if err := ping(pool, ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping postgres database: %w", err)
	}
	return pool, nil
}

func parsePoolConfig(dsn string, maxConns int32) (*pgxpool.Config, error) {
	trimmedDSN := strings.TrimSpace(dsn)
	if trimmedDSN == "" {
		return nil, pgdb.WrapStatusError("postgres dsn must not be empty", grpcerr.ErrInvalidArgument, nil)
	}
	poolCfg, err := pgxpool.ParseConfig(trimmedDSN)
	if err != nil {
		return nil, pgdb.WrapStatusError(fmt.Sprintf("failed to parse postgres dsn: %v", err), grpcerr.ErrInvalidArgument, err)
	}
	if maxConns > 0 {
		poolCfg.MaxConns = maxConns
	}
	return poolCfg, nil
}
