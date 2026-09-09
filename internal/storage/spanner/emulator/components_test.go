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

package emulator_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/data"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
)

type consistencyResult struct {
	isConsistent bool
	hasPending   bool
}

func setupTestEnv(ctx context.Context, t *testing.T) (*spanner.Client, *store.SpannerStore) {
	t.Helper()
	_, essClient := emulator.Setup(ctx, t)
	return essClient, store.NewSpannerStore(essClient)
}

func seedExperiment(ctx context.Context, t *testing.T, client *spanner.Client, xid int64, activeState essmsgpb.ExperimentStatus_ActiveState) int64 {
	t.Helper()
	testExp := &essmsgpb.Experiment{
		Name:      fmt.Sprintf("experiments/%d", xid),
		Title:     fmt.Sprintf("Test Experiment %d", xid),
		Creator:   "test-user",
		Priority:  100,
		Urls:      []*essmsgpb.Url{},
		Tags:      []string{},
		Notes:     "test-notes",
		Resources: []*essmsgpb.ResourceSet{},
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: activeState,
			LaunchState: essmsgpb.ExperimentStatus_LAUNCHING,
		},
		CreateTime: timestamppb.Now(),
		UpdateTime: timestamppb.Now(),
	}
	expMut, err := spannerdata.ExperimentProtoToInsertMutation(testExp)
	if err != nil {
		t.Fatalf("failed to create experiment mutation for xid %d: %v", xid, err)
	}

	if _, err := client.Apply(ctx, []*spanner.Mutation{expMut}); err != nil {
		t.Fatalf("failed to seed experiment %d: %v", xid, err)
	}

	return data.ReverseBits(xid)
}

func seedComponentSnapshot(ctx context.Context, t *testing.T, client *spanner.Client, bitReversedXid int64, comp *store.ComponentRecord, lockedGen int64) {
	t.Helper()
	compProto := &componentspb.Component{
		Name: naming.FullComponentName(comp.Name),
		Spec: comp.Spec,
	}
	configBytes, err := proto.Marshal(compProto)
	if err != nil {
		t.Fatalf("failed to marshal component proto: %v", err)
	}

	var accessBytes []byte
	if comp.Status != nil {
		accessBytes, err = proto.Marshal(comp.Status)
		if err != nil {
			t.Fatalf("failed to marshal access proto: %v", err)
		}
	}

	mut := spanner.Insert(
		"ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto", "AccessPayload"},
		[]any{bitReversedXid, comp.ComponentID, lockedGen, configBytes, accessBytes},
	)
	if _, err := client.Apply(ctx, []*spanner.Mutation{mut}); err != nil {
		t.Fatalf("failed to insert ExperimentComponentSnapshots: %v", err)
	}
}

func queryConsistencyInfo(ctx context.Context, client *spanner.Client, xid int64) (*consistencyResult, bool, error) {
	stmt := spanner.Statement{
		SQL: "SELECT IsExperimentConsistent, HasPendingReconciliation FROM ExperimentConsistencyInfo WHERE Xid = @xid",
		Params: map[string]any{
			"xid": xid,
		},
	}
	iter := client.Single().Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			return nil, false, nil
		}
		return nil, false, err
	}

	var isConsistent, hasPending bool
	if err := row.ColumnByName("IsExperimentConsistent", &isConsistent); err != nil {
		return nil, false, err
	}
	if err := row.ColumnByName("HasPendingReconciliation", &hasPending); err != nil {
		return nil, false, err
	}

	return &consistencyResult{
		isConsistent: isConsistent,
		hasPending:   hasPending,
	}, true, nil
}

func TestExperimentConsistencyView_Consistent(t *testing.T) {
	ctx := context.Background()
	client, s := setupTestEnv(ctx, t)

	const xid = int64(10001)
	bitReversedXid := seedExperiment(ctx, t, client, xid, essmsgpb.ExperimentStatus_ACTIVE)

	spec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Consistent component",
	}
	_, err := s.SetComponentIntent(ctx, "comp-cons", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	access := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 1,
	}
	comp, err := s.UpdateObservedGeneration(ctx, "comp-cons", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	seedComponentSnapshot(ctx, t, client, bitReversedXid, comp, 1)

	res, found, err := queryConsistencyInfo(ctx, client, xid)
	if err != nil {
		t.Fatalf("queryConsistencyInfo failed: %v", err)
	}
	if !found {
		t.Fatalf("expected ExperimentConsistencyInfo row for Xid %d, got none", xid)
	}
	if !res.isConsistent {
		t.Errorf("expected IsExperimentConsistent == true, got false")
	}
	if res.hasPending {
		t.Errorf("expected HasPendingReconciliation == false, got true")
	}
}

