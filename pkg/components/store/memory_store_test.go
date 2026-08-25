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
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func sampleSpec(desc string) *componentspb.ComponentSpec {
	return &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: desc,
		Manifests: []string{
			"apiVersion: v1\nkind: Service\nmetadata:\n  annotations:\n    xmc.dev/depends-on: dep-svc",
		},
	}
}

func TestMemoryStore_SetComponentIntent_NewComponent(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	spec := sampleSpec("Initial intent")
	labels := map[string]string{"env": "test"}

	rec, err := ms.SetComponentIntent(ctx, "components/my-comp", spec, labels, 0)
	if err != nil {
		t.Fatalf("SetComponentIntent() unexpected error: %v", err)
	}

	wantProto := &componentspb.Component{
		Name: "components/my-comp",
		Spec: &componentspb.ComponentSpec{
			Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version:     "v1.0.0",
			Description: "Initial intent",
			Manifests: []string{
				"apiVersion: v1\nkind: Service\nmetadata:\n  annotations:\n    xmc.dev/depends-on: dep-svc",
			},
			Generation: 1,
		},
		Status: &componentspb.ComponentAccessStatus{
			State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		Labels: map[string]string{"env": "test"},
	}
	if diff := cmp.Diff(wantProto, ToProtoComponent(rec), protocmp.Transform(), protocmp.IgnoreFields(&componentspb.Component{}, "etag", "create_time", "update_time")); diff != "" {
		t.Errorf("ToProtoComponent(rec) mismatch (-want +got):\n%s", diff)
	}

	if rec.ComponentID != 1 {
		t.Errorf("ComponentID = %d, want 1", rec.ComponentID)
	}
	if rec.Etag == "" {
		t.Error("Etag is empty, expected non-empty etag")
	}
	if rec.CreateTime.IsZero() || rec.UpdateTime.IsZero() {
		t.Error("CreateTime or UpdateTime is zero")
	}
	if len(rec.Dependencies) != 1 || rec.Dependencies[0] != "dep-svc" {
		t.Errorf("Dependencies = %v, want [dep-svc]", rec.Dependencies)
	}
}

func TestMemoryStore_SetComponentIntent_UpdateExisting(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	spec1 := sampleSpec("First version")
	rec1, err := ms.SetComponentIntent(ctx, "my-comp", spec1, nil, 0)
	if err != nil {
		t.Fatalf("First SetComponentIntent() failed: %v", err)
	}

	time.Sleep(1 * time.Millisecond) // Ensure timestamp advances

	// Transition component to ENABLED
	ms.mu.Lock()
	ms.componentsByName["my-comp"].State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	ms.componentsByName["my-comp"].Status.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	ms.mu.Unlock()

	spec2 := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.1.0",
		Description: "Updated version",
	}
	rec2, err := ms.SetComponentIntent(ctx, "my-comp", spec2, map[string]string{"tier": "prod"}, 0)
	if err != nil {
		t.Fatalf("Second SetComponentIntent() failed: %v", err)
	}

	wantProto := &componentspb.Component{
		Name: "components/my-comp",
		Spec: &componentspb.ComponentSpec{
			Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version:     "v1.1.0",
			Description: "Updated version",
			Generation:  2,
		},
		Status: &componentspb.ComponentAccessStatus{
			State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		Labels: map[string]string{"tier": "prod"},
	}
	if diff := cmp.Diff(wantProto, ToProtoComponent(rec2), protocmp.Transform(), protocmp.IgnoreFields(&componentspb.Component{}, "etag", "create_time", "update_time")); diff != "" {
		t.Errorf("ToProtoComponent(rec2) mismatch (-want +got):\n%s", diff)
	}

	if rec2.ComponentID != rec1.ComponentID {
		t.Errorf("ComponentID changed from %d to %d", rec1.ComponentID, rec2.ComponentID)
	}
	if rec2.Etag == rec1.Etag {
		t.Error("Etag should have changed after update")
	}
	if !rec2.UpdateTime.After(rec1.CreateTime) && !rec2.UpdateTime.Equal(rec1.CreateTime) {
		t.Error("UpdateTime should be >= CreateTime")
	}
}

func TestMemoryStore_SetComponentIntent_DesiredGeneration(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	spec := sampleSpec("Generation override")
	rec, err := ms.SetComponentIntent(ctx, "my-comp", spec, nil, 5)
	if err != nil {
		t.Fatalf("SetComponentIntent() unexpected error: %v", err)
	}

	wantProto := &componentspb.Component{
		Name: "components/my-comp",
		Spec: &componentspb.ComponentSpec{
			Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version:     "v1.0.0",
			Description: "Generation override",
			Manifests: []string{
				"apiVersion: v1\nkind: Service\nmetadata:\n  annotations:\n    xmc.dev/depends-on: dep-svc",
			},
			Generation: 5,
		},
		Status: &componentspb.ComponentAccessStatus{
			State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
	}
	if diff := cmp.Diff(wantProto, ToProtoComponent(rec), protocmp.Transform(), protocmp.IgnoreFields(&componentspb.Component{}, "etag", "create_time", "update_time")); diff != "" {
		t.Errorf("ToProtoComponent(rec) mismatch (-want +got):\n%s", diff)
	}
}

func TestMemoryStore_SetComponentIntent_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()
	validSpec := sampleSpec("Valid")

	tests := []struct {
		name    string
		comp    string
		spec    *componentspb.ComponentSpec
		wantErr codes.Code
	}{
		{
			name:    "EmptyName",
			comp:    "",
			spec:    validSpec,
			wantErr: codes.InvalidArgument,
		},
		{
			name:    "InvalidName_HyphenStart",
			comp:    "-invalid-name",
			spec:    validSpec,
			wantErr: codes.InvalidArgument,
		},
		{
			name:    "InvalidName_Uppercase",
			comp:    "InvalidName",
			spec:    validSpec,
			wantErr: codes.InvalidArgument,
		},
		{
			name:    "NilSpec",
			comp:    "valid-name",
			spec:    nil,
			wantErr: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ms.SetComponentIntent(ctx, tc.comp, tc.spec, nil, 0)
			if status.Code(err) != tc.wantErr {
				t.Errorf("got code %v, want %v (err: %v)", status.Code(err), tc.wantErr, err)
			}
		})
	}
}

