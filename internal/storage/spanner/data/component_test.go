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

package spannerdata

import (
	"errors"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/pkg/components/store"
)

func mustMarshalPayloads(t *testing.T, name string, spec *componentspb.ComponentSpec, labels map[string]string, access *componentspb.ComponentAccessStatus) ([]byte, []byte) {
	t.Helper()
	cfgBytes, accessBytes, err := data.MarshalComponentPayloads(name, spec, labels, access)
	if err != nil {
		t.Fatalf("MarshalComponentPayloads(%q) failed: %v", name, err)
	}
	return cfgBytes, accessBytes
}

func TestComponentInsertMutations(t *testing.T) {
	if _, err := ComponentInsertMutations(nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ComponentInsertMutations(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}

	wrapperSpec := &componentspb.ComponentSpec{
		Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{CrdKind: "RayCluster"},
	}
	rec := &store.ComponentRecord{
		ComponentID:  1,
		Name:         "ray",
		Spec:         wrapperSpec,
		State:        componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Generation:   1,
		Dependencies: []string{"db"},
		Labels:       map[string]string{"env": "prod"},
	}
	if _, err := ComponentInsertMutations(rec, nil); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("ComponentInsertMutations(missing dep) error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}

	cfgBytes, _ := mustMarshalPayloads(t, rec.Name, rec.Spec, rec.Labels, nil)
	want := []*spanner.Mutation{
		spanner.Insert(
			"Components",
			[]string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "AccessPayload", "State", "Generation", "CreateTime", "UpdateTime"},
			[]any{
				int64(1), "ray", spanner.NullString{StringVal: "RayCluster", Valid: true},
				cfgBytes, []byte(nil), int64(rec.State), int64(1), spanner.CommitTimestamp, spanner.CommitTimestamp,
			},
		),
		spanner.Insert("ComponentDependencies", []string{"ComponentId", "DependencyComponentId"}, []any{int64(1), int64(2)}),
	}
	got, err := ComponentInsertMutations(rec, map[string]int64{"db": 2})
	if err != nil {
		t.Fatalf("ComponentInsertMutations() failed: %v", err)
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("ComponentInsertMutations() mismatch (-want +got):\n%s", diff)
	}
}

func TestComponentIntentUpdateColumnsAndMutations(t *testing.T) {
	if _, err := ComponentIntentUpdateMutations(nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ComponentIntentUpdateMutations(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}

	wrapperSpec := &componentspb.ComponentSpec{
		Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{CrdKind: "RayCluster"},
	}
	access := &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION}
	wrapperCfg, wrapperAccess := mustMarshalPayloads(t, "ray", wrapperSpec, map[string]string{"k": "v"}, access)
	emptyCfg, _ := mustMarshalPayloads(t, "bare", nil, nil, nil)

	tests := []struct {
		name     string
		rec      *store.ComponentRecord
		wantCols map[string]any
	}{
		{
			name: "3p wrapper with status",
			rec: &store.ComponentRecord{
				ComponentID: 1,
				Name:        "ray",
				Spec:        wrapperSpec,
				Status:      access,
				State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
				Generation:  2,
				Labels:      map[string]string{"k": "v"},
			},
			wantCols: map[string]any{
				"ComponentId":    int64(1),
				"ComponentClass": spanner.NullString{StringVal: "RayCluster", Valid: true},
				"ConfigProto":    wrapperCfg,
				"AccessPayload":  wrapperAccess,
				"State":          int64(componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION),
				"Generation":     int64(2),
				"UpdateTime":     spanner.CommitTimestamp,
			},
		},
		{
			name: "nil spec and nil access clear nullable columns",
			rec: &store.ComponentRecord{
				ComponentID: 3,
				Name:        "bare",
				State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
				Generation:  1,
			},
			wantCols: map[string]any{
				"ComponentId":    int64(3),
				"ComponentClass": spanner.NullString{},
				"ConfigProto":    emptyCfg,
				"AccessPayload":  []byte(nil),
				"State":          int64(componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION),
				"Generation":     int64(1),
				"UpdateTime":     spanner.CommitTimestamp,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := componentIntentUpdateColumns(tt.rec)
			if err != nil {
				t.Fatalf("componentIntentUpdateColumns() failed: %v", err)
			}
			if diff := cmp.Diff(tt.wantCols, got); diff != "" {
				t.Errorf("componentIntentUpdateColumns() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	recWithDep := &store.ComponentRecord{ComponentID: 1, Name: "ray", Spec: wrapperSpec, Dependencies: []string{"db"}}
	if _, err := ComponentIntentUpdateMutations(recWithDep, nil); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("ComponentIntentUpdateMutations(missing dep) error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	muts, err := ComponentIntentUpdateMutations(recWithDep, map[string]int64{"db": 2})
	if err != nil || len(muts) != 3 {
		t.Fatalf("ComponentIntentUpdateMutations() = (%d muts, %v), want (3 muts, nil)", len(muts), err)
	}
	wantDepMuts := []*spanner.Mutation{
		spanner.Delete("ComponentDependencies", spanner.KeyRange{Start: spanner.Key{int64(1)}, End: spanner.Key{int64(1)}, Kind: spanner.ClosedClosed}),
		spanner.Insert("ComponentDependencies", []string{"ComponentId", "DependencyComponentId"}, []any{int64(1), int64(2)}),
	}
	if diff := cmp.Diff(wantDepMuts, muts[1:], cmp.AllowUnexported(spanner.Mutation{}, spanner.KeyRange{})); diff != "" {
		t.Errorf("ComponentIntentUpdateMutations() dependency mutations mismatch (-want +got):\n%s", diff)
	}
}

func TestComponentStateAndObservationUpdateColumns(t *testing.T) {
	if _, err := ComponentObservationUpdateMutation(nil, false); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ComponentObservationUpdateMutation(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}

	access := &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED, HealthMessage: "ok"}
	accessBytes, err := data.MarshalAccessStatus(access)
	if err != nil {
		t.Fatalf("MarshalAccessStatus() failed: %v", err)
	}

	stateTests := []struct {
		name     string
		access   *componentspb.ComponentAccessStatus
		wantCols map[string]any
	}{
		{
			name:   "nil access clears column",
			access: nil,
			wantCols: map[string]any{
				"ComponentId":   int64(7),
				"State":         int64(componentspb.ComponentState_COMPONENT_STATE_DISABLING),
				"AccessPayload": []byte(nil),
				"UpdateTime":    spanner.CommitTimestamp,
			},
		},
		{
			name:   "non-nil access writes payload",
			access: access,
			wantCols: map[string]any{
				"ComponentId":   int64(7),
				"State":         int64(componentspb.ComponentState_COMPONENT_STATE_DISABLING),
				"AccessPayload": accessBytes,
				"UpdateTime":    spanner.CommitTimestamp,
			},
		},
	}
	for _, tt := range stateTests {
		t.Run("state/"+tt.name, func(t *testing.T) {
			got, err := componentStateUpdateColumns(7, componentspb.ComponentState_COMPONENT_STATE_DISABLING, tt.access)
			if err != nil {
				t.Fatalf("componentStateUpdateColumns() failed: %v", err)
			}
			if diff := cmp.Diff(tt.wantCols, got); diff != "" {
				t.Errorf("componentStateUpdateColumns() mismatch (-want +got):\n%s", diff)
			}
		})
	}
	if m, err := ComponentStateUpdateMutation(7, componentspb.ComponentState_COMPONENT_STATE_DISABLING, nil); err != nil || m == nil {
		t.Errorf("ComponentStateUpdateMutation() = (%v, %v), want (non-nil, nil)", m, err)
	}

	obsGen := int64(2)
	obsTests := []struct {
		name        string
		rec         *store.ComponentRecord
		updateState bool
		wantCols    map[string]any
	}{
		{
			name:        "updateState false omits State and nil clears nullable columns",
			rec:         &store.ComponentRecord{ComponentID: 5, State: componentspb.ComponentState_COMPONENT_STATE_ENABLED},
			updateState: false,
			wantCols: map[string]any{
				"ComponentId":        int64(5),
				"ObservedGeneration": spanner.NullInt64{},
				"AccessPayload":      []byte(nil),
				"UpdateTime":         spanner.CommitTimestamp,
			},
		},
		{
			name: "updateState true writes State and non-nil fields",
			rec: &store.ComponentRecord{
				ComponentID:        5,
				ObservedGeneration: &obsGen,
				Status:             access,
				State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			},
			updateState: true,
			wantCols: map[string]any{
				"ComponentId":        int64(5),
				"ObservedGeneration": int64(2),
				"AccessPayload":      accessBytes,
				"State":              int64(componentspb.ComponentState_COMPONENT_STATE_ENABLED),
				"UpdateTime":         spanner.CommitTimestamp,
			},
		},
	}
	for _, tt := range obsTests {
		t.Run("observation/"+tt.name, func(t *testing.T) {
			got, err := componentObservationUpdateColumns(tt.rec, tt.updateState)
			if err != nil {
				t.Fatalf("componentObservationUpdateColumns() failed: %v", err)
			}
			if diff := cmp.Diff(tt.wantCols, got); diff != "" {
				t.Errorf("componentObservationUpdateColumns() mismatch (-want +got):\n%s", diff)
			}
		})
	}
	if m, err := ComponentObservationUpdateMutation(&store.ComponentRecord{ComponentID: 5, ObservedGeneration: &obsGen}, true); err != nil || m == nil {
		t.Errorf("ComponentObservationUpdateMutation() = (%v, %v), want (non-nil, nil)", m, err)
	}
}

func TestComponentSoftDeleteAndOwnerColumns(t *testing.T) {
	if _, err := ComponentSoftDeleteMutation(nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ComponentSoftDeleteMutation(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if m := ComponentOwnerUpdateMutation(1, nil); m != nil {
		t.Errorf("ComponentOwnerUpdateMutation(1, nil) = %v, want nil", m)
	}

	owner := &store.SourceOwner{UID: "uid-a", Generation: 3, Namespace: "xmc", ClusterUID: "cluster-a", Epoch: 2}
	if m := ComponentOwnerUpdateMutation(1, owner); m == nil {
		t.Error("ComponentOwnerUpdateMutation(1, owner) = nil, want non-nil")
	}
	wantOwnerCols := map[string]any{
		"ComponentId":        int64(1),
		"SourceUid":          "uid-a",
		"SourceGeneration":   int64(3),
		"SourceNamespace":    "xmc",
		"SourceClusterUid":   "cluster-a",
		"SourceEpoch":        int64(2),
		"SourceLastSeenTime": spanner.CommitTimestamp,
	}
	if diff := cmp.Diff(wantOwnerCols, componentOwnerUpdateColumns(1, owner)); diff != "" {
		t.Errorf("componentOwnerUpdateColumns() mismatch (-want +got):\n%s", diff)
	}

	rec := &store.ComponentRecord{
		ComponentID: 9,
		Name:        "app",
		Spec:        &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.0.0"},
	}
	cfgBytes, _ := mustMarshalPayloads(t, rec.Name, rec.Spec, nil, nil)

	deleteTests := []struct {
		name     string
		owner    *store.SourceOwner
		wantCols map[string]any
	}{
		{
			name:  "nil owner writes only soft-delete columns",
			owner: nil,
			wantCols: map[string]any{
				"ComponentId":   int64(9),
				"ConfigProto":   cfgBytes,
				"AccessPayload": []byte(nil),
				"State":         int64(componentspb.ComponentState_COMPONENT_STATE_DELETED),
				"UpdateTime":    spanner.CommitTimestamp,
			},
		},
		{
			name:  "non-nil owner includes all six Source columns",
			owner: owner,
			wantCols: map[string]any{
				"ComponentId":        int64(9),
				"ConfigProto":        cfgBytes,
				"AccessPayload":      []byte(nil),
				"State":              int64(componentspb.ComponentState_COMPONENT_STATE_DELETED),
				"UpdateTime":         spanner.CommitTimestamp,
				"SourceUid":          "uid-a",
				"SourceGeneration":   int64(3),
				"SourceNamespace":    "xmc",
				"SourceClusterUid":   "cluster-a",
				"SourceEpoch":        int64(2),
				"SourceLastSeenTime": spanner.CommitTimestamp,
			},
		},
	}
	for _, tt := range deleteTests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := componentSoftDeleteColumns(rec, tt.owner)
			if err != nil {
				t.Fatalf("componentSoftDeleteColumns() failed: %v", err)
			}
			if diff := cmp.Diff(tt.wantCols, got); diff != "" {
				t.Errorf("componentSoftDeleteColumns() mismatch (-want +got):\n%s", diff)
			}
		})
	}
	if m, err := ComponentSoftDeleteMutation(rec, owner); err != nil || m == nil {
		t.Errorf("ComponentSoftDeleteMutation() = (%v, %v), want (non-nil, nil)", m, err)
	}
}

func TestExperimentComponentSnapshotInsertMutation(t *testing.T) {
	if _, err := ExperimentComponentSnapshotInsertMutation(42, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ExperimentComponentSnapshotInsertMutation(42, nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}

	rec := &store.ComponentRecord{
		ComponentID: 7,
		Name:        "hpo",
		Spec:        &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
		Generation:  2,
	}
	cfgBytes, accessBytes := mustMarshalPayloads(t, rec.Name, rec.Spec, nil, nil)
	want := spanner.Insert("ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto", "AccessPayload"},
		[]any{data.ReverseBits(42), int64(7), int64(2), cfgBytes, accessBytes},
	)
	got, err := ExperimentComponentSnapshotInsertMutation(42, rec)
	if err != nil {
		t.Fatalf("ExperimentComponentSnapshotInsertMutation() failed: %v", err)
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
		t.Errorf("ExperimentComponentSnapshotInsertMutation() mismatch (-want +got):\n%s", diff)
	}
}
