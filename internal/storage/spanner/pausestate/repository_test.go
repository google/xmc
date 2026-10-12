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

package pausestate

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/xmc/internal/grpcerr"
	"github.com/google/xmc/internal/storage"
	"github.com/google/xmc/internal/storage/spanner/emulator"
)

var ignoreUpdatedAt = cmpopts.IgnoreFields(storage.PauseSchedulingState{}, "UpdatedAt")

func newTestRepository(t *testing.T) *Repository {
	t.Helper()
	_, essClient := emulator.Setup(context.Background(), t)
	repo, err := NewRepository(essClient)
	if err != nil {
		t.Fatalf("NewRepository() unexpected error: %v", err)
	}
	return repo
}

func TestNewRepository_NilClient(t *testing.T) {
	if _, err := NewRepository(nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("NewRepository(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
}

func TestRepository_GetUninitializedReturnsDefault(t *testing.T) {
	repo := newTestRepository(t)

	got, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if diff := cmp.Diff(storage.DefaultPauseSchedulingState(), got); diff != "" {
		t.Errorf("Get() mismatch (-want +got):\n%s", diff)
	}
}

func TestRepository_GetReadErrorIsNotMaskedAsDefault(t *testing.T) {
	repo := newTestRepository(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if got, err := repo.Get(ctx); err == nil {
		t.Errorf("Get(cancelled ctx) = %+v, nil; want error", got)
	}
}

func setInTx(ctx context.Context, repo *Repository, state *storage.PauseSchedulingState) error {
	_, err := repo.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		return repo.WithTx(tx).Set(ctx, state)
	})
	return err
}

func TestTxRepository_SetAndGet(t *testing.T) {
	tests := []struct {
		name  string
		state *storage.PauseSchedulingState
	}{
		{
			name:  "paused_without_resumption_ids",
			state: &storage.PauseSchedulingState{SchedulingPaused: true},
		},
		{
			name: "resumed_with_resumption_ids",
			state: &storage.PauseSchedulingState{
				SchedulingPaused:   false,
				ResumptionRunID:    "run-123",
				ResumptionWorkerID: "worker-456",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			repo := newTestRepository(t)

			before := time.Now().Add(-time.Minute)
			if err := setInTx(ctx, repo, tt.state); err != nil {
				t.Fatalf("Set() unexpected error: %v", err)
			}
			got, err := repo.Get(ctx)
			if err != nil {
				t.Fatalf("Get() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.state, got, ignoreUpdatedAt); diff != "" {
				t.Errorf("Get() mismatch (-want +got):\n%s", diff)
			}
			if got.UpdatedAt.Before(before) {
				t.Errorf("UpdatedAt = %v, want after %v", got.UpdatedAt, before)
			}
		})
	}
}

func TestTxRepository_SetOverwritesAndRefreshesTimestamp(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository(t)

	if err := setInTx(ctx, repo, &storage.PauseSchedulingState{SchedulingPaused: false, ResumptionRunID: "run-1"}); err != nil {
		t.Fatalf("Set() unexpected error: %v", err)
	}
	first, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}

	want := &storage.PauseSchedulingState{SchedulingPaused: true}
	if err := setInTx(ctx, repo, want); err != nil {
		t.Fatalf("Set() unexpected error: %v", err)
	}
	second, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}

	if diff := cmp.Diff(want, second, ignoreUpdatedAt); diff != "" {
		t.Errorf("Get() after overwrite mismatch (-want +got):\n%s", diff)
	}
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want after %v", second.UpdatedAt, first.UpdatedAt)
	}
}

func TestTxRepository_GetAndSet(t *testing.T) {
	ctx := context.Background()
	errAbort := errors.New("abort")
	state := &storage.PauseSchedulingState{SchedulingPaused: false, ResumptionRunID: "run-1", ResumptionWorkerID: "worker-1"}

	tests := []struct {
		name    string
		state   *storage.PauseSchedulingState
		fnErr   error
		wantErr error
		want    *storage.PauseSchedulingState
	}{
		{name: "committed", state: state, want: state},
		{name: "rolled_back", state: state, fnErr: errAbort, wantErr: errAbort, want: storage.DefaultPauseSchedulingState()},
		{name: "nil_state", state: nil, wantErr: grpcerr.ErrInvalidArgument, want: storage.DefaultPauseSchedulingState()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newTestRepository(t)
			_, err := repo.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
				txRepo := repo.WithTx(tx)
				if _, err := txRepo.Get(ctx); err != nil {
					return err
				}
				if err := txRepo.Set(ctx, tt.state); err != nil {
					return err
				}
				return tt.fnErr
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ReadWriteTransaction() error = %v, want %v", err, tt.wantErr)
			}

			got, err := repo.Get(ctx)
			if err != nil {
				t.Fatalf("Get() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, ignoreUpdatedAt); diff != "" {
				t.Errorf("Get() after transaction mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
