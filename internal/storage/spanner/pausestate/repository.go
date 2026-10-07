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

// Package pausestate provides the Cloud Spanner implementation for persisting global scheduling pause state.
package pausestate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"
	"xmanager_cloud/internal/storage"
)

// TableName is the Spanner table name persisting the scheduling pause state.
const TableName = "PauseSchedulingState"

// Spanner column names for the PauseSchedulingState table.
const (
	ColID                 = "Id"
	ColSchedulingPaused   = "SchedulingPaused"
	ColResumptionRunID    = "ResumptionRunId"
	ColResumptionWorkerID = "ResumptionWorkerId"
	ColUpdatedAt          = "UpdatedAt"
)

// singletonID is the only permitted primary key value of the PauseSchedulingState table.
const singletonID int64 = 1

var (
	// ErrInvalidArgument indicates a caller provided an invalid argument (such as a nil state).
	ErrInvalidArgument = errors.New("invalid argument")
)

var _ storage.PauseStateRepository = (*Repository)(nil)
var _ storage.PauseStateTxRepository = (*TxRepository)(nil)

var readColumns = []string{ColSchedulingPaused, ColResumptionRunID, ColResumptionWorkerID, ColUpdatedAt}

var writeColumns = []string{ColID, ColSchedulingPaused, ColResumptionRunID, ColResumptionWorkerID, ColUpdatedAt}

// rowReader is implemented by both single-use reads and read-write transactions.
type rowReader interface {
	ReadRow(ctx context.Context, table string, key spanner.Key, columns []string) (*spanner.Row, error)
}

// Repository persists the scheduling pause state in Cloud Spanner.
type Repository struct {
	client *spanner.Client
}

// TxRepository persists the scheduling pause state within a bound read-write transaction.
type TxRepository struct {
	tx *spanner.ReadWriteTransaction
}

// NewRepository creates a Spanner-backed pause state Repository.
func NewRepository(client *spanner.Client) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil")
	}
	return &Repository{client: client}, nil
}

// WithTx returns a repository bound to the given read-write transaction.
func (r *Repository) WithTx(tx *spanner.ReadWriteTransaction) *TxRepository {
	return &TxRepository{tx: tx}
}

// Get retrieves the current global scheduling pause state.
// If the singleton row does not exist yet, it returns storage.DefaultPauseSchedulingState.
func (r *Repository) Get(ctx context.Context) (*storage.PauseSchedulingState, error) {
	return readState(ctx, r.client.Single())
}

// Get retrieves the current global scheduling pause state within the transaction.
func (r *TxRepository) Get(ctx context.Context) (*storage.PauseSchedulingState, error) {
	return readState(ctx, r.tx)
}

// Set buffers an upsert of the singleton row in the transaction. UpdatedAt is set to the commit timestamp.
func (r *TxRepository) Set(ctx context.Context, state *storage.PauseSchedulingState) error {
	mutation, err := stateMutation(state)
	if err != nil {
		return err
	}
	return r.tx.BufferWrite([]*spanner.Mutation{mutation})
}

func readState(ctx context.Context, reader rowReader) (*storage.PauseSchedulingState, error) {
	row, err := reader.ReadRow(ctx, TableName, spanner.Key{singletonID}, readColumns)
	if spanner.ErrCode(err) == codes.NotFound {
		return storage.DefaultPauseSchedulingState(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read pause scheduling state: %w", err)
	}
	return rowToState(row)
}

func stateMutation(state *storage.PauseSchedulingState) (*spanner.Mutation, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state cannot be nil", ErrInvalidArgument)
	}
	return spanner.InsertOrUpdate(TableName, writeColumns, []any{
		singletonID,
		state.SchedulingPaused,
		toNullString(state.ResumptionRunID),
		toNullString(state.ResumptionWorkerID),
		spanner.CommitTimestamp,
	}), nil
}

func rowToState(row *spanner.Row) (*storage.PauseSchedulingState, error) {
	var (
		paused             bool
		resumptionRunID    spanner.NullString
		resumptionWorkerID spanner.NullString
		updatedAt          time.Time
	)
	if err := row.Columns(&paused, &resumptionRunID, &resumptionWorkerID, &updatedAt); err != nil {
		return nil, fmt.Errorf("failed to decode pause scheduling state row: %w", err)
	}
	return &storage.PauseSchedulingState{
		SchedulingPaused:   paused,
		UpdatedAt:          updatedAt,
		ResumptionRunID:    resumptionRunID.StringVal,
		ResumptionWorkerID: resumptionWorkerID.StringVal,
	}, nil
}

// toNullString maps an empty string to SQL NULL, matching the nullable resumption columns.
func toNullString(s string) spanner.NullString {
	return spanner.NullString{StringVal: s, Valid: s != ""}
}
