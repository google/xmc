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

package experiment

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/spanner/utils"
)

const (
	selectExperimentConsistencySQL = `SELECT IsExperimentConsistent
FROM ExperimentConsistencyInfo
WHERE BitReversedXid = @bitReversedXid`

	selectDriftedComponentsSQL = `SELECT c.Name, s.LockedGeneration, c.ObservedGeneration, c.State, s.ComponentId
FROM ExperimentComponentSnapshots s
LEFT JOIN@{JOIN_METHOD=APPLY_JOIN} Components c ON s.ComponentId = c.ComponentId
WHERE s.BitReversedXid = @bitReversedXid
  AND (c.ComponentId IS NULL OR c.State = @deletedState OR (c.ObservedGeneration IS NOT NULL AND s.LockedGeneration != c.ObservedGeneration))
ORDER BY c.Name ASC`

	selectExperimentSnapshotComponentsSQL = `SELECT c.Name, s.LockedGeneration, s.ComponentId, s.ConfigProto
FROM ExperimentComponentSnapshots s
JOIN Experiments e ON s.BitReversedXid = e.BitReversedXid
LEFT JOIN@{JOIN_METHOD=APPLY_JOIN} Components c ON s.ComponentId = c.ComponentId
WHERE s.BitReversedXid = @bitReversedXid
  AND e.ActiveState = 1
ORDER BY c.Name ASC, s.ComponentId ASC`
)

// FetchDriftedComponents checks the ExperimentConsistencyInfo Spanner view and, if drifted,
// queries drifted component generation comparisons within a single read-only transaction.
func (r *Repository) FetchDriftedComponents(ctx context.Context, xid int64, limit int) ([]storage.ComponentDiff, error) {
	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()

	isConsistent, err := checkConsistency(ctx, txn, xid)
	if err != nil || isConsistent {
		return nil, err
	}
	return fetchDriftedComponents(ctx, txn, xid, limit)
}

func checkConsistency(ctx context.Context, txn SpannerTransaction, xid int64) (bool, error) {
	stmt := spanner.Statement{
		SQL: selectExperimentConsistencySQL,
		Params: map[string]any{
			"bitReversedXid": spannerutils.ReverseBits(xid),
		},
	}
	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err == iterator.Done {
		// Experiment is inactive or not found in active view; treat as consistent.
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("querying consistency view failed for xid %d: %w", xid, err)
	}

	var isConsistent spanner.NullBool
	if err := row.ColumnByName("IsExperimentConsistent", &isConsistent); err != nil {
		return false, fmt.Errorf("failed reading column 'IsExperimentConsistent': %w", err)
	}
	if !isConsistent.Valid {
		return true, nil
	}
	return isConsistent.Bool, nil
}

func fetchDriftedComponents(ctx context.Context, txn SpannerTransaction, xid int64, limit int) ([]storage.ComponentDiff, error) {
	stmt := spanner.Statement{
		SQL: selectDriftedComponentsSQL,
		Params: map[string]any{
			"bitReversedXid": spannerutils.ReverseBits(xid),
			"deletedState":   storage.ComponentStateDeleted,
		},
	}
	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	var diffs []storage.ComponentDiff
	if limit > 0 {
		diffs = make([]storage.ComponentDiff, 0, limit)
	}
	for {
		if limit > 0 && len(diffs) >= limit {
			break
		}
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading drifted component row failed: %w", err)
		}

		diff, err := scanComponentDiff(row)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, diff)
	}
	return diffs, nil
}

type componentDiffData struct {
	Name               spanner.NullString `spanner:"Name"`
	LockedGeneration   int64              `spanner:"LockedGeneration"`
	ObservedGeneration spanner.NullInt64  `spanner:"ObservedGeneration"`
	State              spanner.NullInt64  `spanner:"State"`
	ComponentID        int64              `spanner:"ComponentId"`
}

// scanComponentDiff parses a single drifted component row into a backend-neutral ComponentDiff.
func scanComponentDiff(row *spanner.Row) (storage.ComponentDiff, error) {
	var data componentDiffData
	if err := row.ToStruct(&data); err != nil {
		return storage.ComponentDiff{}, fmt.Errorf("failed to convert row to struct for component diff: %w", err)
	}
	return storage.ComponentDiff{
		ComponentID:        data.ComponentID,
		Name:               nullStringPtr(data.Name),
		LockedGeneration:   data.LockedGeneration,
		ObservedGeneration: nullInt64Ptr(data.ObservedGeneration),
		State:              nullInt64Ptr(data.State),
	}, nil
}

func nullStringPtr(v spanner.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.StringVal
}

func nullInt64Ptr(v spanner.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

type snapshotComponentData struct {
	Name             spanner.NullString `spanner:"Name"`
	LockedGeneration int64              `spanner:"LockedGeneration"`
	ComponentID      int64              `spanner:"ComponentId"`
	ConfigProto      []byte             `spanner:"ConfigProto"`
}

// FetchExperimentSnapshotComponents retrieves the component snapshots locked by an active experiment.
func (r *Repository) FetchExperimentSnapshotComponents(ctx context.Context, xid int64) ([]storage.ExperimentSnapshotComponent, error) {
	stmt := spanner.Statement{
		SQL: selectExperimentSnapshotComponentsSQL,
		Params: map[string]any{
			"bitReversedXid": spannerutils.ReverseBits(xid),
		},
	}
	iter := r.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	var snapshots []storage.ExperimentSnapshotComponent
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading experiment snapshot component row failed: %w", err)
		}
		snap, err := scanSnapshotComponent(row)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots, nil
}

func scanSnapshotComponent(row *spanner.Row) (storage.ExperimentSnapshotComponent, error) {
	var rowData snapshotComponentData
	if err := row.ToStruct(&rowData); err != nil {
		return storage.ExperimentSnapshotComponent{}, fmt.Errorf("failed to convert row to struct for snapshot component: %w", err)
	}
	spec, err := data.DecodeComponentSpec(rowData.ConfigProto)
	if err != nil {
		return storage.ExperimentSnapshotComponent{}, err
	}
	return storage.ExperimentSnapshotComponent{
		ComponentID:      rowData.ComponentID,
		Name:             nullStringPtr(rowData.Name),
		LockedGeneration: rowData.LockedGeneration,
		Spec:             spec,
	}, nil
}
