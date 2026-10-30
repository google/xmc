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

// Package dbstoretest provides database backends for the dbstore tests: each backend opens a fresh
// storage.Storage and provides Rows, the hooks that seed and inspect its rows directly. Tests reach
// the database only through these hooks, so a new backend needs only a new Backends entry.
package dbstoretest

import (
	"context"
	"fmt"
	"testing"
	"time"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/store"
)

// Backend is a database that the dbstore tests run against.
type Backend struct {
	Name string
	// Open returns a storage.Storage on a fresh, empty database and the hooks that reach its rows.
	Open func(ctx context.Context, t *testing.T) (storage.Storage, Rows)
}

// Backends returns every database that the cross-backend dbstore tests run against.
func Backends() []Backend {
	return []Backend{SpannerBackend(), PostgresBackend()}
}

// Rows seeds and inspects the rows of one test database directly, bypassing the Store API,
// for state that the Store API cannot create or does not expose.
type Rows interface {
	// OwnerByID returns the recorded owner of the record componentID, including a deleted record.
	OwnerByID(ctx context.Context, componentID int64) (*store.SourceOwner, error)
	// SetSourceLastSeenTime sets the recorded SourceLastSeenTime of the active record canonicalName.
	SetSourceLastSeenTime(ctx context.Context, canonicalName string, seenAt time.Time) error
	// SourceTimes returns SourceLastSeenTime and UpdateTime of the active record canonicalName.
	SourceTimes(ctx context.Context, canonicalName string) (lastSeen, update time.Time, err error)
	// ComponentRowTimestamps returns CreateTime, UpdateTime, and SourceLastSeenTime of componentID.
	ComponentRowTimestamps(ctx context.Context, componentID int64) (RowTimestamps, error)
	// ComponentRows returns every record named name, deleted or not, in ComponentId order.
	ComponentRows(ctx context.Context, name string) ([]ComponentRow, error)
	// HardDeleteComponent deletes the row of componentID outright instead of soft-deleting it.
	HardDeleteComponent(ctx context.Context, componentID int64) error
	// InsertExperimentWithSnapshot inserts exp and snap, which binds a component generation to it.
	InsertExperimentWithSnapshot(ctx context.Context, exp *essmsgpb.Experiment, snap SnapshotRow) error
	// CountSnapshots returns the number of snapshot rows that reference componentID.
	CountSnapshots(ctx context.Context, componentID int64) (int64, error)
	// FenceDeclaredTime returns the DeclaredTime of the ownership fence.
	FenceDeclaredTime(ctx context.Context) (time.Time, error)
	// InsertFence inserts row into the ownership fence table, bypassing DeclareClusterEpoch.
	InsertFence(ctx context.Context, row FenceRow) error
}

// RowTimestamps holds the timestamps of one Components row.
type RowTimestamps struct {
	Create, Update, LastSeen time.Time
}

// ComponentRow holds the lifecycle columns of one Components row.
type ComponentRow struct {
	ID    int64
	State int64
	// ActiveName is nil when the column is NULL, that is once the record released its name.
	ActiveName *string
}

func (r ComponentRow) String() string {
	name := "NULL"
	if r.ActiveName != nil {
		name = fmt.Sprintf("%q", *r.ActiveName)
	}
	return fmt.Sprintf("{ID: %d, State: %d, ActiveName: %s}", r.ID, r.State, name)
}

// SnapshotRow is an ExperimentComponentSnapshots row that locks generation LockedGeneration of
// ComponentID for experiment Xid.
type SnapshotRow struct {
	Xid, ComponentID, LockedGeneration int64
	ConfigProto, AccessPayload         []byte
}

// FenceRow is a ComponentOwnershipFence row. DeclaredTime is the commit time, and so is
// LastSeenTime unless NullLastSeenTime is set.
type FenceRow struct {
	FenceID                 int64
	ClusterUID, ClusterName string
	Epoch                   int64
	NullLastSeenTime        bool
}
