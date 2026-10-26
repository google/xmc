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
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// consistencyResult mirrors one row of the ExperimentConsistencyInfo view.
type consistencyResult struct {
	isConsistent bool
	hasPending   bool
}

// seedComponentWithSnapshot upserts a Components row and an ExperimentComponentSnapshots row
// binding it to xid. A nil observedGen leaves ObservedGeneration NULL (pending reconciliation).
func seedComponentWithSnapshot(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid, componentID int64, name string, state, generation int64, observedGen *int64, lockedGen int64) {
	t.Helper()
	const upsertComponentSQL = `INSERT INTO Components (ComponentId, Name, ComponentClass, ConfigProto, State, Generation, ObservedGeneration, CreateTime, UpdateTime)
VALUES ($1, $2, 'storage', $3, $4, $5, $6, now(), now())
ON CONFLICT (ComponentId) DO UPDATE SET
  Name = EXCLUDED.Name,
  State = EXCLUDED.State,
  Generation = EXCLUDED.Generation,
  ObservedGeneration = EXCLUDED.ObservedGeneration,
  UpdateTime = EXCLUDED.UpdateTime`
	if _, err := pool.Exec(ctx, upsertComponentSQL, componentID, name, []byte{}, state, generation, observedGen); err != nil {
		t.Fatalf("Exec(upsertComponentSQL, %q) unexpected error: %v", name, err)
	}

	const upsertSnapshotSQL = `INSERT INTO ExperimentComponentSnapshots (Xid, ComponentId, LockedGeneration, ConfigProto)
VALUES ($1, $2, $3, $4)
ON CONFLICT (Xid, ComponentId) DO UPDATE SET
  LockedGeneration = EXCLUDED.LockedGeneration`
	if _, err := pool.Exec(ctx, upsertSnapshotSQL, xid, componentID, lockedGen, []byte{}); err != nil {
		t.Fatalf("Exec(upsertSnapshotSQL, %q) unexpected error: %v", name, err)
	}
}

// queryConsistencyInfo reads the ExperimentConsistencyInfo row for xid, reporting whether the
// view emitted a row at all.
func queryConsistencyInfo(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid int64) (consistencyResult, bool) {
	t.Helper()
	const query = "SELECT IsExperimentConsistent, HasPendingReconciliation FROM ExperimentConsistencyInfo WHERE Xid = $1"
	var got consistencyResult
	err := pool.QueryRow(ctx, query, xid).Scan(&got.isConsistent, &got.hasPending)
	if errors.Is(err, pgx.ErrNoRows) {
		return consistencyResult{}, false
	}
	if err != nil {
		t.Fatalf("QueryRow(%q, %d) unexpected error: %v", query, xid, err)
	}
	return got, true
}

func TestDriftDetection_ConsistentSnapshots(t *testing.T) {
	ctx := t.Context()
	repo, essPool := newTestRepository(t)

	const xid = int64(1001)
	createdExp := createBlankExperiment(ctx, t, repo, xid)
	observed := int64(1)
	seedComponentWithSnapshot(ctx, t, essPool, xid, 201, "clickhouse", 1, 1, &observed, 1)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", createdExp.GetName(), err)
	}

	diffs, err := repo.FetchDriftedComponents(ctx, parsedXID, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents(%d) unexpected error: %v", parsedXID, err)
	}
	if len(diffs) != 0 {
		t.Errorf("FetchDriftedComponents(%d) = %v, want empty", parsedXID, diffs)
	}
}

func TestDriftDetection_GenerationDiverged(t *testing.T) {
	ctx := t.Context()
	repo, essPool := newTestRepository(t)

	const xid = int64(1002)
	createdExp := createBlankExperiment(ctx, t, repo, xid)

	// Component is at generation 2 with observed generation 2, but experiment snapshot locked
	// generation 1.
	observed := int64(2)
	seedComponentWithSnapshot(ctx, t, essPool, xid, 202, "redpanda", 1, 2, &observed, 1)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", createdExp.GetName(), err)
	}

	diffs, err := repo.FetchDriftedComponents(ctx, parsedXID, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents(%d) unexpected error: %v", parsedXID, err)
	}
	name := "redpanda"
	state := int64(1)
	want := []storage.ComponentDiff{
		{
			ComponentID:        202,
			Name:               &name,
			LockedGeneration:   1,
			ObservedGeneration: &observed,
			State:              &state,
		},
	}
	if diff := cmp.Diff(want, diffs); diff != "" {
		t.Errorf("FetchDriftedComponents(%d) mismatch (-want +got):\n%s", parsedXID, diff)
	}
}

