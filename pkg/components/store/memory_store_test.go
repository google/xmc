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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

	if rec.ComponentID != 1 {
		t.Errorf("ComponentID = %d, want 1", rec.ComponentID)
	}
	if rec.Name != "my-comp" {
		t.Errorf("Name = %q, want %q", rec.Name, "my-comp")
	}
	if rec.Generation != 1 {
		t.Errorf("Generation = %d, want 1", rec.Generation)
	}
	if rec.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("State = %v, want PENDING_RECONCILIATION", rec.State)
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
	if rec.Labels["env"] != "test" {
		t.Errorf("Labels[env] = %q, want %q", rec.Labels["env"], "test")
	}
	if rec.Spec.GetGeneration() != 1 {
		t.Errorf("Spec.Generation = %d, want 1", rec.Spec.GetGeneration())
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
	if err := ms.UpdateObservedGeneration(ctx, "my-comp", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, ""); err != nil {
		t.Fatalf("UpdateObservedGeneration() failed: %v", err)
	}

	spec2 := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.1.0",
		Description: "Updated version",
	}
	rec2, err := ms.SetComponentIntent(ctx, "my-comp", spec2, map[string]string{"tier": "prod"}, 0)
	if err != nil {
		t.Fatalf("Second SetComponentIntent() failed: %v", err)
	}

	if rec2.ComponentID != rec1.ComponentID {
		t.Errorf("ComponentID changed from %d to %d", rec1.ComponentID, rec2.ComponentID)
	}
	if rec2.Generation != 2 {
		t.Errorf("Generation = %d, want 2", rec2.Generation)
	}
	if rec2.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("State = %v, want PENDING_RECONCILIATION after updating intent", rec2.State)
	}
	if rec2.Spec.GetDescription() != "Updated version" {
		t.Errorf("Spec.Description = %q, want 'Updated version'", rec2.Spec.GetDescription())
	}
	if rec2.Labels["tier"] != "prod" {
		t.Errorf("Labels[tier] = %q, want 'prod'", rec2.Labels["tier"])
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
	if rec.Generation != 5 {
		t.Errorf("Generation = %d, want 5", rec.Generation)
	}
	if rec.Spec.GetGeneration() != 5 {
		t.Errorf("Spec.Generation = %d, want 5", rec.Spec.GetGeneration())
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

func TestMemoryStore_GetComponent(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	spec := sampleSpec("Testing GetComponent")
	_, err := ms.SetComponentIntent(ctx, "comp-a", spec, nil, 0)
	if err != nil {
		t.Fatalf("SetComponentIntent() failed: %v", err)
	}

	// Canonical name lookup
	rec, err := ms.GetComponent(ctx, "comp-a")
	if err != nil {
		t.Fatalf("GetComponent(comp-a) unexpected error: %v", err)
	}
	if rec.Name != "comp-a" {
		t.Errorf("Name = %q, want 'comp-a'", rec.Name)
	}

	// AIP-122 full resource name lookup
	recAIP, err := ms.GetComponent(ctx, "components/comp-a")
	if err != nil {
		t.Fatalf("GetComponent(components/comp-a) unexpected error: %v", err)
	}
	if recAIP.Name != "comp-a" {
		t.Errorf("Name = %q, want 'comp-a'", recAIP.Name)
	}

	// Nonexistent lookup
	_, err = ms.GetComponent(ctx, "comp-nonexistent")
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(comp-nonexistent) got code %v, want NotFound", status.Code(err))
	}

	// Invalid name
	_, err = ms.GetComponent(ctx, "invalid name with spaces")
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetComponent(invalid) got code %v, want InvalidArgument", status.Code(err))
	}
}

func TestMemoryStore_GetComponent_SoftDeleted(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	spec := sampleSpec("Soft delete test")
	rec, err := ms.SetComponentIntent(ctx, "comp-deleted", spec, nil, 0)
	if err != nil {
		t.Fatalf("SetComponentIntent() failed: %v", err)
	}

	// Simulate soft deletion directly in internal state
	ms.mu.Lock()
	now := time.Now().UTC()
	ms.componentsByName["comp-deleted"].DeleteTime = &now
	ms.componentsByName["comp-deleted"].State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	ms.mu.Unlock()

	_, err = ms.GetComponent(ctx, "comp-deleted")
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(deleted) got code %v, want NotFound", status.Code(err))
	}

	_, err = ms.GetComponentByID(ctx, rec.ComponentID)
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetComponentByID(deleted) got code %v, want NotFound", status.Code(err))
	}
}

func TestMemoryStore_GetComponentByID(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	spec := sampleSpec("ByID test")
	created, err := ms.SetComponentIntent(ctx, "comp-id-test", spec, nil, 0)
	if err != nil {
		t.Fatalf("SetComponentIntent() failed: %v", err)
	}

	rec, err := ms.GetComponentByID(ctx, created.ComponentID)
	if err != nil {
		t.Fatalf("GetComponentByID(%d) unexpected error: %v", created.ComponentID, err)
	}
	if rec.Name != "comp-id-test" {
		t.Errorf("Name = %q, want 'comp-id-test'", rec.Name)
	}

	// Nonexistent ID
	_, err = ms.GetComponentByID(ctx, 99999)
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetComponentByID(99999) got code %v, want NotFound", status.Code(err))
	}

	// Invalid IDs
	for _, invalidID := range []int64{0, -1, -100} {
		_, err = ms.GetComponentByID(ctx, invalidID)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("GetComponentByID(%d) got code %v, want InvalidArgument", invalidID, status.Code(err))
		}
	}
}

