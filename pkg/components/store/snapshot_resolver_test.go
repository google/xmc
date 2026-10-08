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

package store

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func TestResolveComponentSnapshotFromMap(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	coords := &componentspb.ResolvedComponentCoordinates{
		ClusterName: "cluster-1",
		Namespace:   "default",
		ServiceName: "comp-svc",
		Port:        8080,
	}

	compRecGen2 := &ComponentRecord{
		ComponentID: 1,
		Name:        "comp-a",
		Generation:  2,
		State:       componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Spec: &componentspb.ComponentSpec{
			Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version:     "v1.2.0",
			Generation:  2,
			Description: "Generation 2 spec",
		},
		Status: &componentspb.ComponentAccessStatus{
			State:       componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Coordinates: coords,
		},
		CreateTime: now,
		UpdateTime: now,
	}

	compRecGen1Snapshot := &SnapshotRecord{
		Name:       "comp-a",
		Generation: 1,
		Record: &ComponentRecord{
			ComponentID: 1,
			Name:        "comp-a",
			Generation:  1,
			State:       componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Spec: &componentspb.ComponentSpec{
				Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version:     "v1.0.0",
				Generation:  1,
				Description: "Generation 1 spec",
			},
			CreateTime: now,
			UpdateTime: now,
		},
		Coordinates: coords,
		CreatedAt:   now,
	}

	components := map[int64]*ComponentRecord{
		1: compRecGen2,
		2: {
			ComponentID: 2,
			Name:        "comp-del",
			DeleteTime:  &now,
			State:       componentspb.ComponentState_COMPONENT_STATE_DELETED,
		},
	}

	snapshots := map[int64]map[int64]*SnapshotRecord{
		1: {
			1: compRecGen1Snapshot,
		},
	}

	tests := []struct {
		name        string
		compName    string
		generation  int64
		wantGen     int64
		wantVersion string
		wantCoords  bool
		wantErrCode codes.Code
	}{
		{
			name:        "LatestGeneration_Gen0",
			compName:    "comp-a",
			generation:  0,
			wantGen:     2,
			wantVersion: "v1.2.0",
			wantCoords:  true,
		},
		{
			name:        "CurrentGeneration_Gen2",
			compName:    "components/comp-a",
			generation:  2,
			wantGen:     2,
			wantVersion: "v1.2.0",
			wantCoords:  true,
		},
		{
			name:        "HistoricalGeneration_Gen1",
			compName:    "comp-a",
			generation:  1,
			wantGen:     1,
			wantVersion: "v1.0.0",
			wantCoords:  true,
		},
		{
			name:        "FutureGeneration_NotFound",
			compName:    "comp-a",
			generation:  5,
			wantErrCode: codes.NotFound,
		},
		{
			name:        "NegativeGeneration_InvalidArgument",
			compName:    "comp-a",
			generation:  -1,
			wantErrCode: codes.InvalidArgument,
		},
		{
			name:        "NonexistentComponent_NotFound",
			compName:    "comp-missing",
			generation:  1,
			wantErrCode: codes.NotFound,
		},
		{
			name:        "DeletedComponent_NotFound",
			compName:    "comp-del",
			generation:  1,
			wantErrCode: codes.NotFound,
		},
		{
			name:        "InvalidName_InvalidArgument",
			compName:    "-invalid-",
			generation:  1,
			wantErrCode: codes.InvalidArgument,
		},
		{
			name:        "EmptyName_InvalidArgument",
			compName:    "",
			generation:  1,
			wantErrCode: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, gotCoords, err := ResolveComponentSnapshotFromMap(components, snapshots, tc.compName, tc.generation)
			if tc.wantErrCode != codes.OK {
				if status.Code(err) != tc.wantErrCode {
					t.Errorf("ResolveComponentSnapshotFromMap() got code %v, want %v (err: %v)", status.Code(err), tc.wantErrCode, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveComponentSnapshotFromMap() unexpected error: %v", err)
			}
			if rec.Generation != tc.wantGen {
				t.Errorf("Generation = %d, want %d", rec.Generation, tc.wantGen)
			}
			if rec.Spec.GetVersion() != tc.wantVersion {
				t.Errorf("Spec.Version = %q, want %q", rec.Spec.GetVersion(), tc.wantVersion)
			}
			if tc.wantCoords && gotCoords == nil {
				t.Error("Coordinates is nil, want non-nil")
			}
		})
	}
}

func TestResolveComponentSnapshotFromMap_HistoricalMissing(t *testing.T) {
	compRecGen3 := &ComponentRecord{
		Name:       "comp-b",
		Generation: 3,
		State:      componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Spec: &componentspb.ComponentSpec{
			Version:    "v3.0.0",
			Generation: 3,
		},
	}
	components := map[int64]*ComponentRecord{
		compRecGen3.ComponentID: compRecGen3,
	}
	snapshots := map[int64]map[int64]*SnapshotRecord{}

	_, _, err := ResolveComponentSnapshotFromMap(components, snapshots, "comp-b", 1)
	if status.Code(err) != codes.NotFound {
		t.Errorf("got code %v, want NotFound for missing historical snapshot", status.Code(err))
	}
}

func TestResolveComponentSnapshotFromMap_Immutability(t *testing.T) {
	now := time.Now().UTC()
	coords := &componentspb.ResolvedComponentCoordinates{
		ClusterName: "cluster-1",
		ServiceName: "original-svc",
	}
	rec := &ComponentRecord{
		Name:       "comp-imm",
		Generation: 1,
		State:      componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Spec: &componentspb.ComponentSpec{
			Version:     "v1.0.0",
			Description: "Original description",
		},
		Status: &componentspb.ComponentAccessStatus{
			Coordinates: coords,
		},
		Labels:     map[string]string{"env": "prod"},
		CreateTime: now,
		UpdateTime: now,
	}

	components := map[int64]*ComponentRecord{rec.ComponentID: rec}
	snapshots := map[int64]map[int64]*SnapshotRecord{}

	snapRec, snapCoords, err := ResolveComponentSnapshotFromMap(components, snapshots, "comp-imm", 1)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshotFromMap() unexpected error: %v", err)
	}

	// Mutate returned snapshot record and coordinates
	snapRec.Spec.Description = "MUTATED"
	snapRec.Labels["env"] = "MUTATED"
	if snapCoords != nil {
		snapCoords.ServiceName = "MUTATED"
	}

	// Verify catalog record remains untouched
	if rec.Spec.GetDescription() != "Original description" {
		t.Errorf("Catalog record was modified: got %q, want 'Original description'", rec.Spec.GetDescription())
	}
	if rec.Labels["env"] != "prod" {
		t.Errorf("Catalog labels were modified: got %q, want 'prod'", rec.Labels["env"])
	}
	if rec.Status.GetCoordinates().GetServiceName() != "original-svc" {
		t.Errorf("Catalog coordinates were modified: got %q, want 'original-svc'", rec.Status.GetCoordinates().GetServiceName())
	}

	// Verify cached snapshot in map was not mutated
	cachedSnap := snapshots[rec.ComponentID][1]
	if cachedSnap.Record.Spec.GetDescription() != "Original description" {
		t.Errorf("Cached snapshot was modified: got %q, want 'Original description'", cachedSnap.Record.Spec.GetDescription())
	}
	if cachedSnap.Coordinates.GetServiceName() != "original-svc" {
		t.Errorf("Cached snapshot coordinates were modified: got %q, want 'original-svc'", cachedSnap.Coordinates.GetServiceName())
	}
}

