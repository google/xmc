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

// External test package: postgres.NewStorage, which runs the transactions under test, imports
// pausestate.
package pausestate_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
)

const (
	// clockTolerance bounds how far a database-set UpdatedAt may be from the test's clock.
	clockTolerance = time.Minute
	// lostUpdateWindow is how long each concurrent read-modify-write waits between its read and
	// its write. Without a row lock, concurrent transactions then read the same state and
	// overwrite each other's updates; with the lock, they merely run one after another.
	lostUpdateWindow = 20 * time.Millisecond
)

var ignoreUpdatedAt = cmpopts.IgnoreFields(storage.PauseSchedulingState{}, "UpdatedAt")

// stateGetters read the state through both repositories: the plain Repository.Get and, in a
// transaction of its own, the locking TxRepository.Get.
var stateGetters = []struct {
	name string
	get  func(ctx context.Context, store *postgres.Storage) (*storage.PauseSchedulingState, error)
}{
	{name: "Repository", get: getState},
	{name: "TxRepository", get: getStateInTx},
}

// nullColumns reports which nullable PauseSchedulingState columns are stored as SQL NULL.
type nullColumns struct {
	ResumptionRunID    bool
	ResumptionWorkerID bool
}

// newTestStorage returns PostgreSQL storage backed by a fresh ESS test database, together with
// the database's pool for raw SQL assertions.
func newTestStorage(t *testing.T) (*postgres.Storage, *pgxpool.Pool) {
	t.Helper()
	_, essPool := postgrestest.Setup(t.Context(), t)
	store, err := postgres.NewStorage(essPool)
	if err != nil {
		t.Fatalf("postgres.NewStorage() unexpected error: %v", err)
	}
	return store, essPool
}

// getState reads the state with Repository.Get.
func getState(ctx context.Context, store *postgres.Storage) (*storage.PauseSchedulingState, error) {
	return store.PauseState().Get(ctx)
}

// getStateInTx reads the state with TxRepository.Get in a transaction of its own.
func getStateInTx(ctx context.Context, store *postgres.Storage) (*storage.PauseSchedulingState, error) {
	var state *storage.PauseSchedulingState
	err := store.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		var err error
		state, err = tx.PauseState.Get(ctx)
		return err
	})
	return state, err
}

// setState writes state with TxRepository.Set in a transaction of its own.
func setState(ctx context.Context, store *postgres.Storage, state *storage.PauseSchedulingState) error {
	return store.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		return tx.PauseState.Set(ctx, state)
	})
}

// queryNullColumns reads, with raw SQL that bypasses the repository, which nullable columns of the
// stored singleton row are NULL.
func queryNullColumns(ctx context.Context, t *testing.T, pool *pgxpool.Pool) nullColumns {
	t.Helper()
	const query = "SELECT ResumptionRunId IS NULL, ResumptionWorkerId IS NULL FROM PauseSchedulingState WHERE Id = 1"
	var got nullColumns
	if err := pool.QueryRow(ctx, query).Scan(&got.ResumptionRunID, &got.ResumptionWorkerID); err != nil {
		t.Fatalf("QueryRow(%q) unexpected error: %v", query, err)
	}
	return got
}

// appendResumptionRunID is a read-modify-write: it reads the state, appends id to its
// comma-separated ResumptionRunID and writes the state back.
func appendResumptionRunID(ctx context.Context, repo storage.PauseStateTxRepository, id string) error {
	state, err := repo.Get(ctx)
	if err != nil {
		return err
	}
	time.Sleep(lostUpdateWindow)
	if state.ResumptionRunID != "" {
		id = state.ResumptionRunID + "," + id
	}
	state.ResumptionRunID = id
	return repo.Set(ctx, state)
}

