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

// Package component contains the PostgreSQL repository implementation for CMS component catalog records.
package component

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/pkg/components/store"
)

var _ storage.ComponentRepository = (*Repository)(nil)

// Repository manages persistence for component catalog records in PostgreSQL.
type Repository struct {
	db pgdb.DBTX
}

// NewRepository creates a new PostgreSQL Component repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool)}
}

// WithTx returns a repository bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *Repository {
	return &Repository{db: tx}
}

// GetActiveComponent retrieves a non-deleted component record by canonical name.
func (r *Repository) GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: GetActiveComponent is not implemented")
}

// GetComponentByID retrieves a non-deleted component record by its unique database identifier.
func (r *Repository) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: GetComponentByID is not implemented")
}
