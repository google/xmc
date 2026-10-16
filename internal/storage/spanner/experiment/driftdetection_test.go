// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package experiment_test

import (
	"context"
	"testing"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/storage/spanner/utils"
	"xmanager_cloud/internal/validator"
)

func newTestStorage(t *testing.T, essClient *cloudspanner.Client) *spanner.Storage {
	t.Helper()
	st, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	return st
}

func TestDriftDetection_ConsistentSnapshots(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st := newTestStorage(t, essClient)

	const xid = int64(1001)
	createdExp := spanner.CreateBlankExperiment(ctx, t, st, xid)
	observed := int64(1)
	seedComponentWithSnapshot(ctx, t, essClient, xid, int64(201), "clickhouse", 1, 1, &observed, 1)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	diffs, err := st.FetchDriftedComponents(ctx, parsedXID, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents failed: %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("FetchDriftedComponents = %v, want empty", diffs)
	}
}

func TestDriftDetection_GenerationDiverged(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st := newTestStorage(t, essClient)

	const xid = int64(1002)
	createdExp := spanner.CreateBlankExperiment(ctx, t, st, xid)

	// Component is at generation 2 with observed generation 2, but experiment snapshot locked generation 1.
	observed := int64(2)
	seedComponentWithSnapshot(ctx, t, essClient, xid, int64(202), "redpanda", 1, 2, &observed, 1)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	diffs, err := st.FetchDriftedComponents(ctx, parsedXID, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents failed: %v", err)
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
		t.Errorf("FetchDriftedComponents mismatch (-want +got):\n%s", diff)
	}
}

func TestDriftDetection_ComponentSoftDeleted(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st := newTestStorage(t, essClient)

	const xid = int64(1003)
	createdExp := spanner.CreateBlankExperiment(ctx, t, st, xid)

	// State = 5 indicates COMPONENT_STATE_DELETED.
	observed := int64(2)
	seedComponentWithSnapshot(ctx, t, essClient, xid, int64(203), "clickhouse", storage.ComponentStateDeleted, 2, &observed, 1)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	diffs, err := st.FetchDriftedComponents(ctx, parsedXID, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents failed: %v", err)
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
		t.Errorf("FetchDriftedComponents mismatch (-want +got):\n%s", diff)
	}
}

func TestDriftDetection_FetchDriftedComponentsLimit(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st := newTestStorage(t, essClient)

	const xid = int64(1010)
	createdExp := spanner.CreateBlankExperiment(ctx, t, st, xid)

	observed := int64(2)
	seedComponentWithSnapshot(ctx, t, essClient, xid, int64(301), "comp-b", 1, 2, &observed, 1)
	seedComponentWithSnapshot(ctx, t, essClient, xid, int64(302), "comp-a", 1, 2, &observed, 1)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	diffs, err := st.FetchDriftedComponents(ctx, parsedXID, 1)
	if err != nil {
		t.Fatalf("FetchDriftedComponents failed: %v", err)
	}
	if len(diffs) != 1 || diffs[0].Name == nil || *diffs[0].Name != "comp-a" {
		t.Errorf("FetchDriftedComponents(limit=1) = %v, want single entry for comp-a", diffs)
	}
}

func TestDriftDetection_TerminalExperimentFiltered(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st := newTestStorage(t, essClient)

	const xid = int64(1004)
	expProto := spanner.BlankExperimentProto(xid)
	expProto.Status.ActiveState = essmsgpb.ExperimentStatus_INACTIVE
	createdExp := spanner.CreateAndStoreExperiment(ctx, t, st, expProto)

	// Live component diverged.
	observed := int64(2)
	seedComponentWithSnapshot(ctx, t, essClient, xid, int64(204), "ingestion-gateway", 1, 2, &observed, 1)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	diffs, err := st.FetchDriftedComponents(ctx, parsedXID, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents failed: %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("FetchDriftedComponents = %v, want empty for inactive experiment excluded from view", diffs)
	}
}

func TestDriftDetection_PendingReconciliationNotDrifted(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st := newTestStorage(t, essClient)

	const xid = int64(1005)
	createdExp := spanner.CreateBlankExperiment(ctx, t, st, xid)

	// ObservedGeneration is NULL (pending initial GKE reconciliation).
	seedComponentWithSnapshot(ctx, t, essClient, xid, int64(205), "pending-comp", 1, 1, nil, 1)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	diffs, err := st.FetchDriftedComponents(ctx, parsedXID, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents failed: %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("FetchDriftedComponents = %v, want empty during pending reconciliation", diffs)
	}
}

