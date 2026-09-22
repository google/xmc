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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner/experiment"
	"xmanager_cloud/internal/storage/spanner/transaction"
	"xmanager_cloud/query_generator/querytools"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/workunitschema"
)

type spannerTransaction interface {
	ReadRow(ctx context.Context, table string, key spanner.Key, columns []string) (*spanner.Row, error)
	Query(ctx context.Context, stmt spanner.Statement) *spanner.RowIterator
}

// ExperimentTxStore defines transactional experiment operations required by WorkUnit repository.
type ExperimentTxStore interface {
	FetchExperimentTransaction(ctx context.Context, xid int64, tx experiment.SpannerTransaction) (*essmsgpb.Experiment, error)
	UpdateExperimentStatus(ctx context.Context, tx *spanner.ReadWriteTransaction, exp *essmsgpb.Experiment) error
	UpdateExperimentResources(ctx context.Context, tx *spanner.ReadWriteTransaction, exp *essmsgpb.Experiment) error
}

var _ storage.WorkUnitStore = (*Repository)(nil)

// Repository manages persistence for work unit entities in Cloud Spanner.
type Repository struct {
	client            *spanner.Client
	experimentRepo    ExperimentTxStore
	workUnitsQueryGen querytools.QueryGenerator
}

// NewRepository creates a new WorkUnit repository instance with initialized query generator.
func NewRepository(client *spanner.Client, expRepo ExperimentTxStore) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil")
	}

	workUnitsQueryGen, err := querytools.NewQueryGenerator(workunitschema.WorkUnitTables, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create work units query generator: %w", err)
	}

	return &Repository{
		client:            client,
		experimentRepo:    expRepo,
		workUnitsQueryGen: workUnitsQueryGen,
	}, nil
}

// SaveWorkUnitExecution saves a work unit execution using the active read-write transaction in context.
func (r *Repository) SaveWorkUnitExecution(ctx context.Context, execution *essmsgpb.WorkUnitExecution) error {
	if execution == nil {
		return fmt.Errorf("execution must not be nil: %w", status.ErrInvalidArgument)
	}
	tx, err := transaction.RequireReadWriteTransaction(ctx)
	if err != nil {
		return err
	}
	if execution.Metadata == nil {
		execution.Metadata = &essmsgpb.WorkUnitExecutionMetadata{}
	}
	if execution.Status == nil {
		execution.Status = &essmsgpb.WorkUnitStatus{}
	}
	if execution.CreateTime == nil {
		execution.CreateTime = timestamppb.Now()
	}
	if execution.UpdateTime == nil {
		execution.UpdateTime = execution.CreateTime
	}
	mutation, err := spannerdata.WorkUnitExecutionProtoToInsertMutation(execution)
	if err != nil {
		return fmt.Errorf("failed to create mutation for work unit execution: %v: %w", err, status.ErrInternal)
	}
	return tx.BufferWrite([]*spanner.Mutation{mutation})
}

// UpdateCurrentWorkUnitExecution updates the current work unit execution using the active read-write transaction in context.
func (r *Repository) UpdateCurrentWorkUnitExecution(ctx context.Context, xid, wid, eid int64, execution *essmsgpb.WorkUnitExecution) error {
	if execution == nil {
		return fmt.Errorf("execution must not be nil: %w", status.ErrInvalidArgument)
	}
	tx, err := transaction.RequireReadWriteTransaction(ctx)
	if err != nil {
		return err
	}
	if execution.Metadata == nil {
		execution.Metadata = &essmsgpb.WorkUnitExecutionMetadata{}
	}
	if execution.Status == nil {
		execution.Status = &essmsgpb.WorkUnitStatus{}
	}
	if execution.CreateTime == nil {
		execution.CreateTime = timestamppb.Now()
	}
	if execution.UpdateTime == nil {
		execution.UpdateTime = execution.CreateTime
	}
	mutation := spannerdata.CurrentWorkUnitExecutionProtoToUpdateMutation(xid, wid, eid, execution)
	return tx.BufferWrite([]*spanner.Mutation{mutation})
}
