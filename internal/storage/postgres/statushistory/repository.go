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

// Package statushistory contains the concrete PostgreSQL repository implementation for status history and velocity metrics.
package statushistory

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

var (
	_ storage.StatusHistoryStore        = (*Repository)(nil)
	_ storage.StatusHistoryTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for status history and velocity metrics in PostgreSQL.
type Repository struct {
	db pgdb.DBTX
}

// TxRepository manages transactional persistence for status history in PostgreSQL.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL StatusHistory repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool)}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{tx: tx}
}

// FetchBatchQueueVelocity queries the status history table for multiple queues and returns raw metric rows.
func (r *Repository) FetchBatchQueueVelocity(ctx context.Context, queueNames []string, window time.Duration) ([]storage.QueueMetricRow, error) {
	return nil, status.Error(codes.Unimplemented, "postgres statushistory store: FetchBatchQueueVelocity is not implemented")
}

// FetchBatchPendingQueueMetrics queries the status history table for pending work units across multiple queues.
func (r *Repository) FetchBatchPendingQueueMetrics(ctx context.Context, queueNames []string) ([]storage.QueueMetricRow, error) {
	return nil, status.Error(codes.Unimplemented, "postgres statushistory store: FetchBatchPendingQueueMetrics is not implemented")
}

// SaveWorkUnitStatusHistory records work unit status history entries within the transaction.
func (r *TxRepository) SaveWorkUnitStatusHistory(ctx context.Context, xid, wid int64, eid *int64, queueName string, prevState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resources map[string]int64, timestamp time.Time) error {
	return status.Error(codes.Unimplemented, "postgres statushistory tx repository: SaveWorkUnitStatusHistory is not implemented")
}
