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

package pgdb

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"xmanager_cloud/internal/grpcerr"
)

// DBTX is the common query and execution interface satisfied by both *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ DBTX = (*pgxpool.Pool)(nil)
	_ DBTX = (pgx.Tx)(nil)
)

// FromPool returns pool as a DBTX, or a nil DBTX when pool is nil, so a nil pool never becomes a
// non-nil interface that wraps a nil pointer.
func FromPool(pool *pgxpool.Pool) DBTX {
	if pool == nil {
		return nil
	}
	return pool
}

// QueryRow executes sql, scans a single row into Row by column name, and translates any error.
func QueryRow[Row any](ctx context.Context, db DBTX, sql, entity string, args ...any) (Row, error) {
	var zero Row
	if db == nil {
		return zero, WrapStatusError("postgres database handle must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return zero, TranslateError(err, entity)
	}
	row, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[Row])
	return row, TranslateError(err, entity)
}

// QueryRows executes sql, scans all returned rows into a slice of Row by column name, and translates any error.
func QueryRows[Row any](ctx context.Context, db DBTX, sql, entity string, args ...any) ([]Row, error) {
	if db == nil {
		return nil, WrapStatusError("postgres database handle must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, TranslateError(err, entity)
	}
	collected, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[Row])
	return collected, TranslateError(err, entity)
}
