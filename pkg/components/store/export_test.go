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

// Test-only package exports for external black-box tests in package store_test.
// Production targets: spanner_store.go and memory_store.go.
// Test type: standard Go export_test.go bridge (compiled only into store_test) exposing unexported
// hooks and raw row inspection helpers without polluting the public Store API.
package store

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
)

// SetBeforeIntentWriteForTest installs hook to run each time SetComponentIntent misses the
// read-only fast path and falls back to its read-write transaction.
func (s *SpannerStore) SetBeforeIntentWriteForTest(hook func()) {
	s.beforeIntentWrite = hook
}

// OwnerByIDForTest returns the recorded owner of the record componentID in s, including a deleted
// record, which the Store read methods hide.
func OwnerByIDForTest(ctx context.Context, s Store, componentID int64) (*SourceOwner, error) {
	switch st := s.(type) {
	case *MemoryStore:
		st.mu.RLock()
		defer st.mu.RUnlock()
		rec, ok := st.componentsByID[componentID]
		if !ok || rec.Owner == nil {
			return nil, fmt.Errorf("component %d has no recorded owner", componentID)
		}
		owner := *rec.Owner
		return &owner, nil
	case *SpannerStore:
		stmt := spanner.Statement{SQL: `SELECT ` + componentColumnsSQL + ` FROM Components WHERE ComponentId = @id`, Params: map[string]any{"id": componentID}}
		var owner *SourceOwner
		err := st.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
			var err error
			owner, err = scanSourceOwner(row)
			return err
		})
		if err == nil && owner == nil {
			err = fmt.Errorf("component %d has no recorded owner", componentID)
		}
		return owner, err
	default:
		return nil, fmt.Errorf("unsupported store %T", s)
	}
}

// SetSourceLastSeenTimeForTest sets the recorded SourceLastSeenTime of canonicalName in s.
func SetSourceLastSeenTimeForTest(ctx context.Context, s Store, canonicalName string, seenAt time.Time) error {
	switch st := s.(type) {
	case *MemoryStore:
		st.mu.Lock()
		defer st.mu.Unlock()
		if rec := st.componentsByName[canonicalName]; rec != nil {
			rec.SourceLastSeenTime = seenAt
		}
		return nil
	case *SpannerStore:
		_, err := st.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
			stmt := spanner.Statement{
				SQL:    `UPDATE Components SET SourceLastSeenTime = @seenAt WHERE ActiveName = @name`,
				Params: map[string]any{"seenAt": seenAt, "name": canonicalName},
			}
			_, err := txn.Update(ctx, stmt)
			return err
		})
		return err
	default:
		return nil
	}
}
