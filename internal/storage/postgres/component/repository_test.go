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

func TestTxRepository_UnimplementedWrites(t *testing.T) {
	txRepo := NewRepository(nil).WithTx(nil)

	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "TxRepository.RefreshFence",
			call: func() error {
				return txRepo.RefreshFence(ctx, nil)
			},
		},
		{
			name: "TxRepository.ReplaceFence",
			call: func() error {
				return txRepo.ReplaceFence(ctx, store.ClusterIdentity{})
			},
		},
		{
			name: "TxRepository.ReassignOwnedComponents",
			call: func() error {
				_, err := txRepo.ReassignOwnedComponents(ctx, "uid", 1)
				return err
			},
		},
		{
			name: "TxRepository.InsertComponent",
			call: func() error {
				return txRepo.InsertComponent(ctx, nil, nil)
			},
		},
		{
			name: "TxRepository.UpdateComponentIntent",
			call: func() error {
				return txRepo.UpdateComponentIntent(ctx, nil, nil)
			},
		},
		{
			name: "TxRepository.UpdateComponentState",
			call: func() error {
				return txRepo.UpdateComponentState(ctx, 1, 0, nil)
			},
		},
		{
			name: "TxRepository.UpdateComponentObservation",
			call: func() error {
				return txRepo.UpdateComponentObservation(ctx, nil, false)
			},
		},
		{
			name: "TxRepository.SoftDeleteComponent",
			call: func() error {
				return txRepo.SoftDeleteComponent(ctx, nil, nil)
			},
		},
		{
			name: "TxRepository.WriteComponentOwner",
			call: func() error {
				return txRepo.WriteComponentOwner(ctx, 1, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if got := status.Code(err); got != codes.Unimplemented {
				t.Errorf("%s code = %v, want %v (err: %v)", tt.name, got, codes.Unimplemented, err)
			}
		})
	}
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