func TestValidateComponentRequirementFromMap(t *testing.T) {
	now := time.Now().UTC()
	components := map[string]*ComponentRecord{
		"comp-valid": {
			Name:  "comp-valid",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Spec: &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version: "v1.5.2",
			},
		},
		"comp-disabled": {
			Name:  "comp-disabled",
			State: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
			Spec: &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version: "v1.5.2",
			},
		},
		"comp-disabling": {
			Name:  "comp-disabling",
			State: componentspb.ComponentState_COMPONENT_STATE_DISABLING,
			Spec: &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version: "v1.5.2",
			},
		},
		"comp-3p": {
			Name:  "comp-3p",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Spec: &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				Version: "v2.0.0",
			},
		},
		"comp-no-version": {
			Name:  "comp-no-version",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Spec: &componentspb.ComponentSpec{
				Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			},
		},
		"comp-invalid-semver": {
			Name:  "comp-invalid-semver",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Spec: &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version: "invalid.version.number",
			},
		},
		"comp-deleted": {
			Name:       "comp-deleted",
			DeleteTime: &now,
			State:      componentspb.ComponentState_COMPONENT_STATE_DELETED,
		},
	}

	tests := []struct {
		name          string
		compName      string
		reqType       componentspb.ComponentType
		minVersion    string
		wantSatisfied bool
		wantErrCode   codes.Code
	}{
		{
			name:          "Satisfied_ExactTypeAndMinVersion",
			compName:      "comp-valid",
			reqType:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			minVersion:    "v1.5.0",
			wantSatisfied: true,
		},
		{
			name:          "Satisfied_AIP122Name",
			compName:      "components/comp-valid",
			reqType:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			minVersion:    "v1.5.2",
			wantSatisfied: true,
		},
		{
			name:          "Satisfied_UnspecifiedType",
			compName:      "comp-valid",
			reqType:       componentspb.ComponentType_COMPONENT_TYPE_UNSPECIFIED,
			minVersion:    "v1.0.0",
			wantSatisfied: true,
		},
		{
			name:          "Satisfied_NoMinVersion",
			compName:      "comp-valid",
			reqType:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			minVersion:    "",
			wantSatisfied: true,
		},
		{
			name:          "Unsatisfied_DisabledComponent",
			compName:      "comp-disabled",
			reqType:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			minVersion:    "v1.0.0",
			wantSatisfied: false,
		},
		{
			name:          "Unsatisfied_DisablingComponent",
			compName:      "comp-disabling",
			reqType:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			minVersion:    "v1.0.0",
			wantSatisfied: false,
		},
		{
			name:          "Unsatisfied_TypeMismatch",
			compName:      "comp-valid",
			reqType:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
			minVersion:    "v1.0.0",
			wantSatisfied: false,
		},
		{
			name:          "Unsatisfied_VersionTooLow",
			compName:      "comp-valid",
			reqType:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			minVersion:    "v2.0.0",
			wantSatisfied: false,
		},
		{
			name:          "Unsatisfied_NoVersionInSpec",
			compName:      "comp-no-version",
			minVersion:    "v1.0.0",
			wantSatisfied: false,
		},
		{
			name:          "Unsatisfied_InvalidSemVerInSpec",
			compName:      "comp-invalid-semver",
			minVersion:    "v1.0.0",
			wantSatisfied: false,
		},
		{
			name:          "Unsatisfied_ComponentNotFound",
			compName:      "comp-nonexistent",
			minVersion:    "v1.0.0",
			wantSatisfied: false,
		},
		{
			name:          "Unsatisfied_DeletedComponent",
			compName:      "comp-deleted",
			minVersion:    "v1.0.0",
			wantSatisfied: false,
		},
		{
			name:        "Error_InvalidMinVersion",
			compName:    "comp-valid",
			minVersion:  "invalid-min-version",
			wantErrCode: codes.InvalidArgument,
		},
		{
			name:        "Error_InvalidComponentName",
			compName:    "-invalid-",
			minVersion:  "v1.0.0",
			wantErrCode: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ValidateComponentRequirementFromMap(components, tc.compName, tc.reqType, tc.minVersion)
			if tc.wantErrCode != codes.OK {
				if status.Code(err) != tc.wantErrCode {
					t.Errorf("ValidateComponentRequirementFromMap() got code %v, want %v (err: %v)", status.Code(err), tc.wantErrCode, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateComponentRequirementFromMap() unexpected error: %v", err)
			}
			if res.IsSatisfied != tc.wantSatisfied {
				t.Errorf("IsSatisfied = %v, want %v (reason: %q)", res.IsSatisfied, tc.wantSatisfied, res.Reason)
			}
		})
	}
}

