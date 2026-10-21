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

// Package statusmessage contains the concrete PostgreSQL repository implementation for status messages.
package statusmessage

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
	_ storage.StatusMessageStore        = (*Repository)(nil)
	_ storage.StatusMessageTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for status message entities in PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
	db   pgdb.DBTX
}

// TxRepository manages transactional persistence for status message entities in PostgreSQL.
type TxRepository struct {
	*Repository
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL StatusMessage repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, db: pgdb.FromPool(pool)}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{Repository: r, tx: tx}
}

// ListStatusMessages retrieves a list of status messages for an experiment or work unit.
func (r *Repository) ListStatusMessages(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.StatusMessage, string, error) {
	return nil, "", status.Error(codes.Unimplemented, "postgres statusmessage store: ListStatusMessages is not implemented")
}

// SaveStatusMessageHistory records a status message history entry within the transaction.
func (r *TxRepository) SaveStatusMessageHistory(ctx context.Context, eid *int64, oldMsg, newMsg *essmsgpb.StructuredMessage) error {
	return status.Error(codes.Unimplemented, "postgres statusmessage tx repository: SaveStatusMessageHistory is not implemented")
}
