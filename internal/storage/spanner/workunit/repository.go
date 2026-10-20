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

// Package workunit contains the concrete Spanner repository implementation for work units and work unit executions.
package workunit

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/storage"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/schema/workunitschema"
)

type spannerTransaction interface {
	ReadRow(ctx context.Context, table string, key spanner.Key, columns []string) (*spanner.Row, error)
	Query(ctx context.Context, stmt spanner.Statement) *spanner.RowIterator
}

var (
	_ storage.WorkUnitStore        = (*Repository)(nil)
	_ storage.WorkUnitQueueFetcher = (*Repository)(nil)
	_ storage.WorkUnitTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for work unit entities in Cloud Spanner.
type Repository struct {
	client            *spanner.Client
	workUnitsQueryGen querytools.QueryGenerator
}

// TxRepository manages transactional persistence for work unit entities in Cloud Spanner.
type TxRepository struct {
	*Repository
	tx *spanner.ReadWriteTransaction
}

// NewRepository creates a new WorkUnit repository instance with initialized query generator.
func NewRepository(client *spanner.Client) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil")
	}

	workUnitsQueryGen, err := querytools.NewQueryGenerator(workunitschema.WorkUnitTables, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create WorkUnits query generator: %w", err)
	}

	return &Repository{
		client:            client,
		workUnitsQueryGen: workUnitsQueryGen,
	}, nil
}

// WithTx returns a wrapper bound to the given read-write transaction.
func (r *Repository) WithTx(tx *spanner.ReadWriteTransaction) *TxRepository {
	return &TxRepository{
		Repository: r,
		tx:         tx,
	}
}

// SaveWorkUnitExecution saves a work unit execution using the bound read-write transaction.
func (r *TxRepository) SaveWorkUnitExecution(ctx context.Context, execution *essmsgpb.WorkUnitExecution) error {
	if execution == nil {
		return fmt.Errorf("execution must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	mutation, err := spannerdata.WorkUnitExecutionProtoToInsertMutation(execution)
	if err != nil {
		return fmt.Errorf("failed to create mutation for work unit execution: %v: %w", err, grpcerr.ErrInternal)
	}
	return r.tx.BufferWrite([]*spanner.Mutation{mutation})
}

// UpdateCurrentWorkUnitExecution updates the current work unit execution using the bound read-write transaction.
func (r *TxRepository) UpdateCurrentWorkUnitExecution(ctx context.Context, xid, wid, eid int64, execution *essmsgpb.WorkUnitExecution) error {
	if execution == nil {
		return fmt.Errorf("execution must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	mutation := spannerdata.CurrentWorkUnitExecutionProtoToUpdateMutation(xid, wid, eid, execution)
	return r.tx.BufferWrite([]*spanner.Mutation{mutation})
}