func TestDriftDetection_ComponentSoftDeleted(t *testing.T) {
	ctx := t.Context()
	repo, essPool := newTestRepository(t)

	const xid = int64(1003)
	createdExp := createBlankExperiment(ctx, t, repo, xid)

	// State = 5 indicates COMPONENT_STATE_DELETED.
	observed := int64(2)
	seedComponentWithSnapshot(ctx, t, essPool, xid, 203, "clickhouse", storage.ComponentStateDeleted, 2, &observed, 1)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", createdExp.GetName(), err)
	}

	diffs, err := repo.FetchDriftedComponents(ctx, parsedXID, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents(%d) unexpected error: %v", parsedXID, err)
	}
	name := "clickhouse"
	deletedState := int64(storage.ComponentStateDeleted)
	want := []storage.ComponentDiff{
		{
			ComponentID:        203,
			Name:               &name,
			LockedGeneration:   1,
			ObservedGeneration: &observed,
			State:              &deletedState,
		},
	}
	if diff := cmp.Diff(want, diffs); diff != "" {
		t.Errorf("FetchDriftedComponents(%d) mismatch (-want +got):\n%s", parsedXID, diff)
	}
}

func TestDriftDetection_FetchDriftedComponentsLimit(t *testing.T) {
	ctx := t.Context()
	repo, essPool := newTestRepository(t)

	const xid = int64(1010)
	createdExp := createBlankExperiment(ctx, t, repo, xid)

	observed := int64(2)
	seedComponentWithSnapshot(ctx, t, essPool, xid, 301, "comp-b", 1, 2, &observed, 1)
	seedComponentWithSnapshot(ctx, t, essPool, xid, 302, "comp-a", 1, 2, &observed, 1)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", createdExp.GetName(), err)
	}

	diffs, err := repo.FetchDriftedComponents(ctx, parsedXID, 1)
	if err != nil {
		t.Fatalf("FetchDriftedComponents(%d, 1) unexpected error: %v", parsedXID, err)
	}
	if len(diffs) != 1 || diffs[0].Name == nil || *diffs[0].Name != "comp-a" {
		t.Errorf("FetchDriftedComponents(%d, 1) = %v, want single entry for comp-a", parsedXID, diffs)
	}
}

func TestDriftDetection_TerminalExperimentFiltered(t *testing.T) {
	ctx := t.Context()
	repo, essPool := newTestRepository(t)

	const xid = int64(1004)
	expProto := blankExperimentProto(xid)
	expProto.Status.ActiveState = pb.ExperimentStatus_INACTIVE
	createdExp := createExperiment(ctx, t, repo, expProto)

	// Live component diverged.
	observed := int64(2)
	seedComponentWithSnapshot(ctx, t, essPool, xid, 204, "ingestion-gateway", 1, 2, &observed, 1)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", createdExp.GetName(), err)
	}

	diffs, err := repo.FetchDriftedComponents(ctx, parsedXID, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents(%d) unexpected error: %v", parsedXID, err)
	}
	if len(diffs) != 0 {
		t.Errorf("FetchDriftedComponents(%d) = %v, want empty for inactive experiment excluded from view", parsedXID, diffs)
	}
}

func TestDriftDetection_PendingReconciliationNotDrifted(t *testing.T) {
	ctx := t.Context()
	repo, essPool := newTestRepository(t)

	const xid = int64(1005)
	createdExp := createBlankExperiment(ctx, t, repo, xid)

	// ObservedGeneration is NULL (pending initial GKE reconciliation).
	seedComponentWithSnapshot(ctx, t, essPool, xid, 205, "pending-comp", 1, 1, nil, 1)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", createdExp.GetName(), err)
	}

	diffs, err := repo.FetchDriftedComponents(ctx, parsedXID, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents(%d) unexpected error: %v", parsedXID, err)
	}
	if len(diffs) != 0 {
		t.Errorf("FetchDriftedComponents(%d) = %v, want empty during pending reconciliation", parsedXID, diffs)
	}
}