func TestExperimentConsistencyView_GenerationDrift(t *testing.T) {
	ctx := context.Background()
	client, s := setupTestEnv(ctx, t)

	const xid = int64(10002)
	bitReversedXid := seedExperiment(ctx, t, client, xid, essmsgpb.ExperimentStatus_ACTIVE)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	_, err := s.SetComponentIntent(ctx, "comp-drift", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	access := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 1,
	}
	comp, err := s.UpdateObservedGeneration(ctx, "comp-drift", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	seedComponentSnapshot(ctx, t, client, bitReversedXid, comp, 1)

	// Verify initially consistent
	res, found, err := queryConsistencyInfo(ctx, client, xid)
	if err != nil || !found || !res.isConsistent {
		t.Fatalf("expected initial consistent state, got res=%v, found=%v, err=%v", res, found, err)
	}

	// Drift component to generation 2
	spec2 := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v2.0.0",
	}
	_, err = s.SetComponentIntent(ctx, "comp-drift", spec2, nil, 2)
	if err != nil {
		t.Fatalf("SetComponentIntent gen 2 failed: %v", err)
	}
	access2 := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 2,
	}
	_, err = s.UpdateObservedGeneration(ctx, "comp-drift", 2, access2, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration gen 2 failed: %v", err)
	}

	// Verify view now reports generation drift (IsExperimentConsistent == false)
	res, found, err = queryConsistencyInfo(ctx, client, xid)
	if err != nil {
		t.Fatalf("queryConsistencyInfo failed: %v", err)
	}
	if !found {
		t.Fatalf("expected row for Xid %d, got none", xid)
	}
	if res.isConsistent {
		t.Errorf("expected IsExperimentConsistent == false after generation drift, got true")
	}
	if res.hasPending {
		t.Errorf("expected HasPendingReconciliation == false, got true")
	}
}

