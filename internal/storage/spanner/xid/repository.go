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

// Package xid contains the concrete Spanner repository implementation for XID entries.
package xid

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"

	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner/schema/xidschema"
)

var (
	_ storage.XidEntryStore   = (*Repository)(nil)
	_ storage.XidTxRepository = (*TxRepository)(nil)
)

type spannerTransaction interface {
	ReadRow(ctx context.Context, table string, key spanner.Key, columns []string) (*spanner.Row, error)
	Query(ctx context.Context, stmt spanner.Statement) *spanner.RowIterator
}

// Repository manages persistence for XID entry entities in Cloud Spanner.
type Repository struct {
	client       *spanner.Client
	xidsQueryGen querytools.QueryGenerator
}

// TxRepository wraps Repository to execute operations within an active Spanner ReadWriteTransaction.
type TxRepository struct {
	*Repository
	tx *spanner.ReadWriteTransaction
}

// WithTx returns a TxRepository bound to the provided Spanner ReadWriteTransaction.
func (r *Repository) WithTx(tx *spanner.ReadWriteTransaction) *TxRepository {
	return &TxRepository{
		Repository: r,
		tx:         tx,
	}
}

// NewRepository creates a new Xid repository instance with initialized query generator.
func NewRepository(client *spanner.Client) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil")
	}

	xidsQueryGen, err := querytools.NewQueryGenerator(
		xidschema.XidTables,
		nil,
		nil,
		nil,
		querytools.RestrictionRewriter(RewriteXidRestriction),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Xids query generator: %w", err)
	}

	return &Repository{
		client:       client,
		xidsQueryGen: xidsQueryGen,
	}, nil
}