func TestDriftDetection_NoSnapshots_Consistent(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st := newTestStorage(t, essClient)

	const xid = int64(1006)
	createdExp := spanner.CreateBlankExperiment(ctx, t, st, xid)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	diffs, err := st.FetchDriftedComponents(ctx, parsedXID, 0)
	if err != nil {
		t.Fatalf("FetchDriftedComponents failed: %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("FetchDriftedComponents = %v, want empty for experiment with no snapshots", diffs)
	}
}

// The tests below assert the SQL contract of the ExperimentConsistencyInfo view directly:
// the HasPendingReconciliation column, the view's `WHERE e.ActiveState = 1` predicate, and
// NOT EXISTS aggregation across multiple components.

// consistencyResult mirrors one row of the ExperimentConsistencyInfo view.
type consistencyResult struct {
	isConsistent bool
	hasPending   bool
}

// queryConsistencyInfo reads the ExperimentConsistencyInfo row for xid, reporting whether the view
// emitted a row at all.
func queryConsistencyInfo(ctx context.Context, t *testing.T, client *cloudspanner.Client, xid int64) (consistencyResult, bool) {
	t.Helper()
	stmt := cloudspanner.Statement{
		SQL:    "SELECT IsExperimentConsistent, HasPendingReconciliation FROM ExperimentConsistencyInfo WHERE Xid = @xid",
		Params: map[string]any{"xid": xid},
	}
	iter := client.Single().Query(ctx, stmt)
	defer iter.Stop()

	var (
		got   consistencyResult
		found bool
	)
	err := iter.Do(func(row *cloudspanner.Row) error {
		if err := row.ColumnByName("IsExperimentConsistent", &got.isConsistent); err != nil {
			return err
		}
		if err := row.ColumnByName("HasPendingReconciliation", &got.hasPending); err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		t.Fatalf("query ExperimentConsistencyInfo failed: %v", err)
	}
	return got, found
}

// seedComponentWithSnapshot inserts a Components row and an ExperimentComponentSnapshots row
// binding it to xid. A nil observedGen leaves ObservedGeneration NULL (pending reconciliation).
func seedComponentWithSnapshot(ctx context.Context, t *testing.T, client *cloudspanner.Client, xid, componentID int64, name string, state, generation int64, observedGen *int64, lockedGen int64) {
	t.Helper()
	cols := []string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "State", "Generation", "CreateTime", "UpdateTime"}
	vals := []any{componentID, name, "storage", []byte{}, state, generation, cloudspanner.CommitTimestamp, cloudspanner.CommitTimestamp}
	if observedGen != nil {
		cols = append(cols, "ObservedGeneration")
		vals = append(vals, *observedGen)
	}

	componentMutation := cloudspanner.InsertOrUpdate("Components", cols, vals)
	snapshotMutation := cloudspanner.InsertOrUpdate("ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto"},
		[]any{spannerutils.ReverseBits(xid), componentID, lockedGen, []byte{}},
	)
	if _, err := client.Apply(ctx, []*cloudspanner.Mutation{componentMutation, snapshotMutation}); err != nil {
		t.Fatalf("Apply mutations for component %q failed: %v", name, err)
	}
}

func TestConsistencyView_HasPendingReconciliationColumn(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st := newTestStorage(t, essClient)

	const xid = int64(1200)
	spanner.CreateBlankExperiment(ctx, t, st, xid)

	// ObservedGeneration NULL: awaiting the first GKE reconciliation, not drifted.
	seedComponentWithSnapshot(ctx, t, essClient, xid, int64(400), "pending-view-comp", 1, 1, nil, 1)

	got, found := queryConsistencyInfo(ctx, t, essClient, xid)
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
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st := newTestStorage(t, essClient)

	const xid = int64(1201)
	expProto := spanner.BlankExperimentProto(xid)
	expProto.Status.ActiveState = essmsgpb.ExperimentStatus_INACTIVE
	spanner.CreateAndStoreExperiment(ctx, t, st, expProto)

	// Drifted component (live generation 2 vs locked generation 1) that the view must still ignore
	// because the owning experiment is terminal.
	observedGen := int64(2)
	seedComponentWithSnapshot(ctx, t, essClient, xid, int64(401), "terminal-view-comp", 1, 2, &observedGen, 1)

	if _, found := queryConsistencyInfo(ctx, t, essClient, xid); found {
		t.Errorf("view returned a row for terminal experiment %d, want it excluded by WHERE e.ActiveState = 1", xid)
	}
}

func TestConsistencyView_MultipleComponentsAggregation(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st := newTestStorage(t, essClient)

	const xid = int64(1202)
	spanner.CreateBlankExperiment(ctx, t, st, xid)

	// Three components, all reconciled at the generation their snapshot locked.
	observedGen := int64(1)
	for i, name := range []string{"multi-view-comp-1", "multi-view-comp-2", "multi-view-comp-3"} {
		seedComponentWithSnapshot(ctx, t, essClient, xid, int64(410+i), name, 1, 1, &observedGen, 1)
	}

	got, found := queryConsistencyInfo(ctx, t, essClient, xid)
	if !found {
		t.Fatalf("expected a view row for Xid %d, got none", xid)
	}
	if !got.isConsistent || got.hasPending {
		t.Errorf("got isConsistent=%v hasPending=%v, want true/false with all components reconciled", got.isConsistent, got.hasPending)
	}

	// Drift exactly one of the three: NOT EXISTS must flip the whole experiment inconsistent.
	driftedGen := int64(2)
	drift := cloudspanner.InsertOrUpdate("Components",
		[]string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "State", "Generation", "ObservedGeneration", "CreateTime", "UpdateTime"},
		[]any{int64(411), "multi-view-comp-2", "storage", []byte{}, int64(1), driftedGen, driftedGen, cloudspanner.CommitTimestamp, cloudspanner.CommitTimestamp},
	)
	if _, err := essClient.Apply(ctx, []*cloudspanner.Mutation{drift}); err != nil {
		t.Fatalf("Apply drift mutation failed: %v", err)
	}

	got, found = queryConsistencyInfo(ctx, t, essClient, xid)
	if !found {
		t.Fatalf("expected a view row for Xid %d after drift, got none", xid)
	}
	if got.isConsistent {
		t.Errorf("IsExperimentConsistent = true, want false when 1 of 3 components has drifted")
	}
}