func TestExperimentConsistencyView_PendingReconciliation(t *testing.T) {
	ctx := context.Background()
	client, s := setupTestEnv(ctx, t)

	const xid = int64(10003)
	bitReversedXid := seedExperiment(ctx, t, client, xid, essmsgpb.ExperimentStatus_ACTIVE)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	// Initial intent creation: ObservedGeneration is NULL
	comp, err := s.SetComponentIntent(ctx, "comp-pending", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	seedComponentSnapshot(ctx, t, client, bitReversedXid, comp, 1)

	res, found, err := queryConsistencyInfo(ctx, client, xid)
	if err != nil {
		t.Fatalf("queryConsistencyInfo failed: %v", err)
	}
	if !found {
		t.Fatalf("expected row for Xid %d, got none", xid)
	}
	if !res.isConsistent {
		t.Errorf("expected IsExperimentConsistent == true for pending reconciliation, got false")
	}
	if !res.hasPending {
		t.Errorf("expected HasPendingReconciliation == true for unobserved component, got false")
	}
}

func TestExperimentConsistencyView_TerminalExperiment(t *testing.T) {
	ctx := context.Background()
	client, s := setupTestEnv(ctx, t)

	const xid = int64(10004)
	bitReversedXid := seedExperiment(ctx, t, client, xid, essmsgpb.ExperimentStatus_INACTIVE)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	comp, err := s.SetComponentIntent(ctx, "comp-term", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}
	seedComponentSnapshot(ctx, t, client, bitReversedXid, comp, 1)

	_, found, err := queryConsistencyInfo(ctx, client, xid)
	if err != nil {
		t.Fatalf("queryConsistencyInfo failed: %v", err)
	}
	if found {
		t.Errorf("expected no rows returned for terminal/inactive experiment, but row was found")
	}
}

func TestExperimentConsistencyView_MultipleComponents(t *testing.T) {
	ctx := context.Background()
	client, s := setupTestEnv(ctx, t)

	const xid = int64(10005)
	bitReversedXid := seedExperiment(ctx, t, client, xid, essmsgpb.ExperimentStatus_ACTIVE)

	compNames := []string{"multi-comp-1", "multi-comp-2", "multi-comp-3"}
	comps := make([]*store.ComponentRecord, len(compNames))

	for i, name := range compNames {
		spec := &componentspb.ComponentSpec{
			Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version: fmt.Sprintf("v1.%d.0", i),
		}
		_, err := s.SetComponentIntent(ctx, name, spec, nil, 1)
		if err != nil {
			t.Fatalf("SetComponentIntent %s failed: %v", name, err)
		}
		access := &componentspb.ComponentAccessStatus{
			State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			ObservedGeneration: 1,
		}
		comp, err := s.UpdateObservedGeneration(ctx, name, 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
		if err != nil {
			t.Fatalf("UpdateObservedGeneration %s failed: %v", name, err)
		}
		seedComponentSnapshot(ctx, t, client, bitReversedXid, comp, 1)
		comps[i] = comp
	}

	// When all components are observed and match locked generation -> consistent
	res, found, err := queryConsistencyInfo(ctx, client, xid)
	if err != nil || !found {
		t.Fatalf("queryConsistencyInfo failed: err=%v, found=%v", err, found)
	}
	if !res.isConsistent || res.hasPending {
		t.Errorf("expected consistent=true, pending=false; got %v", res)
	}

	// Update only one component (multi-comp-2) to generation 2
	specUpdate := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v2.0.0",
	}
	_, err = s.SetComponentIntent(ctx, "multi-comp-2", specUpdate, nil, 2)
	if err != nil {
		t.Fatalf("SetComponentIntent multi-comp-2 gen 2 failed: %v", err)
	}
	accessUpdate := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 2,
	}
	_, err = s.UpdateObservedGeneration(ctx, "multi-comp-2", 2, accessUpdate, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration multi-comp-2 gen 2 failed: %v", err)
	}

	// Now the experiment must be reported as inconsistent
	res, found, err = queryConsistencyInfo(ctx, client, xid)
	if err != nil || !found {
		t.Fatalf("queryConsistencyInfo failed: err=%v, found=%v", err, found)
	}
	if res.isConsistent {
		t.Errorf("expected IsExperimentConsistent == false when one component drifts, got true")
	}
}

func TestComponentDeactivation_DependentCascade(t *testing.T) {
	ctx := context.Background()
	_, s := setupTestEnv(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}

	// Seed component A
	_, err := s.SetComponentIntent(ctx, "cascade-a", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent cascade-a failed: %v", err)
	}
	access := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 1,
	}
	_, err = s.UpdateObservedGeneration(ctx, "cascade-a", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration cascade-a failed: %v", err)
	}

	// Seed component B depending on A
	labelsB := map[string]string{"xmc.dev/depends-on": "cascade-a"}
	_, err = s.SetComponentIntent(ctx, "cascade-b", spec, labelsB, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent cascade-b failed: %v", err)
	}
	_, err = s.UpdateObservedGeneration(ctx, "cascade-b", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration cascade-b failed: %v", err)
	}

	// Without requestCascade: should reject deactivation due to active dependents
	_, err = s.PrepareDeactivation(ctx, "cascade-a", false)
	if err == nil {
		t.Fatalf("expected error from PrepareDeactivation without cascade, got nil")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}

	// With requestCascade: should succeed and transition both to DISABLING
	res, err := s.PrepareDeactivation(ctx, "cascade-a", true)
	if err != nil {
		t.Fatalf("PrepareDeactivation with cascade failed: %v", err)
	}
	if !res.IsSafeToDeactivate {
		t.Errorf("expected IsSafeToDeactivate == true")
	}
	expectedCascaded := []string{"cascade-b"}
	if diff := cmp.Diff(expectedCascaded, res.CascadedDeactivations); diff != "" {
		t.Errorf("CascadedDeactivations mismatch (-want +got):\n%s", diff)
	}

	// Verify both A and B are in COMPONENT_STATE_DISABLING
	recA, err := s.GetComponent(ctx, "cascade-a")
	if err != nil {
		t.Fatalf("GetComponent cascade-a failed: %v", err)
	}
	if recA.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
		t.Errorf("expected cascade-a state DISABLING, got %v", recA.State)
	}

	recB, err := s.GetComponent(ctx, "cascade-b")
	if err != nil {
		t.Fatalf("GetComponent cascade-b failed: %v", err)
	}
	if recB.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
		t.Errorf("expected cascade-b state DISABLING, got %v", recB.State)
	}
}

