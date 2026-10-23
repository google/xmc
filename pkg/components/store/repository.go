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

package store

import (
	"context"
	"errors"
)

// ErrSnapshotNotFound is returned by ComponentReader snapshot reads when no snapshot is stored for
// the requested generation. Callers translate it into the NotFound status they report.
var ErrSnapshotNotFound = errors.New("component snapshot not found")

// ComponentRepository is the persistence contract behind a database-backed Store. Implementations
// run queries and mutations only; every catalog rule stays with the caller.
type ComponentRepository interface {
	// ReadSnapshot runs fn against one consistent read-only snapshot of the catalog. Errors returned
	// by fn are returned unchanged.
	ReadSnapshot(ctx context.Context, fn func(ctx context.Context, r ComponentReader) error) error
}

// ComponentReader reads component records within a read-only snapshot or a read-write transaction.
type ComponentReader interface {
	// GetActiveComponent returns the active record of canonicalName with its active dependencies,
	// or ErrComponentNotFound.
	GetActiveComponent(ctx context.Context, canonicalName string) (*ComponentRecord, error)

	// GetComponentByID returns the non-deleted record componentID with its active dependencies, or
	// ErrComponentNotFound.
	GetComponentByID(ctx context.Context, componentID int64) (*ComponentRecord, error)

	// ListActiveComponents returns every active record, with its active dependencies, by name.
	ListActiveComponents(ctx context.Context) (map[string]*ComponentRecord, error)

	// GetSnapshot returns the stored snapshot of componentID at generation with only ComponentID,
	// Generation, Spec, Labels and Status set, or ErrSnapshotNotFound.
	GetSnapshot(ctx context.Context, componentID, generation int64) (*ComponentRecord, error)

	// GetSnapshotByName returns the latest stored snapshot of canonicalName at generation across
	// incarnations, like GetSnapshot plus CreateTime and UpdateTime of the owning incarnation, or
	// ErrSnapshotNotFound.
	GetSnapshotByName(ctx context.Context, canonicalName string, generation int64) (*ComponentRecord, error)
}
