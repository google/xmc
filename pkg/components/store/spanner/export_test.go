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

// Test-only package exports for external black-box tests in package spanner_test.
// Production targets: spanner_store.go and memory_store.go.
// Test type: standard Go export_test.go bridge (compiled only into spanner_test) exposing unexported
// hooks and raw row inspection helpers without polluting the public Store API.
package spanner

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"

	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/memory"
)

// SetBeforeIntentWriteForTest installs hook to run each time SetComponentIntent misses the
// read-only fast path and falls back to its read-write transaction.
func (s *SpannerStore) SetBeforeIntentWriteForTest(hook func()) {
	s.beforeIntentWrite = hook
}

// OwnerByIDForTest returns the recorded owner of the record componentID in s, including a deleted
// record, which the Store read methods hide.
func OwnerByIDForTest(ctx context.Context, s store.Store, componentID int64) (*store.SourceOwner, error) {
	switch st := s.(type) {
	case *memory.MemoryStore:
		return st.OwnerByIDForTest(componentID)
	case *SpannerStore:
		stmt := spanner.Statement{SQL: `SELECT SourceUid, SourceGeneration, SourceNamespace, SourceClusterUid, SourceEpoch FROM Components WHERE ComponentId = @id`, Params: map[string]any{"id": componentID}}
		var owner *store.SourceOwner
		err := st.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
			var uid, namespace, clusterUID spanner.NullString
			var generation, epoch spanner.NullInt64
			if err := row.Columns(&uid, &generation, &namespace, &clusterUID, &epoch); err != nil {
				return err
			}
			if clusterUID.Valid {
				owner = &store.SourceOwner{UID: uid.StringVal, Generation: generation.Int64, Namespace: namespace.StringVal, ClusterUID: clusterUID.StringVal, Epoch: epoch.Int64}
			}
			return nil
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
func SetSourceLastSeenTimeForTest(ctx context.Context, s store.Store, canonicalName string, seenAt time.Time) error {
	switch st := s.(type) {
	case *memory.MemoryStore:
		st.SetSourceLastSeenTimeForTest(canonicalName, seenAt)
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