func TestComponentDeactivation_CompleteTwoPhase(t *testing.T) {
	ctx := context.Background()
	_, s := setupTestEnv(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}

	_, err := s.SetComponentIntent(ctx, "two-phase-a", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent two-phase-a failed: %v", err)
	}
	access := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 1,
	}
	_, err = s.UpdateObservedGeneration(ctx, "two-phase-a", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration two-phase-a failed: %v", err)
	}

	labelsB := map[string]string{"xmc.dev/depends-on": "two-phase-a"}
	_, err = s.SetComponentIntent(ctx, "two-phase-b", spec, labelsB, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent two-phase-b failed: %v", err)
	}
	_, err = s.UpdateObservedGeneration(ctx, "two-phase-b", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration two-phase-b failed: %v", err)
	}

	// Prepare deactivation for both
	_, err = s.PrepareDeactivation(ctx, "two-phase-a", true)
	if err != nil {
		t.Fatalf("PrepareDeactivation failed: %v", err)
	}

	// Attempt CompleteDeactivation on parent A while child B is still DISABLING: should fail
	err = s.CompleteDeactivation(ctx, "two-phase-a", false)
	if err == nil {
		t.Fatalf("expected error completing deactivation on A while B is DISABLING, got nil")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}

	// Complete deactivation on child B first
	err = s.CompleteDeactivation(ctx, "two-phase-b", false)
	if err != nil {
		t.Fatalf("CompleteDeactivation on B failed: %v", err)
	}
	recB, err := s.GetComponent(ctx, "two-phase-b")
	if err != nil {
		t.Fatalf("GetComponent two-phase-b failed: %v", err)
	}
	if recB.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("expected two-phase-b state DISABLED, got %v", recB.State)
	}

	// Now complete deactivation on parent A
	err = s.CompleteDeactivation(ctx, "two-phase-a", false)
	if err != nil {
		t.Fatalf("CompleteDeactivation on A failed after B disabled: %v", err)
	}
	recA, err := s.GetComponent(ctx, "two-phase-a")
	if err != nil {
		t.Fatalf("GetComponent two-phase-a failed: %v", err)
	}
	if recA.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("expected two-phase-a state DISABLED, got %v", recA.State)
	}
}