func TestGet_EmptyDatabaseReturnsDefault(t *testing.T) {
	for _, getter := range stateGetters {
		t.Run(getter.name, func(t *testing.T) {
			store, _ := newTestStorage(t)

			got, err := getter.get(t.Context(), store)
			if err != nil {
				t.Fatalf("Get() unexpected error: %v", err)
			}
			if diff := cmp.Diff(storage.DefaultPauseSchedulingState(), got); diff != "" {
				t.Errorf("Get() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRepository_GetReadErrorIsNotMaskedAsDefault(t *testing.T) {
	store, _ := newTestStorage(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if got, err := getState(ctx, store); !errors.Is(err, context.Canceled) {
		t.Errorf("Get(canceled ctx) = %+v, %v; want error %v", got, err, context.Canceled)
	}
}

func TestTxRepository_GetSeedsDefaultRow(t *testing.T) {
	ctx := t.Context()
	store, essPool := newTestStorage(t)
	if _, err := getStateInTx(ctx, store); err != nil {
		t.Fatalf("TxRepository.Get() unexpected error: %v", err)
	}

	// The committed seed row reads exactly like the default returned for a missing row.
	got, err := getState(ctx, store)
	if err != nil {
		t.Fatalf("Repository.Get() unexpected error: %v", err)
	}
	if diff := cmp.Diff(storage.DefaultPauseSchedulingState(), got); diff != "" {
		t.Errorf("Repository.Get() of the seeded row mismatch (-want +got):\n%s", diff)
	}
	wantNull := nullColumns{ResumptionRunID: true, ResumptionWorkerID: true}
	if diff := cmp.Diff(wantNull, queryNullColumns(ctx, t, essPool)); diff != "" {
		t.Errorf("seeded row NULL columns mismatch (-want +got):\n%s", diff)
	}
}

func TestTxRepository_SetThenGet(t *testing.T) {
	// Set ignores this caller-provided UpdatedAt; the database sets UpdatedAt itself.
	staleUpdatedAt := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	tests := []struct {
		name     string
		state    *storage.PauseSchedulingState
		wantNull nullColumns
	}{
		{
			name: "paused_with_resumption_ids",
			state: &storage.PauseSchedulingState{
				SchedulingPaused:   true,
				ResumptionRunID:    "run-123",
				ResumptionWorkerID: "worker-456",
				UpdatedAt:          staleUpdatedAt,
			},
		},
		{
			name:     "unpaused_with_empty_resumption_ids",
			state:    &storage.PauseSchedulingState{SchedulingPaused: false, UpdatedAt: staleUpdatedAt},
			wantNull: nullColumns{ResumptionRunID: true, ResumptionWorkerID: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			store, essPool := newTestStorage(t)
			if err := setState(ctx, store, tt.state); err != nil {
				t.Fatalf("Set() unexpected error: %v", err)
			}

			for _, getter := range stateGetters {
				got, err := getter.get(ctx, store)
				if err != nil {
					t.Fatalf("%s.Get() unexpected error: %v", getter.name, err)
				}
				if diff := cmp.Diff(tt.state, got, ignoreUpdatedAt); diff != "" {
					t.Errorf("%s.Get() mismatch (-want +got):\n%s", getter.name, diff)
				}
				if got.UpdatedAt.IsZero() || time.Since(got.UpdatedAt).Abs() > clockTolerance {
					t.Errorf("%s.Get() UpdatedAt = %v, want a database timestamp within %v of now", getter.name, got.UpdatedAt, clockTolerance)
				}
			}
			if diff := cmp.Diff(tt.wantNull, queryNullColumns(ctx, t, essPool)); diff != "" {
				t.Errorf("stored NULL columns mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTxRepository_SetOverwritesAndRefreshesTimestamp(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStorage(t)
	if err := setState(ctx, store, &storage.PauseSchedulingState{ResumptionRunID: "run-1", ResumptionWorkerID: "worker-1"}); err != nil {
		t.Fatalf("Set() unexpected error: %v", err)
	}
	first, err := getState(ctx, store)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}

	want := &storage.PauseSchedulingState{SchedulingPaused: true}
	if err := setState(ctx, store, want); err != nil {
		t.Fatalf("Set() unexpected error: %v", err)
	}
	second, err := getState(ctx, store)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}

	if diff := cmp.Diff(want, second, ignoreUpdatedAt); diff != "" {
		t.Errorf("Get() after overwrite mismatch (-want +got):\n%s", diff)
	}
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("UpdatedAt after overwrite = %v, want after %v", second.UpdatedAt, first.UpdatedAt)
	}
}

func TestTxRepository_GetAndSetInTransaction(t *testing.T) {
	errAbort := errors.New("abort")
	state := &storage.PauseSchedulingState{SchedulingPaused: true, ResumptionRunID: "run-1", ResumptionWorkerID: "worker-1"}

	tests := []struct {
		name     string
		state    *storage.PauseSchedulingState
		fnErr    error
		wantErr  error
		wantCode codes.Code
		want     *storage.PauseSchedulingState
	}{
		{name: "committed", state: state, want: state},
		{name: "rolled_back", state: state, fnErr: errAbort, wantErr: errAbort, wantCode: codes.Unknown, want: storage.DefaultPauseSchedulingState()},
		{name: "nil_state", state: nil, wantErr: grpcerr.ErrInvalidArgument, wantCode: codes.InvalidArgument, want: storage.DefaultPauseSchedulingState()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			store, _ := newTestStorage(t)
			err := store.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
				if _, err := tx.PauseState.Get(ctx); err != nil {
					return err
				}
				if err := tx.PauseState.Set(ctx, tt.state); err != nil {
					return err
				}
				return tt.fnErr
			})
			if !errors.Is(err, tt.wantErr) || status.Code(err) != tt.wantCode {
				t.Fatalf("RunInTransaction() error = %v (code %v), want %v (code %v)", err, status.Code(err), tt.wantErr, tt.wantCode)
			}

			got, err := getState(ctx, store)
			if err != nil {
				t.Fatalf("Get() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, ignoreUpdatedAt); diff != "" {
				t.Errorf("Get() after transaction mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Concurrent read-modify-write transactions must not lose updates: TxRepository.Get locks the row,
// so each transaction reads the state that the previous one committed.
func TestTxRepository_GetLocksRowAgainstLostUpdates(t *testing.T) {
	const workers = 10
	ctx := t.Context()
	store, _ := newTestStorage(t)
	wantIDs := make([]string, workers)
	for i := range wantIDs {
		wantIDs[i] = fmt.Sprintf("run-%02d", i)
	}

	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i, id := range wantIDs {
		wg.Go(func() {
			errs[i] = store.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
				return appendResumptionRunID(ctx, tx.PauseState, id)
			})
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("RunInTransaction() unexpected error: %v", err)
	}

	got, err := getState(ctx, store)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	gotIDs := strings.Split(got.ResumptionRunID, ",")
	slices.Sort(gotIDs)
	if diff := cmp.Diff(wantIDs, gotIDs); diff != "" {
		t.Errorf("appended resumption run IDs mismatch (-want +got); missing IDs are lost updates:\n%s", diff)
	}
}
