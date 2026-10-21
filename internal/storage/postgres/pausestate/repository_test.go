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
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

func TestRepository_Unimplemented(t *testing.T) {
	repo := NewRepository(nil)
	if repo == nil {
		t.Fatal("NewRepository() returned nil")
	}
	txRepo := repo.WithTx(nil)
	if txRepo == nil {
		t.Fatal("WithTx() returned nil")
	}

	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "Get",
			call: func() error {
				_, err := repo.Get(ctx)
				return err
			},
		},
		{
			name: "Tx_Get",
			call: func() error {
				_, err := txRepo.Get(ctx)
				return err
			},
		},
		{
			name: "Tx_Set",
			call: func() error {
				return txRepo.Set(ctx, &storage.PauseSchedulingState{})
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
	if repo.pool != pool || repo.db != pool {
		t.Errorf("NewRepository(pool) = {pool: %p, db: %v}, want both bound to %p", repo.pool, repo.db, pool)
	}

	// Any DBTX implementation can stand in for the transaction handle.
	var tx pgdb.DBTX = &pgxpool.Pool{}
	txRepo := repo.WithTx(tx)
	if txRepo.Repository != repo || txRepo.tx != tx {
		t.Errorf("WithTx(tx) = {Repository: %p, tx: %v}, want {Repository: %p, tx: %v}", txRepo.Repository, txRepo.tx, repo, tx)
	}
}