func TestSoftDeletion_FKIntegrityPreserved(t *testing.T) {
	ctx := context.Background()
	client, s := setupTestEnv(ctx, t)

	const xid = int64(10008)
	bitReversedXid := seedExperiment(ctx, t, client, xid, essmsgpb.ExperimentStatus_ACTIVE)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	comp, err := s.SetComponentIntent(ctx, "fk-comp-c", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}
	access := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 1,
	}
	comp, err = s.UpdateObservedGeneration(ctx, "fk-comp-c", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	seedComponentSnapshot(ctx, t, client, bitReversedXid, comp, 1)

	// Attempt hard SQL DELETE on Components: Spanner must reject with foreign key constraint violation
	_, err = client.Apply(ctx, []*spanner.Mutation{
		spanner.Delete("Components", spanner.Key{comp.ComponentID}),
	})
	if err == nil {
		t.Fatalf("expected hard SQL DELETE to fail due to foreign key violation, got nil")
	}
	errStr := strings.ToLower(err.Error())
	if !strings.Contains(errStr, "foreign key") && !strings.Contains(errStr, "fk_snapshots_component") && status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected foreign key violation error, got: %v", err)
	}

	// Soft-deletion via store.ExecuteDeletion
	err = s.ExecuteDeletion(ctx, "fk-comp-c", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false)
	if err != nil {
		t.Fatalf("ExecuteDeletion failed: %v", err)
	}

	// Verify row still exists in Components table with State = 5 (DELETED) and ActiveName IS NULL
	stmtComp := spanner.Statement{
		SQL: "SELECT State, ActiveName FROM Components WHERE ComponentId = @id",
		Params: map[string]any{
			"id": comp.ComponentID,
		},
	}
	iterComp := client.Single().Query(ctx, stmtComp)
	defer iterComp.Stop()
	rowComp, err := iterComp.Next()
	if err != nil {
		t.Fatalf("failed to query soft-deleted component row: %v", err)
	}

	var state int64
	var activeName spanner.NullString
	if err := rowComp.ColumnByName("State", &state); err != nil {
		t.Fatalf("failed to read State column: %v", err)
	}
	if err := rowComp.ColumnByName("ActiveName", &activeName); err != nil {
		t.Fatalf("failed to read ActiveName column: %v", err)
	}

	if state != int64(componentspb.ComponentState_COMPONENT_STATE_DELETED) {
		t.Errorf("expected State == 5 (DELETED), got %d", state)
	}
	if activeName.Valid {
		t.Errorf("expected ActiveName IS NULL, got %q", activeName.StringVal)
	}

	// Verify ExperimentComponentSnapshots row still exists (FK intact)
	stmtSnap := spanner.Statement{
		SQL: "SELECT COUNT(1) FROM ExperimentComponentSnapshots WHERE ComponentId = @id",
		Params: map[string]any{
			"id": comp.ComponentID,
		},
	}
	iterSnap := client.Single().Query(ctx, stmtSnap)
	defer iterSnap.Stop()
	rowSnap, err := iterSnap.Next()
	if err != nil {
		t.Fatalf("failed to query snapshots count: %v", err)
	}
	var snapCount int64
	if err := rowSnap.Column(0, &snapCount); err != nil {
		t.Fatalf("failed to read snapshot count: %v", err)
	}
	if snapCount != 1 {
		t.Errorf("expected snapshot count 1, got %d", snapCount)
	}

	// Verify ExperimentConsistencyInfo view now reports IsExperimentConsistent == false
	res, found, err := queryConsistencyInfo(ctx, client, xid)
	if err != nil || !found {
		t.Fatalf("queryConsistencyInfo failed: err=%v, found=%v", err, found)
	}
	if res.isConsistent {
		t.Errorf("expected IsExperimentConsistent == false for soft-deleted component snapshot, got true")
	}
}

func TestSoftDeletion_ActiveNameReleased(t *testing.T) {
	ctx := context.Background()
	client, s := setupTestEnv(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}

	// Create component "reusable-comp"
	rec1, err := s.SetComponentIntent(ctx, "reusable-comp", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent reusable-comp 1 failed: %v", err)
	}
	oldID := rec1.ComponentID

	// Soft-delete it
	err = s.ExecuteDeletion(ctx, "reusable-comp", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false)
	if err != nil {
		t.Fatalf("ExecuteDeletion reusable-comp failed: %v", err)
	}

	// Re-create component with the exact same name
	rec2, err := s.SetComponentIntent(ctx, "reusable-comp", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent reusable-comp 2 failed: %v", err)
	}
	newID := rec2.ComponentID

	if oldID == newID {
		t.Errorf("expected different ComponentId for re-created component, got %d", newID)
	}

	// Query both rows in Components table directly
	stmt := spanner.Statement{
		SQL: "SELECT ComponentId, ActiveName, State FROM Components WHERE Name = 'reusable-comp' ORDER BY ComponentId ASC",
	}
	iter := client.Single().Query(ctx, stmt)
	defer iter.Stop()

	type compRow struct {
		id         int64
		activeName spanner.NullString
		state      int64
	}
	var rows []compRow
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			t.Fatalf("failed iterating component rows: %v", err)
		}
		var r compRow
		if err := row.ColumnByName("ComponentId", &r.id); err != nil {
			t.Fatalf("failed reading id: %v", err)
		}
		if err := row.ColumnByName("ActiveName", &r.activeName); err != nil {
			t.Fatalf("failed reading activeName: %v", err)
		}
		if err := row.ColumnByName("State", &r.state); err != nil {
			t.Fatalf("failed reading state: %v", err)
		}
		rows = append(rows, r)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows for Name = 'reusable-comp', got %d", len(rows))
	}

	for _, r := range rows {
		switch r.id {
		case oldID:
			if r.activeName.Valid {
				t.Errorf("expected old component activeName to be NULL, got %q", r.activeName.StringVal)
			}
			if r.state != 5 {
				t.Errorf("expected old component state 5 (DELETED), got %d", r.state)
			}
		case newID:
			if !r.activeName.Valid || r.activeName.StringVal != "reusable-comp" {
				t.Errorf("expected new component activeName 'reusable-comp', got %v", r.activeName)
			}
			if r.state == 5 {
				t.Errorf("expected new component state != 5, got %d", r.state)
			}
		default:
			t.Errorf("unexpected component ID: %d", r.id)
		}
	}
}

