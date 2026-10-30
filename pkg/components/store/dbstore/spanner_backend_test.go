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

package dbstore_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"cloud.google.com/go/spanner"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	spannerstorage "xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/component"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/pkg/components/store"
)

// spannerBackend runs the dbstore tests against a fresh database on the Spanner emulator.
func spannerBackend() dbBackend {
	return dbBackend{name: "spanner", open: openSpannerStorage}
}

// openSpannerStorage returns a storage.Storage on a fresh Spanner emulator database and the hooks
// that reach its rows.
func openSpannerStorage(ctx context.Context, t *testing.T) (storage.Storage, backendRows) {
	t.Helper()
	_, client := emulator.Setup(ctx, t)
	st, err := spannerstorage.NewStorage(client)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	return st, spannerRows{client: client}
}

// spannerRows implements backendRows with SQL and mutations on a Spanner database.
type spannerRows struct {
	client *spanner.Client
}

// OwnerByID implements backendRows.
func (r spannerRows) OwnerByID(ctx context.Context, componentID int64) (*store.SourceOwner, error) {
	stmt := spanner.Statement{SQL: `SELECT ` + component.ComponentColumnsSQL + ` FROM Components WHERE ComponentId = @id`, Params: map[string]any{"id": componentID}}
	var owner *store.SourceOwner
	err := r.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		rec, err := component.RowToComponentRecord(row)
		if err != nil {
			return err
		}
		owner = rec.Owner
		return nil
	})
	if err != nil {
		return nil, err
	}
	if owner == nil {
		return nil, fmt.Errorf("component %d has no recorded owner", componentID)
	}
	return owner, nil
}

// SetSourceLastSeenTime implements backendRows.
func (r spannerRows) SetSourceLastSeenTime(ctx context.Context, canonicalName string, seenAt time.Time) error {
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		_, err := txn.Update(ctx, spanner.Statement{SQL: `UPDATE Components SET SourceLastSeenTime = @seenAt WHERE ActiveName = @name`, Params: map[string]any{"seenAt": seenAt, "name": canonicalName}})
		return err
	})
	return err
}

// SourceTimes implements backendRows.
func (r spannerRows) SourceTimes(ctx context.Context, canonicalName string) (lastSeen, update time.Time, err error) {
	stmt := spanner.Statement{
		SQL:    `SELECT SourceLastSeenTime, UpdateTime FROM Components WHERE ActiveName = @name`,
		Params: map[string]any{"name": canonicalName},
	}
	found := false
	err = r.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		found = true
		return row.Columns(&lastSeen, &update)
	})
	if err == nil && !found {
		err = fmt.Errorf("active component %q not found", canonicalName)
	}
	return lastSeen, update, err
}

// ComponentRowTimestamps implements backendRows.
func (r spannerRows) ComponentRowTimestamps(ctx context.Context, componentID int64) (rowTimestamps, error) {
	row, err := r.client.Single().ReadRow(ctx, "Components", spanner.Key{componentID}, []string{"CreateTime", "UpdateTime", "SourceLastSeenTime"})
	if err != nil {
		return rowTimestamps{}, err
	}
	var ts rowTimestamps
	var nullLastSeen spanner.NullTime
	if err := row.Columns(&ts.Create, &ts.Update, &nullLastSeen); err != nil {
		return rowTimestamps{}, err
	}
	if nullLastSeen.Valid {
		ts.LastSeen = nullLastSeen.Time
	}
	return ts, nil
}

// ComponentRows implements backendRows.
func (r spannerRows) ComponentRows(ctx context.Context, name string) ([]componentRow, error) {
	stmt := spanner.Statement{
		SQL:    `SELECT ComponentId, State, ActiveName FROM Components WHERE Name = @name ORDER BY ComponentId ASC`,
		Params: map[string]any{"name": name},
	}
	var rows []componentRow
	err := r.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var cr componentRow
		var activeName spanner.NullString
		if err := row.Columns(&cr.ID, &cr.State, &activeName); err != nil {
			return err
		}
		if activeName.Valid {
			cr.ActiveName = &activeName.StringVal
		}
		rows = append(rows, cr)
		return nil
	})
	return rows, err
}

// HardDeleteComponent implements backendRows.
func (r spannerRows) HardDeleteComponent(ctx context.Context, componentID int64) error {
	_, err := r.client.Apply(ctx, []*spanner.Mutation{spanner.Delete("Components", spanner.Key{componentID})})
	return err
}

// InsertExperimentWithSnapshot implements backendRows. Spanner keys snapshots by the bit-reversed
// experiment ID.
func (r spannerRows) InsertExperimentWithSnapshot(ctx context.Context, exp *essmsgpb.Experiment, snap snapshotRow) error {
	expMut, err := spannerdata.ExperimentProtoToInsertMutation(exp)
	if err != nil {
		return fmt.Errorf("failed to create experiment mutation: %w", err)
	}
	snapMut := spanner.Insert("ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto", "AccessPayload"},
		[]any{data.ReverseBits(snap.Xid), snap.ComponentID, snap.LockedGeneration, snap.ConfigProto, snap.AccessPayload})
	_, err = r.client.Apply(ctx, []*spanner.Mutation{expMut, snapMut})
	return err
}

// CountSnapshots implements backendRows.
func (r spannerRows) CountSnapshots(ctx context.Context, componentID int64) (int64, error) {
	var count int64
	err := r.client.Single().Query(ctx, spanner.Statement{
		SQL:    "SELECT COUNT(1) AS Total FROM ExperimentComponentSnapshots WHERE ComponentId = @id",
		Params: map[string]any{"id": componentID},
	}).Do(func(row *spanner.Row) error { return row.ColumnByName("Total", &count) })
	return count, err
}

// FenceDeclaredTime implements backendRows.
func (r spannerRows) FenceDeclaredTime(ctx context.Context) (time.Time, error) {
	var declared time.Time
	row, err := r.client.Single().ReadRow(ctx, "ComponentOwnershipFence", spanner.Key{int64(1)}, []string{"DeclaredTime"})
	if err != nil {
		return declared, err
	}
	err = row.Columns(&declared)
	return declared, err
}

// InsertFence implements backendRows.
func (r spannerRows) InsertFence(ctx context.Context, row fenceRow) error {
	var lastSeen any = spanner.CommitTimestamp
	if row.NullLastSeenTime {
		lastSeen = nil
	}
	cols := []string{"FenceId", "ActiveClusterUid", "ActiveClusterName", "Epoch", "DeclaredTime", "LastSeenTime"}
	vals := []any{row.FenceID, row.ClusterUID, row.ClusterName, row.Epoch, spanner.CommitTimestamp, lastSeen}
	_, err := r.client.Apply(ctx, []*spanner.Mutation{spanner.Insert("ComponentOwnershipFence", cols, vals)})
	return err
}
