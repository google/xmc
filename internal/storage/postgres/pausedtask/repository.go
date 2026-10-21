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

// Package pausedtask provides the PostgreSQL implementation for persisting paused actuation tasks.
package pausedtask

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

var (
	_ storage.PausedTaskRepository   = (*Repository)(nil)
	_ storage.PausedTaskTxRepository = (*TxRepository)(nil)
)

// Repository persists paused tasks in PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
	db   pgdb.DBTX
}

// TxRepository persists paused task writes within a bound read-write transaction.
type TxRepository struct {
	*Repository
	tx pgdb.DBTX
}

// NewRepository creates a PostgreSQL-backed paused task Repository bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, db: pgdb.FromPool(pool)}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{Repository: r, tx: tx}
}

// ListPending retrieves pending paused tasks ordered by Priority DESC, CreateTime ASC.
func (r *Repository) ListPending(ctx context.Context, limit int) ([]*storage.PausedTask, error) {
	return nil, status.Error(codes.Unimplemented, "postgres pausedtask repository: ListPending is not implemented")
}

// Create persists a new paused task within the transaction.
func (r *TxRepository) Create(ctx context.Context, task *storage.PausedTask) error {
	return status.Error(codes.Unimplemented, "postgres pausedtask tx repository: Create is not implemented")
}

// MarkProcessed records resolution details for a batch of paused tasks within the transaction.
func (r *TxRepository) MarkProcessed(ctx context.Context, resolutions []storage.PausedTaskResolution) error {
	return status.Error(codes.Unimplemented, "postgres pausedtask tx repository: MarkProcessed is not implemented")
}
