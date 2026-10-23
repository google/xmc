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

package component

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"

	"xmanager_cloud/pkg/components/store"
)

const (
	selectSnapshotByComponentIDAndGenSQL = `SELECT ComponentId, LockedGeneration, ConfigProto, AccessPayload
FROM ExperimentComponentSnapshots
WHERE ComponentId = @id AND LockedGeneration = @gen
LIMIT 1`

	selectSnapshotByNameAndGenSQL = `SELECT s.ComponentId, s.LockedGeneration, s.ConfigProto, s.AccessPayload, c.CreateTime, c.UpdateTime
FROM ExperimentComponentSnapshots s
JOIN Components c ON s.ComponentId = c.ComponentId
WHERE c.Name = @name AND s.LockedGeneration = @gen
ORDER BY c.CreateTime DESC, c.ComponentId DESC
LIMIT 1`
)

// snapshotRowData holds the raw ExperimentComponentSnapshots column values read from a query row.
type snapshotRowData struct {
	componentID   int64
	lockedGen     int64
	configProto   []byte
	accessPayload []byte
}

// GetSnapshot returns the decoded snapshot of componentID at generation.
func (r *reader) GetSnapshot(ctx context.Context, componentID, generation int64) (*store.ComponentRecord, error) {
	stmt := spanner.Statement{
		SQL: selectSnapshotByComponentIDAndGenSQL,
		Params: map[string]any{
			"id":  componentID,
			"gen": generation,
		},
	}
	row, err := querySnapshotRow(ctx, r.q, stmt)
	if err != nil {
		return nil, err
	}
	d, err := scanSnapshotRow(row)
	if err != nil {
		return nil, err
	}
	return snapshotRowToRecord(d)
}

// GetSnapshotByName returns the decoded snapshot of canonicalName at generation, covering
// soft-deleted or recreated incarnations. Timestamps come from the joined Components row of the
// incarnation that owns the snapshot.
func (r *reader) GetSnapshotByName(ctx context.Context, canonicalName string, generation int64) (*store.ComponentRecord, error) {
	stmt := spanner.Statement{
		SQL: selectSnapshotByNameAndGenSQL,
		Params: map[string]any{
			"name": canonicalName,
			"gen":  generation,
		},
	}
	row, err := querySnapshotRow(ctx, r.q, stmt)
	if err != nil {
		return nil, err
	}
	d, err := scanSnapshotRow(row)
	if err != nil {
		return nil, err
	}

	var createTime, updateTime time.Time
	if err := row.ColumnByName("CreateTime", &createTime); err != nil {
		return nil, fmt.Errorf("failed to read CreateTime: %w", err)
	}
	if err := row.ColumnByName("UpdateTime", &updateTime); err != nil {
		return nil, fmt.Errorf("failed to read UpdateTime: %w", err)
	}
	snapRec, err := snapshotRowToRecord(d)
	if err != nil {
		return nil, err
	}
	snapRec.CreateTime, snapRec.UpdateTime = createTime, updateTime
	return snapRec, nil
}

// querySnapshotRow executes stmt and returns the first matching ExperimentComponentSnapshots row.
// It returns store.ErrSnapshotNotFound when no snapshot exists for the requested generation.
func querySnapshotRow(ctx context.Context, queryable spannerQueryable, stmt spanner.Statement) (*spanner.Row, error) {
	iter := queryable.Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			return nil, store.ErrSnapshotNotFound
		}
		return nil, fmt.Errorf("failed to query experiment component snapshots: %w", err)
	}
	return row, nil
}

// scanSnapshotRow reads the ExperimentComponentSnapshots columns shared by all snapshot queries.
func scanSnapshotRow(row *spanner.Row) (*snapshotRowData, error) {
	var d snapshotRowData
	if err := row.ColumnByName("ComponentId", &d.componentID); err != nil {
		return nil, fmt.Errorf("failed to read ComponentId: %w", err)
	}
	if err := row.ColumnByName("LockedGeneration", &d.lockedGen); err != nil {
		return nil, fmt.Errorf("failed to read LockedGeneration: %w", err)
	}
	if err := row.ColumnByName("ConfigProto", &d.configProto); err != nil {
		return nil, fmt.Errorf("failed to read ConfigProto: %w", err)
	}
	if err := row.ColumnByName("AccessPayload", &d.accessPayload); err != nil {
		return nil, fmt.Errorf("failed to read AccessPayload: %w", err)
	}
	return &d, nil
}

// snapshotRowToRecord decodes a scanned snapshot row into a record with only ComponentID,
// Generation, Spec, Labels and Status set. The record is built from scratch so no active
// component state leaks into the historical view.
func snapshotRowToRecord(d *snapshotRowData) (*store.ComponentRecord, error) {
	snapRec := &store.ComponentRecord{
		ComponentID: d.componentID,
		Generation:  d.lockedGen,
	}
	if err := unmarshalConfigProto(d.configProto, snapRec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal historical config proto: %w", err)
	}
	if err := unmarshalAccessStatus(d.accessPayload, snapRec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal historical access status: %w", err)
	}
	return snapRec, nil
}
