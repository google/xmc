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

// Package workunit contains the concrete PostgreSQL repository implementation for work units and work unit executions.
package workunit

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
)

var (
	_ storage.WorkUnitStore        = (*Repository)(nil)
	_ storage.WorkUnitTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for work unit entities in PostgreSQL.
type Repository struct{}

// TxRepository manages transactional persistence for work unit entities in PostgreSQL.
type TxRepository struct {
	*Repository
}

// NewRepository creates a new PostgreSQL WorkUnit repository instance.
func NewRepository() *Repository {
	return &Repository{}
}

// WithTx returns a wrapper bound to a read-write transaction.
func (r *Repository) WithTx() *TxRepository {
	return &TxRepository{Repository: r}
}

// FetchWorkUnit retrieves a work unit by its unique ID.
func (r *Repository) FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error) {
	return nil, status.Error(codes.Unimplemented, "postgres workunit store: FetchWorkUnit is not implemented")
}

// ListWorkUnits retrieves a list of work units matching the given filter.
func (r *Repository) ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error) {
	return nil, "", status.Error(codes.Unimplemented, "postgres workunit store: ListWorkUnits is not implemented")
}

// UpdateWorkUnit updates a work unit with the given update mask.
func (r *Repository) UpdateWorkUnit(ctx context.Context, update *storage.WorkUnitUpdate) (storage.UpdatedWorkUnits, error) {
	return storage.UpdatedWorkUnits{}, status.Error(codes.Unimplemented, "postgres workunit store: UpdateWorkUnit is not implemented")
}

// FetchCurrentWorkUnitExecution retrieves the current work unit execution.
func (r *Repository) FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error) {
	return nil, status.Error(codes.Unimplemented, "postgres workunit store: FetchCurrentWorkUnitExecution is not implemented")
}

// ListWorkUnitExecutions retrieves the historical work unit executions for a given work unit.
func (r *Repository) ListWorkUnitExecutions(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error) {
	return nil, status.Error(codes.Unimplemented, "postgres workunit store: ListWorkUnitExecutions is not implemented")
}

// FetchActiveWorkUnitsByQueues retrieves active work units associated with the requested queue names.
func (r *Repository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) (*storage.ActiveWorkUnitsResult, error) {
	return nil, status.Error(codes.Unimplemented, "postgres workunit store: FetchActiveWorkUnitsByQueues is not implemented")
}

// FetchNextWorkUnitID retrieves the next available work unit ID for an experiment within the transaction.
func (r *TxRepository) FetchNextWorkUnitID(ctx context.Context, xid int64) (int64, error) {
	return 0, status.Error(codes.Unimplemented, "postgres workunit tx repository: FetchNextWorkUnitID is not implemented")
}

// InsertWorkUnit inserts a new work unit and its initial execution within the transaction.
func (r *TxRepository) InsertWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) error {
	return status.Error(codes.Unimplemented, "postgres workunit tx repository: InsertWorkUnit is not implemented")
}

// FetchWorkUnit retrieves a work unit by its unique ID within the transaction.
func (r *TxRepository) FetchWorkUnit(ctx context.Context, xid, wid int64) (*essmsgpb.WorkUnit, error) {
	return nil, status.Error(codes.Unimplemented, "postgres workunit tx repository: FetchWorkUnit is not implemented")
}

// FetchUpdatingWorkUnits retrieves a batch of work units for update by their unique IDs within the transaction.
func (r *TxRepository) FetchUpdatingWorkUnits(ctx context.Context, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnit, error) {
	return nil, status.Error(codes.Unimplemented, "postgres workunit tx repository: FetchUpdatingWorkUnits is not implemented")
}

// SaveWorkUnitExecution saves a work unit execution within the transaction.
func (r *TxRepository) SaveWorkUnitExecution(ctx context.Context, execution *essmsgpb.WorkUnitExecution) error {
	return status.Error(codes.Unimplemented, "postgres workunit tx repository: SaveWorkUnitExecution is not implemented")
}

// UpdateCurrentWorkUnitExecution updates the current work unit execution within the transaction.
func (r *TxRepository) UpdateCurrentWorkUnitExecution(ctx context.Context, xid, wid, eid int64, execution *essmsgpb.WorkUnitExecution) error {
	return status.Error(codes.Unimplemented, "postgres workunit tx repository: UpdateCurrentWorkUnitExecution is not implemented")
}

// ListWorkUnitMessagesBySeverity retrieves work unit status messages matching a given severity level within the transaction.
func (r *TxRepository) ListWorkUnitMessagesBySeverity(ctx context.Context, xid int64, severity essmsgpb.StructuredMessage_Severity) ([]*essmsgpb.StructuredMessage, error) {
	return nil, status.Error(codes.Unimplemented, "postgres workunit tx repository: ListWorkUnitMessagesBySeverity is not implemented")
}
