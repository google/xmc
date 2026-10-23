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

// Package component provides the Cloud Spanner implementation of store.ComponentRepository: the
// Components, ComponentDependencies, ExperimentComponentSnapshots and ComponentOwnershipFence
// queries and mutations behind the CMS component store. It holds no catalog rules.
package component

import (
	"context"

	"cloud.google.com/go/spanner"

	"xmanager_cloud/pkg/components/store"
)

// Ensure the Spanner types implement the persistence contract at compile time.
var (
	_ store.ComponentRepository = (*Repository)(nil)
	_ store.ComponentReader     = (*reader)(nil)
)

// spannerQueryable is implemented by every Spanner transaction type that can run a query.
type spannerQueryable interface {
	Query(ctx context.Context, statement spanner.Statement) *spanner.RowIterator
}

// Repository is the Cloud Spanner backed store.ComponentRepository.
type Repository struct {
	client *spanner.Client
}

// NewRepository returns a Repository that reads and writes through client.
func NewRepository(client *spanner.Client) *Repository {
	return &Repository{client: client}
}

// ReadSnapshot runs fn against one Spanner read-only transaction.
func (r *Repository) ReadSnapshot(ctx context.Context, fn func(ctx context.Context, rd store.ComponentReader) error) error {
	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()
	return fn(ctx, &reader{q: txn})
}

// reader runs the component reads against any Spanner transaction.
type reader struct {
	q spannerQueryable
}

// NewReader returns the component reads over q, a transaction owned by the caller. It is exported
// only while SpannerStore still runs its own transactions; it goes away once they move to dbstore.
func NewReader(q spannerQueryable) store.ComponentReader {
	return &reader{q: q}
}
