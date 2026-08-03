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

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner/experiment"
	"xmanager_cloud/query_generator/querytools"
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
	client                      *spanner.Client
	experimentRepo              ExperimentTxStore
	workUnitsQueryGen           querytools.QueryGenerator
	statusMessageHistoryEnabled bool
}

// RepositoryConfig configures optional settings for Repository.
type RepositoryConfig struct {
	StatusMessageHistoryEnabled bool
}

// NewRepository creates a new WorkUnit repository instance with initialized query generator.
func NewRepository(client *spanner.Client, expRepo ExperimentTxStore, cfg RepositoryConfig) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil")
	}

	workUnitsQueryGen, err := querytools.NewQueryGenerator(workunitschema.WorkUnitTables, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create work units query generator: %w", err)
	}

	return &Repository{
		client:                      client,
		experimentRepo:              expRepo,
		workUnitsQueryGen:           workUnitsQueryGen,
		statusMessageHistoryEnabled: cfg.StatusMessageHistoryEnabled,
	}, nil
}
