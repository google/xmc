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

// Package pausestate provides the PostgreSQL implementation for persisting global scheduling pause state.
package pausestate

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

// singletonID is the only permitted primary key value of the PauseSchedulingState table.
const singletonID int64 = 1

// entityName names the pause scheduling state in translated storage errors.
const entityName = "pause scheduling state"

// selectStateSQL reads the singleton row without locking it.
var selectStateSQL = safesql.New(`SELECT SchedulingPaused, ResumptionRunId, ResumptionWorkerId, UpdatedAt
FROM PauseSchedulingState
WHERE Id = $1`)

// lockStateSQL reads the singleton row and locks it until the transaction ends. FOR NO KEY UPDATE,
// unlike FOR UPDATE, does not conflict with the FOR KEY SHARE locks that foreign-key checks take.
var lockStateSQL = safesql.New(`SELECT SchedulingPaused, ResumptionRunId, ResumptionWorkerId, UpdatedAt
FROM PauseSchedulingState
WHERE Id = $1
FOR NO KEY UPDATE`)

// seedStateSQL inserts the singleton row unless it already exists.
var seedStateSQL = safesql.New(`INSERT INTO PauseSchedulingState (Id, SchedulingPaused, ResumptionRunId, ResumptionWorkerId, UpdatedAt)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (Id) DO NOTHING`)

// upsertStateSQL inserts or overwrites the singleton row, setting UpdatedAt to the database's now().
var upsertStateSQL = safesql.New(`INSERT INTO PauseSchedulingState (Id, SchedulingPaused, ResumptionRunId, ResumptionWorkerId, UpdatedAt)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (Id) DO UPDATE SET
  SchedulingPaused = EXCLUDED.SchedulingPaused,
  ResumptionRunId = EXCLUDED.ResumptionRunId,
  ResumptionWorkerId = EXCLUDED.ResumptionWorkerId,
  UpdatedAt = EXCLUDED.UpdatedAt`)

var (
	_ storage.PauseStateRepository   = (*Repository)(nil)
	_ storage.PauseStateTxRepository = (*TxRepository)(nil)
)

// Repository persists the scheduling pause state in PostgreSQL.
type Repository struct {
	db pgdb.DBTX
}

// TxRepository persists the scheduling pause state within a bound read-write transaction.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a PostgreSQL-backed pause state Repository bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool)}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{tx: tx}
}

// Get retrieves the current global scheduling pause state without locking it, so callers that
// write the state back must read it with TxRepository.Get instead. If the singleton row does not
// exist yet, it returns storage.DefaultPauseSchedulingState.
func (r *Repository) Get(ctx context.Context) (*storage.PauseSchedulingState, error) {
	state, err := scanState(r.db.QueryRow(ctx, selectStateSQL.String(), singletonID))
	if errors.Is(err, pgx.ErrNoRows) {
		return storage.DefaultPauseSchedulingState(), nil
	}
	if err != nil {
		return nil, pgdb.TranslateError(err, entityName)
	}
	return state, nil
}

// Get retrieves the current global scheduling pause state within the transaction and locks the
// singleton row until the transaction ends. If the row does not exist yet, Get first inserts it
// with the values of storage.DefaultPauseSchedulingState.
func (r *TxRepository) Get(ctx context.Context) (*storage.PauseSchedulingState, error) {
	// RunInTransaction callbacks run at READ COMMITTED, where a plain SELECT takes no row lock and
	// concurrent read-modify-write callers would silently overwrite each other's updates. Locking a
	// missing row locks nothing, so seed the row before locking it with FOR NO KEY UPDATE, which
	// serializes those callers until this transaction ends.
	if err := r.seedDefaultState(ctx); err != nil {
		return nil, err
	}
	state, err := scanState(r.tx.QueryRow(ctx, lockStateSQL.String(), singletonID))
	if err != nil {
		return nil, pgdb.TranslateError(err, entityName)
	}
	return state, nil
}

// Set upserts the singleton row within the transaction, storing empty resumption IDs as NULL.
// The database sets UpdatedAt to now(), the start time of the transaction, and any caller-provided
// value is ignored; the Spanner implementation uses the commit timestamp instead.
func (r *TxRepository) Set(ctx context.Context, state *storage.PauseSchedulingState) error {
	if state == nil {
		return pgdb.WrapStatusError("pause scheduling state must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	_, err := r.tx.Exec(ctx, upsertStateSQL.String(),
		singletonID, state.SchedulingPaused, nullText(state.ResumptionRunID), nullText(state.ResumptionWorkerID))
	return pgdb.TranslateError(err, entityName)
}

// seedDefaultState inserts storage.DefaultPauseSchedulingState as the singleton row unless the row
// already exists.
func (r *TxRepository) seedDefaultState(ctx context.Context) error {
	def := storage.DefaultPauseSchedulingState()
	_, err := r.tx.Exec(ctx, seedStateSQL.String(),
		singletonID, def.SchedulingPaused, nullText(def.ResumptionRunID), nullText(def.ResumptionWorkerID), def.UpdatedAt)
	return pgdb.TranslateError(err, entityName)
}

// scanState decodes a row of selectStateSQL or lockStateSQL, mapping NULL resumption IDs to empty
// strings. It returns pgx.ErrNoRows when the singleton row does not exist.
func scanState(row pgx.Row) (*storage.PauseSchedulingState, error) {
	var (
		state              storage.PauseSchedulingState
		resumptionRunID    pgtype.Text
		resumptionWorkerID pgtype.Text
	)
	if err := row.Scan(&state.SchedulingPaused, &resumptionRunID, &resumptionWorkerID, &state.UpdatedAt); err != nil {
		return nil, err
	}
	state.ResumptionRunID = resumptionRunID.String
	state.ResumptionWorkerID = resumptionWorkerID.String
	return &state, nil
}

// nullText maps an empty string to SQL NULL, matching the nullable resumption ID columns.
func nullText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
