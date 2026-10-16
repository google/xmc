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

// Package experiment contains the concrete Spanner repository implementation for experiments.
package experiment

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"

	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner/schema/experimentschema"
)

var _ storage.ExperimentStore = (*Repository)(nil)

// SpannerTransaction defines single-row read and query capabilities within a transaction.
type SpannerTransaction interface {
	ReadRow(ctx context.Context, table string, key spanner.Key, columns []string) (*spanner.Row, error)
	Query(ctx context.Context, stmt spanner.Statement) *spanner.RowIterator
}

// Repository manages persistence for experiment entities in Cloud Spanner.
type Repository struct {
	client              *spanner.Client
	experimentsQueryGen querytools.QueryGenerator
}

// TxRepository manages transactional persistence for experiment entities in Cloud Spanner.
type TxRepository struct {
	*Repository
	tx *spanner.ReadWriteTransaction
}

// NewRepository creates a new Experiment repository instance with initialized query generator.
func NewRepository(client *spanner.Client) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil")
	}

	experimentsQueryGen, err := querytools.NewQueryGenerator(experimentschema.ExperimentTables, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create experiments query generator: %w", err)
	}

	return &Repository{
		client:              client,
		experimentsQueryGen: experimentsQueryGen,
	}, nil
}

// WithTx returns a wrapper bound to the given read-write transaction.
func (r *Repository) WithTx(tx *spanner.ReadWriteTransaction) *TxRepository {
	return &TxRepository{
		Repository: r,
		tx:         tx,
	}
}
