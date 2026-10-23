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

// In-memory store.ComponentRepository test double for the dbstore unit tests.
package dbstore

import (
	"context"
	"sort"
	"sync"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// memoryRepository is a high-fidelity in-memory store.ComponentRepository. Like the Spanner
// schema, it keeps deleted components as rows and dependency rows by component ID, and its reads
// see only active (non-deleted) components and dependencies. Every snapshot reads a copy of the
// committed rows taken when it starts, so later writes stay invisible to it.
type memoryRepository struct {
	mu        sync.Mutex
	committed memoryRows
	// readErr, when non-nil, fails every read.
	readErr error
}

// memoryRows are the stored rows of a memoryRepository.
type memoryRows struct {
	components   map[int64]*store.ComponentRecord
	dependencies map[int64][]int64 // Component ID to the IDs of its dependencies.
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{committed: memoryRows{
		components:   map[int64]*store.ComponentRecord{},
		dependencies: map[int64][]int64{},
	}}
}

// putComponent stores a copy of rec, without its Dependencies and Etag, and dependencyIDs as its
// dependency rows.
func (m *memoryRepository) putComponent(rec *store.ComponentRecord, dependencyIDs ...int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := store.CloneRecord(rec)
	row.Dependencies, row.Etag = nil, ""
	m.committed.components[rec.ComponentID] = row
	m.committed.dependencies[rec.ComponentID] = append([]int64(nil), dependencyIDs...)
}

// clone returns a deep copy of the rows.
func (r memoryRows) clone() memoryRows {
	cp := memoryRows{
		components:   make(map[int64]*store.ComponentRecord, len(r.components)),
		dependencies: make(map[int64][]int64, len(r.dependencies)),
	}
	for id, rec := range r.components {
		cp.components[id] = store.CloneRecord(rec)
	}
	for id, deps := range r.dependencies {
		cp.dependencies[id] = append([]int64(nil), deps...)
	}
	return cp
}

// ReadSnapshot runs fn against a copy of the committed rows.
func (m *memoryRepository) ReadSnapshot(ctx context.Context, fn func(ctx context.Context, r store.ComponentReader) error) error {
	m.mu.Lock()
	rd := &memoryReader{rows: m.committed.clone(), err: m.readErr}
	m.mu.Unlock()
	return fn(ctx, rd)
}

// memoryReader reads one copy of the rows of a memoryRepository.
type memoryReader struct {
	rows memoryRows
	err  error
}

// isActiveRow reports whether rec has an ActiveName, that is, whether it is not deleted.
func isActiveRow(rec *store.ComponentRecord) bool {
	return rec.State != componentspb.ComponentState_COMPONENT_STATE_DELETED
}

func (r *memoryReader) GetActiveComponent(_ context.Context, canonicalName string) (*store.ComponentRecord, error) {
	if r.err != nil {
		return nil, r.err
	}
	for _, rec := range r.rows.components {
		if rec.Name == canonicalName && isActiveRow(rec) {
			return r.withDependencies(rec), nil
		}
	}
	return nil, store.ErrComponentNotFound
}

func (r *memoryReader) GetComponentByID(_ context.Context, componentID int64) (*store.ComponentRecord, error) {
	if r.err != nil {
		return nil, r.err
	}
	rec, ok := r.rows.components[componentID]
	if !ok || !isActiveRow(rec) {
		return nil, store.ErrComponentNotFound
	}
	return r.withDependencies(rec), nil
}

func (r *memoryReader) ListActiveComponents(_ context.Context) (map[string]*store.ComponentRecord, error) {
	if r.err != nil {
		return nil, r.err
	}
	catalog := make(map[string]*store.ComponentRecord)
	for _, rec := range r.rows.components {
		if isActiveRow(rec) {
			catalog[rec.Name] = r.withDependencies(rec)
		}
	}
	return catalog, nil
}

// withDependencies returns a copy of rec with the sorted names of its active dependencies (nil
// when there are none) and its Etag, as the Spanner reads return it.
func (r *memoryReader) withDependencies(rec *store.ComponentRecord) *store.ComponentRecord {
	cp := store.CloneRecord(rec)
	for _, depID := range r.rows.dependencies[rec.ComponentID] {
		if dep, ok := r.rows.components[depID]; ok && isActiveRow(dep) {
			cp.Dependencies = append(cp.Dependencies, dep.Name)
		}
	}
	sort.Strings(cp.Dependencies)
	cp.Etag = store.GenerateEtag(cp)
	return cp
}