func TestHistoricalSnapshotResolution_SoftDeletedComponent(t *testing.T) {
	ctx := context.Background()
	client, s := setupTestEnv(ctx, t)

	const xid = int64(10010)
	bitReversedXid := seedExperiment(ctx, t, client, xid, essmsgpb.ExperimentStatus_ACTIVE)

	_, err := s.SetComponentIntent(ctx, "dep-target", &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent for dep-target failed: %v", err)
	}
	_, err = s.UpdateObservedGeneration(ctx, "dep-target", 1, &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 1,
	}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration for dep-target failed: %v", err)
	}

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0-historical",
	}
	_, err = s.SetComponentIntent(ctx, "hist-soft-del", spec, map[string]string{"xmc.dev/depends-on": "dep-target"}, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	access := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 1,
		Coordinates: &componentspb.ResolvedComponentCoordinates{
			ClusterName: "gke-cluster-west",
			Namespace:   "default",
			ServiceName: "hist-svc",
			Port:        8080,
		},
	}
	comp, err := s.UpdateObservedGeneration(ctx, "hist-soft-del", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	// Seed snapshot in ExperimentComponentSnapshots at generation 1
	seedComponentSnapshot(ctx, t, client, bitReversedXid, comp, 1)

	// Soft-delete the component
	err = s.ExecuteDeletion(ctx, "hist-soft-del", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false)
	if err != nil {
		t.Fatalf("ExecuteDeletion failed: %v", err)
	}

	// GetComponent returns NotFound
	if _, err := s.GetComponent(ctx, "hist-soft-del"); status.Code(err) != codes.NotFound {
		t.Errorf("expected GetComponent on soft-deleted component to return NotFound, got %v", err)
	}

	// ResolveComponentSnapshot for generation 1 should succeed using historical snapshot table
	histRec, histCoords, err := s.ResolveComponentSnapshot(ctx, "hist-soft-del", 1)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot for soft-deleted component historical gen 1 failed: %v", err)
	}
	if histRec.Name != "hist-soft-del" {
		t.Errorf("expected Name 'hist-soft-del', got %q", histRec.Name)
	}
	if histRec.Generation != 1 {
		t.Errorf("expected Generation 1, got %d", histRec.Generation)
	}
	if histRec.Spec.GetVersion() != "v1.0.0-historical" {
		t.Errorf("expected version v1.0.0-historical, got %q", histRec.Spec.GetVersion())
	}
	if len(histRec.Dependencies) != 1 || histRec.Dependencies[0] != "dep-target" {
		t.Errorf("expected dependencies [dep-target], got %v", histRec.Dependencies)
	}
	if histRec.ObservedGeneration == nil || *histRec.ObservedGeneration != 1 {
		t.Errorf("expected ObservedGeneration 1, got %v", histRec.ObservedGeneration)
	}
	if histCoords.GetServiceName() != "hist-svc" || histCoords.GetPort() != 8080 {
		t.Errorf("expected coordinates hist-svc:8080, got %v", histCoords)
	}
}

