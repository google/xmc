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
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
)

type fakeDBTX struct {
	queryErr error
}

func (f *fakeDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (f *fakeDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, f.queryErr
}

func (f *fakeDBTX) QueryRow(context.Context, string, ...any) pgx.Row {
	return nil
}

func TestFromPool(t *testing.T) {
	pool := &pgxpool.Pool{}
	tests := []struct {
		name string
		pool *pgxpool.Pool
		want DBTX
	}{
		{name: "nil pool returns nil DBTX", pool: nil, want: nil},
		{name: "non-nil pool is returned as DBTX", pool: pool, want: pool},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FromPool(tt.pool); got != tt.want {
				t.Errorf("FromPool(%p) = %v, want %v", tt.pool, got, tt.want)
			}
		})
	}
}

func TestQueryRowAndQueryRows_Errors(t *testing.T) {
	type testRow struct {
		ID int64 `db:"id"`
	}
	ctx := t.Context()

	t.Run("nil DBTX returns FailedPrecondition", func(t *testing.T) {
		_, rowErr := QueryRow[testRow](ctx, nil, "SELECT 1", "test")
		_, rowsErr := QueryRows[testRow](ctx, nil, "SELECT 1", "test")
		for name, err := range map[string]error{"QueryRow": rowErr, "QueryRows": rowsErr} {
			if !errors.Is(err, grpcerr.ErrFailedPrecondition) || status.Code(err) != codes.FailedPrecondition {
				t.Errorf("%s(nil) = %v (code %v), want %v", name, err, status.Code(err), grpcerr.ErrFailedPrecondition)
			}
		}
	})

	t.Run("query error is translated", func(t *testing.T) {
		db := &fakeDBTX{queryErr: &pgconn.PgError{Code: SQLStateUniqueViolation}}
		_, rowErr := QueryRow[testRow](ctx, db, "SELECT 1", "test")
		_, rowsErr := QueryRows[testRow](ctx, db, "SELECT 1", "test")
		for name, err := range map[string]error{"QueryRow": rowErr, "QueryRows": rowsErr} {
			if !errors.Is(err, grpcerr.ErrAlreadyExists) || status.Code(err) != codes.AlreadyExists {
				t.Errorf("%s(queryErr) = %v (code %v), want %v", name, err, status.Code(err), grpcerr.ErrAlreadyExists)
			}
		}
	})
}
