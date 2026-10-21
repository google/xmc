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

// Package operation contains the concrete PostgreSQL repository implementation for operations.
package operation

import (
	"context"

	longrunningpb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

var _ storage.OperationStore = (*Repository)(nil)

// Repository manages persistence for operation entities in PostgreSQL.
type Repository struct {
	db pgdb.DBTX
}

// NewRepository creates a new PostgreSQL Operation repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool)}
}

// AddWorkUnitOperation adds an operation to a work unit.
func (r *Repository) AddWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationMetadata *essmsgpb.OperationMetadata) (*longrunningpb.Operation, error) {
	return nil, status.Error(codes.Unimplemented, "postgres operation store: AddWorkUnitOperation is not implemented")
}

// FetchWorkUnitOperation retrieves an operation by its unique ID.
func (r *Repository) FetchWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationID string) (*longrunningpb.Operation, error) {
	return nil, status.Error(codes.Unimplemented, "postgres operation store: FetchWorkUnitOperation is not implemented")
}

// UpdateWorkUnitOperation updates the status of an operation.
func (r *Repository) UpdateWorkUnitOperation(ctx context.Context, operationUpdate *storage.OperationUpdate) (*longrunningpb.Operation, error) {
	return nil, status.Error(codes.Unimplemented, "postgres operation store: UpdateWorkUnitOperation is not implemented")
}
