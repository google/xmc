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

// Database backends of the dbstore tests: each backend opens a fresh storage.Storage and provides
// backendRows, the hooks that seed and inspect its rows directly. Tests reach the database only
// through these hooks, so a new backend needs only a new dbBackends entry.
package dbstore_test

import (
	"context"
	"testing"
	"time"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dbstore"
)

// dbBackend is a database that the dbstore tests run against.
type dbBackend struct {
	name string
	// open returns a storage.Storage on a fresh, empty database and the hooks that reach its rows.
	open func(ctx context.Context, t *testing.T) (storage.Storage, backendRows)
}

// dbBackends returns every database that the cross-backend dbstore tests run against.
func dbBackends() []dbBackend {
	return []dbBackend{spannerBackend()}
}

// newStore returns a Store on a fresh b database and the hooks that reach its rows.
func (b dbBackend) newStore(ctx context.Context, t *testing.T) (*dbstore.Store, backendRows) {
	t.Helper()
	st, rows := b.open(ctx, t)
	s, err := dbstore.New(st)
	if err != nil {
		t.Fatalf("dbstore.New() failed: %v", err)
	}
	return s, rows
}

// backendRows seeds and inspects the rows of one test database directly, bypassing the Store API,
// for state that the Store API cannot create or does not expose.
type backendRows interface {
	// OwnerByID returns the recorded owner of the record componentID, including a deleted record.
	OwnerByID(ctx context.Context, componentID int64) (*store.SourceOwner, error)
	// SetSourceLastSeenTime sets the recorded SourceLastSeenTime of the active record canonicalName.
	SetSourceLastSeenTime(ctx context.Context, canonicalName string, seenAt time.Time) error
	// SourceTimes returns SourceLastSeenTime and UpdateTime of the active record canonicalName.
	SourceTimes(ctx context.Context, canonicalName string) (lastSeen, update time.Time, err error)
	// ComponentRowTimestamps returns CreateTime, UpdateTime, and SourceLastSeenTime of componentID.
	ComponentRowTimestamps(ctx context.Context, componentID int64) (rowTimestamps, error)
	// ComponentRows returns every record named name, deleted or not, in ComponentId order.
	ComponentRows(ctx context.Context, name string) ([]componentRow, error)
	// HardDeleteComponent deletes the row of componentID outright instead of soft-deleting it.
	HardDeleteComponent(ctx context.Context, componentID int64) error
	// InsertExperimentWithSnapshot inserts exp and snap, which binds a component generation to it.
	InsertExperimentWithSnapshot(ctx context.Context, exp *essmsgpb.Experiment, snap snapshotRow) error
	// CountSnapshots returns the number of snapshot rows that reference componentID.
	CountSnapshots(ctx context.Context, componentID int64) (int64, error)
	// FenceDeclaredTime returns the DeclaredTime of the ownership fence.
	FenceDeclaredTime(ctx context.Context) (time.Time, error)
	// InsertFence inserts row into the ownership fence table, bypassing DeclareClusterEpoch.
	InsertFence(ctx context.Context, row fenceRow) error
}

// rowTimestamps holds the timestamps of one Components row.
type rowTimestamps struct {
	Create, Update, LastSeen time.Time
}

// componentRow holds the lifecycle columns of one Components row.
type componentRow struct {
	ID    int64
	State int64
	// ActiveName is empty when the column is NULL, that is once the record released its name.
	ActiveName string
}

// snapshotRow is an ExperimentComponentSnapshots row that locks generation LockedGeneration of
// ComponentID for experiment Xid.
type snapshotRow struct {
	Xid, ComponentID, LockedGeneration int64
	ConfigProto, AccessPayload         []byte
}

// fenceRow is a ComponentOwnershipFence row. DeclaredTime is the commit time, and so is
// LastSeenTime unless NullLastSeenTime is set.
type fenceRow struct {
	FenceID                 int64
	ClusterUID, ClusterName string
	Epoch                   int64
	NullLastSeenTime        bool
}

// inspectableStore is a database-backed Store together with the hooks that reach its rows, so that
// dbstore.OwnerByIDForTest and dbstore.SetSourceLastSeenTimeForTest work on it.
type inspectableStore struct {
	*dbstore.Store
	backendRows
}
