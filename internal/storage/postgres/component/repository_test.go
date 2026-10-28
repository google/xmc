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
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/pkg/components/store"
)

func TestRepository_Unimplemented(t *testing.T) {
	repo := NewRepository(nil)
	txRepo := repo.WithTx(nil)

	ctx := context.Background()
	now := time.Now()
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "Repository.GetActiveComponent",
			call: func() error {
				_, err := repo.GetActiveComponent(ctx, "app")
				return err
			},
		},
		{
			name: "Repository.GetComponentByID",
			call: func() error {
				_, err := repo.GetComponentByID(ctx, 1)
				return err
			},
		},
		{
			name: "Repository.ListActiveComponents",
			call: func() error {
				_, err := repo.ListActiveComponents(ctx)
				return err
			},
		},
		{
			name: "Repository.GetComponentWithFence",
			call: func() error {
				_, _, err := repo.GetComponentWithFence(ctx, "app")
				return err
			},
		},
		{
			name: "TxRepository.GetSnapshotByID",
			call: func() error {
				_, _, err := txRepo.GetSnapshotByID(ctx, 1, 1, "app", now, now)
				return err
			},
		},
		{
			name: "TxRepository.GetSnapshotByName",
			call: func() error {
				_, _, err := txRepo.GetSnapshotByName(ctx, "app", 1)
				return err
			},
		},
		{
			name: "Repository.ReadOwnershipGauges",
			call: func() error {
				_, err := repo.ReadOwnershipGauges(ctx, now)
				return err
			},
		},
		{
			name: "TxRepository.ReadFence",
			call: func() error {
				_, err := txRepo.ReadFence(ctx)
				return err
			},
		},
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

func TestNewRepository_BindsPoolAndTx(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}

	var tx pgdb.DBTX = &pgxpool.Pool{}
	txRepo := repo.WithTx(tx)
	if txRepo.db != tx {
		t.Errorf("WithTx(tx).db = %v, want %v", txRepo.db, tx)
	}
}
