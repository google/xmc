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

// Test-only package exports for external black-box tests in package dbstore_test.
// Production targets: dbstore.go and memory_store.go.
// Test type: standard Go export_test.go bridge (compiled only into dbstore_test) exposing unexported
// hooks and raw row inspection helpers without polluting the public Store API.
package dbstore

import (
	"context"
	"fmt"
	"time"

	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/memory"
)

// RowInspector reads and writes component rows directly, bypassing the Store API. The
// database-backed stores of the dbstore_test backends implement it.
type RowInspector interface {
	// OwnerByID returns the recorded owner of the record componentID, including a deleted record.
	OwnerByID(ctx context.Context, componentID int64) (*store.SourceOwner, error)
	// SetSourceLastSeenTime sets the recorded SourceLastSeenTime of the active record canonicalName.
	SetSourceLastSeenTime(ctx context.Context, canonicalName string, seenAt time.Time) error
}

// SetBeforeIntentWriteForTest installs hook to run each time SetComponentIntent misses the
// read-only fast path and falls back to its read-write transaction.
func (s *Store) SetBeforeIntentWriteForTest(hook func()) {
	s.beforeIntentWrite = hook
}

// OwnerByIDForTest returns the recorded owner of the record componentID in s, including a deleted
// record, which the Store read methods hide.
func OwnerByIDForTest(ctx context.Context, s store.Store, componentID int64) (*store.SourceOwner, error) {
	switch st := s.(type) {
	case *memory.MemoryStore:
		return st.OwnerByIDForTest(componentID)
	case RowInspector:
		return st.OwnerByID(ctx, componentID)
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
	case RowInspector:
		return st.SetSourceLastSeenTime(ctx, canonicalName, seenAt)
	default:
		return fmt.Errorf("unsupported store %T", s)
	}
}
