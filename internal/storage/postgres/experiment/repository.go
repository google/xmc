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

// Package experiment contains the concrete PostgreSQL repository implementation for experiments.
package experiment

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
	_ storage.ExperimentStore        = (*Repository)(nil)
	_ storage.ExperimentTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for experiment entities in PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
	db   pgdb.DBTX
}

// TxRepository manages transactional persistence for experiment entities in PostgreSQL.
type TxRepository struct {
	*Repository
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL Experiment repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, db: pgdb.FromPool(pool)}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{Repository: r, tx: tx}
}

// FetchExperiment retrieves an experiment by its unique ID.
func (r *Repository) FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error) {
	return nil, status.Error(codes.Unimplemented, "postgres experiment store: FetchExperiment is not implemented")
}

// FetchExperimentStatus retrieves only the status of an experiment by its unique ID.
func (r *Repository) FetchExperimentStatus(ctx context.Context, xid int64) (*essmsgpb.ExperimentStatus, error) {
	return nil, status.Error(codes.Unimplemented, "postgres experiment store: FetchExperimentStatus is not implemented")
}

// ListExperiments retrieves a list of experiments matching the given filter.
func (r *Repository) ListExperiments(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Experiment, string, error) {
	return nil, "", status.Error(codes.Unimplemented, "postgres experiment store: ListExperiments is not implemented")
}

// UpdateExperiment updates an experiment with the given update mask.
func (r *Repository) UpdateExperiment(ctx context.Context, update *storage.ExperimentUpdate) (storage.UpdatedExperiments, error) {
	return storage.UpdatedExperiments{}, status.Error(codes.Unimplemented, "postgres experiment store: UpdateExperiment is not implemented")
}

// FetchDriftedComponents retrieves components whose live catalog entry diverges from the experiment's snapshot.
func (r *Repository) FetchDriftedComponents(ctx context.Context, xid int64, limit int) ([]storage.ComponentDiff, error) {
	return nil, status.Error(codes.Unimplemented, "postgres experiment store: FetchDriftedComponents is not implemented")
}

// FetchExperiment retrieves an experiment by its unique ID within the transaction.
func (r *TxRepository) FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error) {
	return nil, status.Error(codes.Unimplemented, "postgres experiment tx repository: FetchExperiment is not implemented")
}

// InsertExperiment inserts a new experiment within the transaction.
func (r *TxRepository) InsertExperiment(ctx context.Context, exp *essmsgpb.Experiment) error {
	return status.Error(codes.Unimplemented, "postgres experiment tx repository: InsertExperiment is not implemented")
}

// UpdateExperimentStatus updates the experiment status within the transaction.
func (r *TxRepository) UpdateExperimentStatus(ctx context.Context, exp *essmsgpb.Experiment) error {
	return status.Error(codes.Unimplemented, "postgres experiment tx repository: UpdateExperimentStatus is not implemented")
}

// UpdateExperimentResources updates the experiment resources within the transaction.
func (r *TxRepository) UpdateExperimentResources(ctx context.Context, exp *essmsgpb.Experiment) error {
	return status.Error(codes.Unimplemented, "postgres experiment tx repository: UpdateExperimentResources is not implemented")
}