func Test2PL_ConcurrentGenerationUpdatesAndDriftView(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	client, s := setupTestEnv(ctx, t)

	const xid = int64(10011)
	bitReversedXid := seedExperiment(ctx, t, client, xid, essmsgpb.ExperimentStatus_ACTIVE)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	_, err := s.SetComponentIntent(ctx, "concurrent-drift", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}
	access := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 1,
	}
	comp, err := s.UpdateObservedGeneration(ctx, "concurrent-drift", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	seedComponentSnapshot(ctx, t, client, bitReversedXid, comp, 1)

	const numWriters = 10
	const numReaders = 10
	const writerIterations = 5
	const readerIterations = 10

	var wg sync.WaitGroup
	wg.Add(numWriters + numReaders)

	// Launch concurrent writers
	for w := 0; w < numWriters; w++ {
		go func(workerID int) {
			defer wg.Done()
			for it := 0; it < writerIterations; it++ {
				wSpec := &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: fmt.Sprintf("v%d.%d.0", workerID, it),
				}
				rec, err := s.SetComponentIntent(ctx, "concurrent-drift", wSpec, nil, 0)
				if err != nil {
					t.Errorf("writer %d iter %d SetComponentIntent failed: %v", workerID, it, err)
					return
				}
				wAccess := &componentspb.ComponentAccessStatus{
					State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
					ObservedGeneration: rec.Generation,
				}
				_, err = s.UpdateObservedGeneration(ctx, "concurrent-drift", rec.Generation, wAccess, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
				if err != nil {
					t.Errorf("writer %d iter %d UpdateObservedGeneration failed: %v", workerID, it, err)
					return
				}
			}
		}(w)
	}

	// Launch concurrent readers querying ExperimentConsistencyInfo
	for r := 0; r < numReaders; r++ {
		go func(readerID int) {
			defer wg.Done()
			for it := 0; it < readerIterations; it++ {
				res, found, err := queryConsistencyInfo(ctx, client, xid)
				if err != nil {
					t.Errorf("reader %d iter %d queryConsistencyInfo failed: %v", readerID, it, err)
					return
				}
				if !found {
					t.Errorf("reader %d iter %d row not found", readerID, it)
					return
				}
				// View consistency invariants under atomic read for single-component experiment:
				// When hasPending is true, ObservedGeneration is NULL, so isConsistent must be true.
				if res.hasPending && !res.isConsistent {
					t.Errorf("reader %d iter %d: illegal state pending=true but consistent=false", readerID, it)
				}
				select {
				case <-ctx.Done():
					return
				default:
				}
			}
		}(r)
	}

	wg.Wait()

	// Final verification: component must be valid and readable
	finalRec, err := s.GetComponent(ctx, "concurrent-drift")
	if err != nil {
		t.Fatalf("final GetComponent failed: %v", err)
	}
	if finalRec.Generation <= 1 {
		t.Errorf("expected final Generation > 1, got %d", finalRec.Generation)
	}
	if finalRec.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected final State ENABLED, got %v", finalRec.State)
	}
}

func Test2PL_ConcurrentDeactivationAndIntentConflict(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, s := setupTestEnv(ctx, t)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}
	_, err := s.SetComponentIntent(ctx, "conflict-comp", spec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}
	access := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 1,
	}
	_, err = s.UpdateObservedGeneration(ctx, "conflict-comp", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	const concurrentPairs = 4
	var wg sync.WaitGroup
	wg.Add(concurrentPairs * 2)

	for i := 0; i < concurrentPairs; i++ {
		// Goroutine 1: attempts deactivation
		go func(workerID int) {
			defer wg.Done()
			if _, err := s.PrepareDeactivation(ctx, "conflict-comp", false); err != nil {
				t.Logf("worker %d deactivation returned expected/handled error: %v", workerID, err)
			}
		}(i)

		// Goroutine 2: attempts intent update
		go func(workerID int) {
			defer wg.Done()
			intentSpec := &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version: fmt.Sprintf("v1.%d.0", workerID),
			}
			if _, err := s.SetComponentIntent(ctx, "conflict-comp", intentSpec, nil, 0); err != nil {
				t.Logf("worker %d intent update returned expected/handled error: %v", workerID, err)
			}
		}(i)
	}

	wg.Wait()

	// Verify Spanner 2PL transaction retry / lock serialization properly serialized
	// without database corruption: record must be intact and valid
	finalRec, err := s.GetComponent(ctx, "conflict-comp")
	if err != nil {
		t.Fatalf("GetComponent failed after concurrent deactivation and intent conflict: %v", err)
	}
	if finalRec.Name != "conflict-comp" {
		t.Errorf("expected Name 'conflict-comp', got %q", finalRec.Name)
	}
	if finalRec.State == componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED {
		t.Errorf("unexpected UNSPECIFIED state after concurrent conflict")
	}
}
