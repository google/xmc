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

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"xmanager_cloud/internal/storage/postgres/pgdb"
)

func TestRepository_Unimplemented(t *testing.T) {
	repo := NewRepository(nil)
	txRepo := repo.WithTx(nil)

	ctx := context.Background()
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
			name: "TxRepository.GetActiveComponent",
			call: func() error {
				_, err := txRepo.GetActiveComponent(ctx, "app")
				return err
			},
		},
		{
			name: "TxRepository.GetComponentByID",
			call: func() error {
				_, err := txRepo.GetComponentByID(ctx, 1)
				return err
			},
		},
		{
			name: "TxRepository.ListActiveComponents",
			call: func() error {
				_, err := txRepo.ListActiveComponents(ctx)
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
