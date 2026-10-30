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

package component

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/pkg/components/store"
)

func TestTxRepository_WritePreconditionsAndValidation(t *testing.T) {
	ctx := context.Background()
	nilTxRepo := NewRepository(nil).WithTx(nil)
	rwRepo := NewRepository(nil).WithTx(&pgxpool.Pool{})
	validRec := &store.ComponentRecord{ComponentID: 1, Name: "app"}

	t.Run("nil transaction is FailedPrecondition", func(t *testing.T) {
		calls := map[string]func() error{
			"RefreshFence":               func() error { return nilTxRepo.RefreshFence(ctx, nil) },
			"ReplaceFence":               func() error { return nilTxRepo.ReplaceFence(ctx, store.ClusterIdentity{}) },
			"ReassignOwnedComponents":    func() error { _, err := nilTxRepo.ReassignOwnedComponents(ctx, "uid", 1); return err },
			"InsertComponent":            func() error { return nilTxRepo.InsertComponent(ctx, validRec, nil) },
			"UpdateComponentIntent":      func() error { return nilTxRepo.UpdateComponentIntent(ctx, validRec, nil) },
			"UpdateComponentState":       func() error { return nilTxRepo.UpdateComponentState(ctx, 1, 0, nil) },
			"UpdateComponentObservation": func() error { return nilTxRepo.UpdateComponentObservation(ctx, validRec, false) },
			"SoftDeleteComponent":        func() error { return nilTxRepo.SoftDeleteComponent(ctx, validRec, nil) },
			"WriteComponentOwner":        func() error { return nilTxRepo.WriteComponentOwner(ctx, 1, nil) },
		}
		for name, call := range calls {
			if err := call(); !errors.Is(err, grpcerr.ErrFailedPrecondition) || status.Code(err) != codes.FailedPrecondition {
				t.Errorf("%s error = %v, want %v", name, err, grpcerr.ErrFailedPrecondition)
			}
		}
	})

	t.Run("nil record is InvalidArgument", func(t *testing.T) {
		calls := map[string]func() error{
			"InsertComponent":            func() error { return rwRepo.InsertComponent(ctx, nil, nil) },
			"UpdateComponentIntent":      func() error { return rwRepo.UpdateComponentIntent(ctx, nil, nil) },
			"UpdateComponentObservation": func() error { return rwRepo.UpdateComponentObservation(ctx, nil, false) },
			"SoftDeleteComponent":        func() error { return rwRepo.SoftDeleteComponent(ctx, nil, nil) },
		}
		for name, call := range calls {
			if err := call(); !errors.Is(err, grpcerr.ErrInvalidArgument) || status.Code(err) != codes.InvalidArgument {
				t.Errorf("%s(nil) error = %v, want %v", name, err, grpcerr.ErrInvalidArgument)
			}
		}
	})

	t.Run("missing dependency ID is FailedPrecondition", func(t *testing.T) {
		recWithDep := &store.ComponentRecord{ComponentID: 1, Name: "app", Dependencies: []string{"db"}}
		for name, call := range map[string]func() error{
			"InsertComponent":       func() error { return rwRepo.InsertComponent(ctx, recWithDep, map[string]int64{}) },
			"UpdateComponentIntent": func() error { return rwRepo.UpdateComponentIntent(ctx, recWithDep, nil) },
		} {
			if err := call(); !errors.Is(err, grpcerr.ErrFailedPrecondition) || status.Code(err) != codes.FailedPrecondition {
				t.Errorf("%s(missing dep) error = %v, want %v", name, err, grpcerr.ErrFailedPrecondition)
			}
		}
	})
}

func TestRepository_NilPoolOrTransaction(t *testing.T) {
	ctx := context.Background()
	nilRepo := NewRepository(nil)
	nilTxRepo := nilRepo.WithTx(nil)
	now := time.Now()

	if err := nilRepo.WithReadOnlySnapshot(ctx, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("WithReadOnlySnapshot(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if err := nilRepo.WithReadOnlySnapshot(ctx, func(context.Context, storage.ComponentTxRepository) error { return nil }); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("WithReadOnlySnapshot(fn) error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}

	calls := map[string]func() error{
		"Repository.GetActiveComponent":    func() error { _, err := nilRepo.GetActiveComponent(ctx, "app"); return err },
		"Repository.GetComponentByID":      func() error { _, err := nilRepo.GetComponentByID(ctx, 1); return err },
		"Repository.ListActiveComponents":  func() error { _, err := nilRepo.ListActiveComponents(ctx); return err },
		"Repository.GetComponentWithFence": func() error { _, _, err := nilRepo.GetComponentWithFence(ctx, "app"); return err },
		"Repository.ReadOwnershipGauges":   func() error { _, err := nilRepo.ReadOwnershipGauges(ctx, now); return err },
		"TxRepository.GetActiveComponent":  func() error { _, err := nilTxRepo.GetActiveComponent(ctx, "app"); return err },
		"TxRepository.GetComponentByID":    func() error { _, err := nilTxRepo.GetComponentByID(ctx, 1); return err },
		"TxRepository.ListActiveComponents": func() error {
			_, err := nilTxRepo.ListActiveComponents(ctx)
			return err
		},
		"TxRepository.GetSnapshotByID":   func() error { _, _, err := nilTxRepo.GetSnapshotByID(ctx, 1, 1, "app", now, now); return err },
		"TxRepository.GetSnapshotByName": func() error { _, _, err := nilTxRepo.GetSnapshotByName(ctx, "app", 1); return err },
		"TxRepository.ReadFence":         func() error { _, err := nilTxRepo.ReadFence(ctx); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
			t.Errorf("%s error = %v, want %v", name, err, grpcerr.ErrFailedPrecondition)
		}
	}
}

func TestNewRepository_BindsPoolAndTx(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool || repo.pool != pool {
		t.Errorf("NewRepository(pool) = {db: %v, pool: %p}, want both %p", repo.db, repo.pool, pool)
	}

	var tx pgdb.DBTX = &pgxpool.Pool{}
	txRepo := repo.WithTx(tx)
	if txRepo.db != tx {
		t.Errorf("WithTx(tx).db = %v, want %v", txRepo.db, tx)
	}
}
