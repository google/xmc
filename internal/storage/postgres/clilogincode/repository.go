// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package clilogincode provides the PostgreSQL store for one-time CLI login codes.
package clilogincode

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/pkg/auth/oauth2"
)

const entityName = "cli login code"

var (
	insertCodeSQL = safesql.New(`INSERT INTO CliLoginCodes (CodeHash, Email, Name, CodeChallenge, ExpiresAt)
VALUES (@codehash, @email, @name, @codechallenge, @expiresat)`)

	consumeCodeSQL = safesql.New(`DELETE FROM CliLoginCodes
WHERE CodeHash = $1
RETURNING CodeHash, Email, Name, CodeChallenge, ExpiresAt`)
)

var _ storage.CLILoginCodeRepository = (*Repository)(nil)

// Repository implements storage.CLILoginCodeRepository for PostgreSQL.
type Repository struct {
	db pgdb.DBTX
}

type cliLoginCodeRow struct {
	CodeHash      []byte      `db:"codehash"`
	Email         string      `db:"email"`
	Name          pgtype.Text `db:"name"`
	CodeChallenge string      `db:"codechallenge"`
	ExpiresAt     time.Time   `db:"expiresat"`
}

// NewRepository creates a new PostgreSQL CLI login code Repository bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool)}
}

// Save inserts a new CLI login authorization code. Inserting a hash that already exists fails.
func (r *Repository) Save(ctx context.Context, code oauth2.CLILoginCode) error {
	_, err := r.db.Exec(ctx, insertCodeSQL.String(), insertArgs(code))
	return pgdb.TranslateError(err, entityName)
}

// Consume atomically deletes and returns the CLI login code matching hash, so concurrent
// redemptions of the same code succeed at most once.
func (r *Repository) Consume(ctx context.Context, hash []byte) (oauth2.CLILoginCode, error) {
	row, err := pgdb.QueryRow[cliLoginCodeRow](ctx, r.db, consumeCodeSQL.String(), entityName, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return oauth2.CLILoginCode{}, oauth2.ErrCLICodeNotFound
	}
	if err != nil {
		return oauth2.CLILoginCode{}, err
	}
	return row.toCode(), nil
}

func insertArgs(code oauth2.CLILoginCode) pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"codehash":      code.Hash,
		"email":         code.Email,
		"name":          pgtype.Text{String: code.Name, Valid: code.Name != ""},
		"codechallenge": code.CodeChallenge,
		"expiresat":     code.ExpiresAt,
	}
}

func (r cliLoginCodeRow) toCode() oauth2.CLILoginCode {
	return oauth2.CLILoginCode{
		Hash:          r.CodeHash,
		Email:         r.Email,
		Name:          r.Name.String,
		CodeChallenge: r.CodeChallenge,
		ExpiresAt:     r.ExpiresAt.UTC(),
	}
}
