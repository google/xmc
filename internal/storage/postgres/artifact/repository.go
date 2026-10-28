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

// Package artifact contains the concrete PostgreSQL repository implementation for artifacts.
package artifact

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
	_ storage.ArtifactStore        = (*Repository)(nil)
	_ storage.ArtifactTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for artifact entities in PostgreSQL.
type Repository struct {
	db pgdb.DBTX
}

// TxRepository manages artifact entities within a bound read-write transaction.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL Artifact repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool)}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{tx: tx}
}

// FetchArtifact retrieves an artifact by its unique ID within the transaction.
func (r *TxRepository) FetchArtifact(ctx context.Context, aid int64) (*essmsgpb.Artifact, error) {
	return nil, status.Error(codes.Unimplemented, "postgres artifact store: FetchArtifact is not implemented")
}

// UpdateArtifact updates an artifact with the given update mask within the transaction.
func (r *TxRepository) UpdateArtifact(ctx context.Context, update *storage.ArtifactUpdate) (storage.UpdatedArtifacts, error) {
	return storage.UpdatedArtifacts{}, status.Error(codes.Unimplemented, "postgres artifact store: UpdateArtifact is not implemented")
}

// CreateArtifact creates a new artifact.
func (r *Repository) CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error) {
	return nil, status.Error(codes.Unimplemented, "postgres artifact store: CreateArtifact is not implemented")
}

// FetchArtifact retrieves an artifact by its unique ID.
func (r *Repository) FetchArtifact(ctx context.Context, aid int64) (*essmsgpb.Artifact, error) {
	return nil, status.Error(codes.Unimplemented, "postgres artifact store: FetchArtifact is not implemented")
}

// ListArtifacts retrieves a list of artifacts matching the given filter.
func (r *Repository) ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Artifact, string, error) {
	return nil, "", status.Error(codes.Unimplemented, "postgres artifact store: ListArtifacts is not implemented")
}

// UpdateArtifact updates an artifact with the given update mask.
func (r *Repository) UpdateArtifact(ctx context.Context, update *storage.ArtifactUpdate) (storage.UpdatedArtifacts, error) {
	return storage.UpdatedArtifacts{}, status.Error(codes.Unimplemented, "postgres artifact store: UpdateArtifact is not implemented")
}

// DeleteArtifact deletes an artifact by its unique ID.
func (r *Repository) DeleteArtifact(ctx context.Context, aid int64) error {
	return status.Error(codes.Unimplemented, "postgres artifact store: DeleteArtifact is not implemented")
}