func TestValidateComponentRequirementFromMap_Immutability(t *testing.T) {
	components := map[string]*ComponentRecord{
		"comp-val-imm": {
			Name:  "comp-val-imm",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Spec: &componentspb.ComponentSpec{
				Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version:     "v1.0.0",
				Description: "Original",
			},
		},
	}

	res, err := ValidateComponentRequirementFromMap(components, "comp-val-imm", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "v1.0.0")
	if err != nil {
		t.Fatalf("ValidateComponentRequirementFromMap() failed: %v", err)
	}
	if !res.IsSatisfied {
		t.Fatalf("IsSatisfied = false, want true")
	}

	// Mutate returned component record
	res.Component.Spec.Description = "MUTATED"

	// Verify catalog record remains unmodified
	if components["comp-val-imm"].Spec.GetDescription() != "Original" {
		t.Error("Catalog record was modified through requirement validation result!")
	}
}

func TestSnapshotResolver_NilCoordinates(t *testing.T) {
	if coords := cloneCoordinates(nil); coords != nil {
		t.Errorf("cloneCoordinates(nil) = %v, want nil", coords)
	}

	components := map[int64]*ComponentRecord{
		1: {
			ComponentID: 1,
			Name:        "comp-no-coords",
			Generation:  1,
			State:       componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Spec: &componentspb.ComponentSpec{
				Version: "v1.0.0",
			},
			Status: nil,
		},
	}
	snapshots := map[int64]map[int64]*SnapshotRecord{}

	rec, coords, err := ResolveComponentSnapshotFromMap(components, snapshots, "comp-no-coords", 1)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshotFromMap failed: %v", err)
	}
	if rec == nil {
		t.Fatal("rec is nil")
	}
	if coords != nil {
		t.Errorf("coords = %v, want nil", coords)
	}
}
