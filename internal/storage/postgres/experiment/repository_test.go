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

package experiment

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
			name: "FetchExperiment",
			call: func() error {
				_, err := repo.FetchExperiment(ctx, 1)
				return err
			},
		},
		{
			name: "FetchExperimentStatus",
			call: func() error {
				_, err := repo.FetchExperimentStatus(ctx, 1)
				return err
			},
		},
		{
			name: "UpdateExperiment",
			call: func() error {
				_, err := repo.UpdateExperiment(ctx, &storage.ExperimentUpdate{})
				return err
			},
		},
		{
			name: "ListExperiments",
			call: func() error {
				_, _, err := repo.ListExperiments(ctx, 10, "", "", "", 0)
				return err
			},
		},
		{
			name: "FetchDriftedComponents",
			call: func() error {
				_, err := repo.FetchDriftedComponents(ctx, 1, 10)
				return err
			},
		},
		{
			name: "Tx_FetchExperiment",
			call: func() error {
				_, err := txRepo.FetchExperiment(ctx, 1)
				return err
			},
		},
		{
			name: "Tx_InsertExperiment",
			call: func() error {
				return txRepo.InsertExperiment(ctx, nil)
			},
		},
		{
			name: "Tx_UpdateExperimentStatus",
			call: func() error {
				return txRepo.UpdateExperimentStatus(ctx, nil)
			},
		},
		{
			name: "Tx_UpdateExperimentResources",
			call: func() error {
				return txRepo.UpdateExperimentResources(ctx, nil)
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

	// Any DBTX implementation can stand in for the transaction handle.
	var tx pgdb.DBTX = &pgxpool.Pool{}
	txRepo := repo.WithTx(tx)
	if txRepo.tx != tx {
		t.Errorf("WithTx(tx).tx = %v, want %v", txRepo.tx, tx)
	}
}