func TestDriftDetection_NoSnapshots_Consistent(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	const xid = int64(1006)
	createdExp := createBlankExperiment(ctx, t, repo, xid)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", createdExp.GetName(), err)
	}

	diffs, err := repo.FetchDriftedComponents(ctx, parsedXID, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents(%d) unexpected error: %v", parsedXID, err)
	}
	if len(diffs) != 0 {
		t.Errorf("FetchDriftedComponents(%d) = %v, want empty for experiment with no snapshots", parsedXID, diffs)
	}
}

func TestDriftDetection_NonExistentExperiment_Consistent(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	diffs, err := repo.FetchDriftedComponents(ctx, 999999, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents(999999) unexpected error: %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("FetchDriftedComponents(999999) = %v, want empty", diffs)
	}
}

func TestConsistencyView_HasPendingReconciliationColumn(t *testing.T) {
	ctx := t.Context()
	repo, essPool := newTestRepository(t)

	const xid = int64(1200)
	createBlankExperiment(ctx, t, repo, xid)

	// ObservedGeneration NULL: awaiting the first GKE reconciliation, not drifted.
	seedComponentWithSnapshot(ctx, t, essPool, xid, 400, "pending-view-comp", 1, 1, nil, 1)

	got, found := queryConsistencyInfo(ctx, t, essPool, xid)
	if !found {
		t.Fatalf("expected a view row for Xid %d, got none", xid)
	}
	if !got.isConsistent {
		t.Errorf("IsExperimentConsistent = false, want true (NULL ObservedGeneration is not drift)")
	}
	if !got.hasPending {
		t.Errorf("HasPendingReconciliation = false, want true (component has NULL ObservedGeneration)")
	}
}

func TestConsistencyView_TerminalExperimentExcluded(t *testing.T) {
	ctx := t.Context()
	repo, essPool := newTestRepository(t)

	const xid = int64(1201)
	expProto := blankExperimentProto(xid)
	expProto.Status.ActiveState = pb.ExperimentStatus_INACTIVE
	createExperiment(ctx, t, repo, expProto)

	// Drifted component (live generation 2 vs locked generation 1) that the view must still
	// ignore because the owning experiment is terminal.
	observedGen := int64(2)
	seedComponentWithSnapshot(ctx, t, essPool, xid, 401, "terminal-view-comp", 1, 2, &observedGen, 1)

	if _, found := queryConsistencyInfo(ctx, t, essPool, xid); found {
		t.Errorf("view returned a row for terminal experiment %d, want it excluded by WHERE e.ActiveState = 1", xid)
	}
}

func TestConsistencyView_MultipleComponentsAggregation(t *testing.T) {
	ctx := t.Context()
	repo, essPool := newTestRepository(t)

	const xid = int64(1202)
	createBlankExperiment(ctx, t, repo, xid)

	// Three components, all reconciled at the generation their snapshot locked.
	observedGen := int64(1)
	for i, name := range []string{"multi-view-comp-1", "multi-view-comp-2", "multi-view-comp-3"} {
		seedComponentWithSnapshot(ctx, t, essPool, xid, int64(410+i), name, 1, 1, &observedGen, 1)
	}

	got, found := queryConsistencyInfo(ctx, t, essPool, xid)
	if !found {
		t.Fatalf("expected a view row for Xid %d, got none", xid)
	}
	if !got.isConsistent || got.hasPending {
		t.Errorf("got isConsistent=%v hasPending=%v, want true/false with all components reconciled", got.isConsistent, got.hasPending)
	}

	// Drift exactly one of the three: NOT EXISTS must flip the whole experiment inconsistent.
	driftedGen := int64(2)
	seedComponentWithSnapshot(ctx, t, essPool, xid, 411, "multi-view-comp-2", 1, driftedGen, &driftedGen, 1)

	got, found = queryConsistencyInfo(ctx, t, essPool, xid)
	if !found {
		t.Fatalf("expected a view row for Xid %d after drift, got none", xid)
	}
	if got.isConsistent {
		t.Errorf("IsExperimentConsistent = true, want false when 1 of 3 components has drifted")
	}
}