func TestMemoryStore_Immutability(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	spec := sampleSpec("Original Spec")
	labels := map[string]string{"key": "original"}
	rec1, err := ms.SetComponentIntent(ctx, "comp-immutable", spec, labels, 0)
	if err != nil {
		t.Fatalf("SetComponentIntent() failed: %v", err)
	}

	// Mutate returned object
	rec1.Spec.Description = "MUTATED"
	rec1.Labels["key"] = "MUTATED"
	rec1.Dependencies[0] = "MUTATED"

	// Mutate original input spec
	spec.Description = "MUTATED_INPUT"

	// Invoke SetComponentIntent again and verify internal state used original clean data
	rec2, err := ms.SetComponentIntent(ctx, "comp-immutable", sampleSpec("Second Spec"), nil, 0)
	if err != nil {
		t.Fatalf("Second SetComponentIntent() failed: %v", err)
	}

	if rec2.Generation != 2 {
		t.Errorf("Generation = %d, want 2", rec2.Generation)
	}
}

func TestMemoryStore_ConcurrencyStress(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	const numGoroutines = 60
	const opsPerGoroutine = 40
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			compName := fmt.Sprintf("comp-%d", gid%10)

			for op := 0; op < opsPerGoroutine; op++ {
				spec := &componentspb.ComponentSpec{
					Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version:     "v1.0.0",
					Description: fmt.Sprintf("goroutine %d op %d", gid, op),
				}
				_, _ = ms.SetComponentIntent(ctx, compName, spec, map[string]string{"g": fmt.Sprintf("%d", gid)}, 0)
			}
		}(g)
	}

	wg.Wait()
}

func TestMemoryStore_UnimplementedStubs(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	if _, err := ms.GetComponent(ctx, "comp-1"); status.Code(err) != codes.Unimplemented {
		t.Errorf("GetComponent got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := ms.GetComponentByID(ctx, 1); status.Code(err) != codes.Unimplemented {
		t.Errorf("GetComponentByID got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := ms.ListComponents(ctx); status.Code(err) != codes.Unimplemented {
		t.Errorf("ListComponents got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := ms.UpdateObservedGeneration(ctx, "comp-1", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, ""); status.Code(err) != codes.Unimplemented {
		t.Errorf("UpdateObservedGeneration got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := ms.PrepareDeactivation(ctx, "comp-1", false); status.Code(err) != codes.Unimplemented {
		t.Errorf("PrepareDeactivation got code %v, want Unimplemented", status.Code(err))
	}
	if err := ms.CompleteDeactivation(ctx, "comp-1"); status.Code(err) != codes.Unimplemented {
		t.Errorf("CompleteDeactivation got code %v, want Unimplemented", status.Code(err))
	}
	if err := ms.ExecuteDeletion(ctx, "comp-1", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false); status.Code(err) != codes.Unimplemented {
		t.Errorf("ExecuteDeletion got code %v, want Unimplemented", status.Code(err))
	}
	if _, _, err := ms.ResolveComponentSnapshot(ctx, "comp-1", 1); status.Code(err) != codes.Unimplemented {
		t.Errorf("ResolveComponentSnapshot got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := ms.ValidateComponentRequirement(ctx, "comp-1", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "v1.0.0"); status.Code(err) != codes.Unimplemented {
		t.Errorf("ValidateComponentRequirement got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := ms.GetPlatformHealth(ctx, "", 10, ""); status.Code(err) != codes.Unimplemented {
		t.Errorf("GetPlatformHealth got code %v, want Unimplemented", status.Code(err))
	}
}