func TestMemoryStore_ListComponents(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	// Empty list
	emptyList, err := ms.ListComponents(ctx)
	if err != nil {
		t.Fatalf("ListComponents() on empty store failed: %v", err)
	}
	if len(emptyList) != 0 {
		t.Errorf("ListComponents() len = %d, want 0", len(emptyList))
	}

	// Populate multiple components out of alphabetical order
	names := []string{"comp-c", "comp-a", "comp-b"}
	for _, n := range names {
		if _, err := ms.SetComponentIntent(ctx, n, sampleSpec(n), nil, 0); err != nil {
			t.Fatalf("SetComponentIntent(%s) failed: %v", n, err)
		}
	}

	list, err := ms.ListComponents(ctx)
	if err != nil {
		t.Fatalf("ListComponents() failed: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("ListComponents() returned %d items, want 3", len(list))
	}

	// Verify alphabetical order
	expectedOrder := []string{"comp-a", "comp-b", "comp-c"}
	for i, exp := range expectedOrder {
		if list[i].Name != exp {
			t.Errorf("list[%d].Name = %q, want %q", i, list[i].Name, exp)
		}
	}
}

func TestMemoryStore_UpdateObservedGeneration(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	spec := sampleSpec("Observed gen test")
	_, err := ms.SetComponentIntent(ctx, "comp-obs", spec, nil, 0)
	if err != nil {
		t.Fatalf("SetComponentIntent() failed: %v", err)
	}

	access := &componentspb.ComponentAccessStatus{
		Coordinates: &componentspb.ResolvedComponentCoordinates{
			ClusterName: "gke-cluster-1",
			Namespace:   "default",
			ServiceName: "comp-obs-svc",
			Port:        8080,
		},
		Ready: true,
	}

	err = ms.UpdateObservedGeneration(ctx, "comp-obs", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Reconciliation successful")
	if err != nil {
		t.Fatalf("UpdateObservedGeneration() unexpected error: %v", err)
	}

	rec, err := ms.GetComponent(ctx, "comp-obs")
	if err != nil {
		t.Fatalf("GetComponent() unexpected error: %v", err)
	}

	if rec.ObservedGeneration == nil || *rec.ObservedGeneration != 1 {
		t.Errorf("ObservedGeneration = %v, want 1", rec.ObservedGeneration)
	}
	if rec.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("State = %v, want ENABLED", rec.State)
	}
	if rec.Status == nil {
		t.Fatal("Status is nil, expected non-nil")
	}
	if rec.Status.GetState() != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("Status.State = %v, want ENABLED", rec.Status.GetState())
	}
	if rec.Status.GetHealthMessage() != "Reconciliation successful" {
		t.Errorf("Status.HealthMessage = %q, want 'Reconciliation successful'", rec.Status.GetHealthMessage())
	}
	if rec.Status.GetCoordinates().GetServiceName() != "comp-obs-svc" {
		t.Errorf("Status.Coordinates.ServiceName = %q, want 'comp-obs-svc'", rec.Status.GetCoordinates().GetServiceName())
	}

	// Update on nonexistent component
	err = ms.UpdateObservedGeneration(ctx, "nonexistent", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if status.Code(err) != codes.NotFound {
		t.Errorf("UpdateObservedGeneration(nonexistent) got code %v, want NotFound", status.Code(err))
	}

	// Negative observed generation
	err = ms.UpdateObservedGeneration(ctx, "comp-obs", -1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateObservedGeneration(negative) got code %v, want InvalidArgument", status.Code(err))
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

	// Fetch again and verify internal state remained unchanged
	rec2, err := ms.GetComponent(ctx, "comp-immutable")
	if err != nil {
		t.Fatalf("GetComponent() failed: %v", err)
	}

	if rec2.Spec.GetDescription() == "MUTATED" {
		t.Error("MemoryStore internal Spec was mutated!")
	}
	if rec2.Labels["key"] == "MUTATED" {
		t.Error("MemoryStore internal Labels map was mutated!")
	}
	if rec2.Dependencies[0] == "MUTATED" {
		t.Error("MemoryStore internal Dependencies slice was mutated!")
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
				switch op % 5 {
				case 0:
					spec := &componentspb.ComponentSpec{
						Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
						Version:     "v1.0.0",
						Description: fmt.Sprintf("goroutine %d op %d", gid, op),
					}
					_, _ = ms.SetComponentIntent(ctx, compName, spec, map[string]string{"g": fmt.Sprintf("%d", gid)}, 0)
				case 1:
					_, _ = ms.GetComponent(ctx, compName)
				case 2:
					_, _ = ms.GetComponentByID(ctx, int64((gid%10)+1))
				case 3:
					_, _ = ms.ListComponents(ctx)
				case 4:
					_ = ms.UpdateObservedGeneration(ctx, compName, int64(op), nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "OK")
				}
			}
		}(g)
	}

	wg.Wait()

	list, err := ms.ListComponents(ctx)
	if err != nil {
		t.Fatalf("ListComponents() after concurrent stress failed: %v", err)
	}
	if len(list) == 0 {
		t.Error("ListComponents() returned 0 components after concurrent writes")
	}
}

func TestMemoryStore_UnimplementedStubs(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

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
