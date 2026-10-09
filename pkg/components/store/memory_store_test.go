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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// sampleDependency is the dependency that sampleSpec declares through a manifest annotation.
const sampleDependency = "dep-svc"

func sampleSpec(desc string) *componentspb.ComponentSpec {
	return &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: desc,
		Manifests: []string{
			"apiVersion: v1\nkind: Service\nmetadata:\n  annotations:\n    xmc.dev/depends-on: " + sampleDependency,
		},
	}
}

// newMemoryStoreWithDependencies returns a MemoryStore in which each of deps is registered without
// dependencies of its own, so intents may declare them; MemoryStore rejects unregistered ones.
func newMemoryStoreWithDependencies(t *testing.T, deps ...string) *MemoryStore {
	t.Helper()
	ms := NewMemoryStore()
	for _, dep := range deps {
		if _, err := ms.SetComponentIntent(context.Background(), dep, specWithDeps(""), nil, 0); err != nil {
			t.Fatalf("register dependency %s error = %v", dep, err)
		}
	}
	return ms
}

func TestMemoryStore_SetComponentIntent_NewComponent(t *testing.T) {
	ctx := context.Background()
	ms := newMemoryStoreWithDependencies(t, sampleDependency)

	spec := sampleSpec("Initial intent")
	labels := map[string]string{"env": "test"}

	rec, err := ms.SetComponentIntent(ctx, "components/my-comp", spec, labels, 0)
	if err != nil {
		t.Fatalf("SetComponentIntent() unexpected error: %v", err)
	}

	wantRec := &ComponentRecord{
		ComponentID:  2,
		Name:         "my-comp",
		Generation:   1,
		State:        componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Dependencies: []string{"dep-svc"},
		Labels:       map[string]string{"env": "test"},
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
	}
	if diff := cmp.Diff(wantRec, rec, protocmp.Transform(), cmpopts.IgnoreFields(ComponentRecord{}, "CreateTime", "UpdateTime", "Etag")); diff != "" {
		t.Errorf("ComponentRecord mismatch (-want +got):\n%s", diff)
	}

	if rec.Etag == "" {
		t.Error("Etag is empty, expected non-empty etag")
	}
	if rec.CreateTime.IsZero() || rec.UpdateTime.IsZero() {
		t.Error("CreateTime or UpdateTime is zero")
	}
}

func TestMemoryStore_SetComponentIntent_UpdateExisting(t *testing.T) {
	ctx := context.Background()
	ms := newMemoryStoreWithDependencies(t, sampleDependency)

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

	wantRec2 := &ComponentRecord{
		ComponentID:  2,
		Name:         "my-comp",
		Generation:   2,
		State:        componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Dependencies: nil,
		Labels:       map[string]string{"tier": "prod"},
		Spec: &componentspb.ComponentSpec{
			Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version:     "v1.1.0",
			Description: "Updated version",
			Generation:  2,
		},
		Status: &componentspb.ComponentAccessStatus{
			State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
	}
	if diff := cmp.Diff(wantRec2, rec2, protocmp.Transform(), cmpopts.IgnoreFields(ComponentRecord{}, "CreateTime", "UpdateTime", "Etag")); diff != "" {
		t.Errorf("ComponentRecord mismatch (-want +got):\n%s", diff)
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
	ms := newMemoryStoreWithDependencies(t, sampleDependency)

	spec := sampleSpec("Generation override")
	rec, err := ms.SetComponentIntent(ctx, "my-comp", spec, nil, 5)
	if err != nil {
		t.Fatalf("SetComponentIntent() unexpected error: %v", err)
	}

	wantRec := &ComponentRecord{
		ComponentID:  2,
		Name:         "my-comp",
		Generation:   5,
		State:        componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Dependencies: []string{"dep-svc"},
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
	if diff := cmp.Diff(wantRec, rec, protocmp.Transform(), cmpopts.IgnoreFields(ComponentRecord{}, "CreateTime", "UpdateTime", "Etag")); diff != "" {
		t.Errorf("ComponentRecord mismatch (-want +got):\n%s", diff)
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
	ms := newMemoryStoreWithDependencies(t, sampleDependency)

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
	ms := newMemoryStoreWithDependencies(t, sampleDependency)

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
	ms := newMemoryStoreWithDependencies(t, sampleDependency)

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
		if _, err := ms.SetComponentIntent(ctx, n, specWithDeps(n), nil, 0); err != nil {
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
	ms := newMemoryStoreWithDependencies(t, sampleDependency)

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

	rec, err := ms.UpdateObservedGeneration(ctx, "comp-obs", 1, access, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Reconciliation successful", nil)
	if err != nil {
		t.Fatalf("UpdateObservedGeneration() unexpected error: %v", err)
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
	_, err = ms.UpdateObservedGeneration(ctx, "nonexistent", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "", nil)
	if status.Code(err) != codes.NotFound {
		t.Errorf("UpdateObservedGeneration(nonexistent) got code %v, want NotFound", status.Code(err))
	}

	// Negative observed generation
	_, err = ms.UpdateObservedGeneration(ctx, "comp-obs", -1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "", nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateObservedGeneration(negative) got code %v, want InvalidArgument", status.Code(err))
	}
}

func TestMemoryStore_Immutability(t *testing.T) {
	ctx := context.Background()
	ms := newMemoryStoreWithDependencies(t, sampleDependency)

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
					_, _ = ms.UpdateObservedGeneration(ctx, compName, int64(op), nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "OK", nil)
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

func TestMemoryStore_SetComponentIntent_CycleRejection(t *testing.T) {
	ctx := context.Background()
	// Dependencies must be registered, so build the chain comp-a -> comp-b -> comp-c bottom-up.
	ms := newMemoryStoreWithDependencies(t, "comp-c")
	for _, link := range []struct{ name, dep string }{{"comp-b", "comp-c"}, {"comp-a", "comp-b"}} {
		if _, err := ms.SetComponentIntent(ctx, link.name, specWithDeps("@depends-on: "+link.dep), nil, 0); err != nil {
			t.Fatalf("SetComponentIntent(%s) unexpected error: %v", link.name, err)
		}
	}

	tests := []struct {
		name   string
		target string
	}{
		{name: "two-node cycle", target: "comp-b"},
		{name: "three-node cycle", target: "comp-c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ms.SetComponentIntent(ctx, tt.target, specWithDeps("@depends-on: comp-a"), nil, 0)
			if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "dependency cycle detected") {
				t.Errorf("SetComponentIntent(%s -> comp-a) error = %v, want FailedPrecondition dependency cycle", tt.target, err)
			}
		})
	}
}

// specWithDeps returns a 1P spec whose dependencies are declared only via the structured
// spec.dependencies field (mirroring the Component CR), with an optional description.
func specWithDeps(desc string, deps ...string) *componentspb.ComponentSpec {
	return &componentspb.ComponentSpec{
		Type:         componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:      "v1.0.0",
		Description:  desc,
		Dependencies: deps,
	}
}

func TestMemoryStore_SpecDependencies(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		action   func(ms *MemoryStore) error
		wantCode codes.Code
	}{
		{
			name: "deactivating a dependency with an active spec.dependencies dependent is blocked",
			action: func(ms *MemoryStore) error {
				_, err := ms.PrepareDeactivation(ctx, "redpanda", false, nil)
				return err
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "non-cascading deletion of a dependency with an active dependent is blocked",
			action: func(ms *MemoryStore) error {
				return ms.ExecuteDeletion(ctx, "redpanda", componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, false, nil)
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "cycle via spec.dependencies is rejected",
			action: func(ms *MemoryStore) error {
				_, err := ms.SetComponentIntent(ctx, "redpanda", specWithDeps("", "components/ingestion-gateway"), nil, 0)
				return err
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "deactivating the dependent itself is allowed",
			action: func(ms *MemoryStore) error {
				_, err := ms.PrepareDeactivation(ctx, "ingestion-gateway", false, nil)
				return err
			},
			wantCode: codes.OK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := NewMemoryStore()
			if _, err := ms.SetComponentIntent(ctx, "redpanda", specWithDeps(""), nil, 0); err != nil {
				t.Fatalf("SetComponentIntent(redpanda) unexpected error: %v", err)
			}
			if _, err := ms.SetComponentIntent(ctx, "ingestion-gateway", specWithDeps("", "redpanda"), nil, 0); err != nil {
				t.Fatalf("SetComponentIntent(ingestion-gateway) unexpected error: %v", err)
			}
			if got := status.Code(tt.action(ms)); got != tt.wantCode {
				t.Errorf("action got code %v, want %v", got, tt.wantCode)
			}
		})
	}
}

func TestMemoryStore_SpecDependencies_MergedWithDirectives(t *testing.T) {
	ctx := context.Background()
	ms := newMemoryStoreWithDependencies(t, "auth", "metrics", "redpanda")
	spec := specWithDeps("@depends-on: redpanda, metrics", "redpanda", "components/auth")

	rec, err := ms.SetComponentIntent(ctx, "ingestion-gateway", spec, nil, 0)
	if err != nil {
		t.Fatalf("SetComponentIntent(ingestion-gateway) unexpected error: %v", err)
	}
	if diff := cmp.Diff([]string{"auth", "metrics", "redpanda"}, rec.Dependencies); diff != "" {
		t.Errorf("Dependencies mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"redpanda", "components/auth"}, rec.Spec.GetDependencies()); diff != "" {
		t.Errorf("Spec.Dependencies not preserved verbatim (-want +got):\n%s", diff)
	}
}

func TestMemoryStore_PrepareDeactivation(t *testing.T) {
	ctx := context.Background()
	ms := newMemoryStoreWithDependencies(t, sampleDependency)

	specRoot := sampleSpec("Root component")
	if _, err := ms.SetComponentIntent(ctx, "root-comp", specRoot, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent(root-comp) failed: %v", err)
	}

	specDep := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "@depends-on: root-comp",
	}
	if _, err := ms.SetComponentIntent(ctx, "dependent-comp", specDep, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent(dependent-comp) failed: %v", err)
	}

	// 1. Prepare deactivation without cascade on root -> rejected due to active dependent
	_, err := ms.PrepareDeactivation(ctx, "root-comp", false, nil)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("PrepareDeactivation(root, cascade=false) got code %v, want FailedPrecondition", status.Code(err))
	}

	// 2. Prepare deactivation with cascade -> succeeds and marks both DISABLING
	res, err := ms.PrepareDeactivation(ctx, "root-comp", true, nil)
	if err != nil {
		t.Fatalf("PrepareDeactivation(root, cascade=true) unexpected error: %v", err)
	}
	if !res.IsSafeToDeactivate {
		t.Error("IsSafeToDeactivate = false, want true")
	}
	if diff := cmp.Diff([]string{"dependent-comp"}, res.CascadedDeactivations); diff != "" {
		t.Errorf("CascadedDeactivations mismatch (-want +got):\n%s", diff)
	}

	// Verify states in store
	recRoot, err := ms.GetComponent(ctx, "root-comp")
	if err != nil {
		t.Fatalf("GetComponent(root-comp) failed: %v", err)
	}
	if recRoot.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
		t.Errorf("root-comp State = %v, want DISABLING", recRoot.State)
	}
	if recRoot.Status.GetLastTransitionTime() == nil {
		t.Error("root-comp Status.LastTransitionTime is nil, want non-nil timestamp")
	}

	recDep, err := ms.GetComponent(ctx, "dependent-comp")
	if err != nil {
		t.Fatalf("GetComponent(dependent-comp) failed: %v", err)
	}
	if recDep.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
		t.Errorf("dependent-comp State = %v, want DISABLING", recDep.State)
	}
	if recDep.Status.GetLastTransitionTime() == nil {
		t.Error("dependent-comp Status.LastTransitionTime is nil, want non-nil timestamp")
	}

	// 3. Repeated PrepareDeactivation is idempotent and preserves UpdateTime and Etag
	origEtag := recRoot.Etag
	origUpdateTime := recRoot.UpdateTime
	if _, err := ms.PrepareDeactivation(ctx, "root-comp", true, nil); err != nil {
		t.Fatalf("PrepareDeactivation(root, second call) unexpected error: %v", err)
	}
	recRoot2, err := ms.GetComponent(ctx, "root-comp")
	if err != nil {
		t.Fatalf("GetComponent(root-comp) failed: %v", err)
	}
	if recRoot2.Etag != origEtag {
		t.Errorf("root-comp Etag changed on second PrepareDeactivation: got %s, want %s", recRoot2.Etag, origEtag)
	}
	if !recRoot2.UpdateTime.Equal(origUpdateTime) {
		t.Errorf("root-comp UpdateTime changed on second PrepareDeactivation: got %v, want %v", recRoot2.UpdateTime, origUpdateTime)
	}

	// 3. Nonexistent component
	_, err = ms.PrepareDeactivation(ctx, "nonexistent", false, nil)
	if status.Code(err) != codes.NotFound {
		t.Errorf("PrepareDeactivation(nonexistent) got code %v, want NotFound", status.Code(err))
	}
}

func TestMemoryStore_CompleteDeactivation(t *testing.T) {
	ctx := context.Background()
	ms := newMemoryStoreWithDependencies(t, sampleDependency)

	spec := sampleSpec("Deactivation target")
	if _, err := ms.SetComponentIntent(ctx, "comp-deact", spec, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	// 1. Complete deactivation on non-disabling state -> ErrInvalidStateTransition
	err := ms.CompleteDeactivation(ctx, "comp-deact", false, nil)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("CompleteDeactivation(not disabling) got code %v, want FailedPrecondition", status.Code(err))
	}

	// 2. Prepare deactivation
	if _, err := ms.PrepareDeactivation(ctx, "comp-deact", false, nil); err != nil {
		t.Fatalf("PrepareDeactivation failed: %v", err)
	}

	// 3. Add an active dependent
	specDep := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "@depends-on: comp-deact",
	}
	if _, err := ms.SetComponentIntent(ctx, "dep-active", specDep, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent(dep-active) failed: %v", err)
	}

	// 4. Attempt complete deactivation while dependent is active -> FailedPrecondition
	err = ms.CompleteDeactivation(ctx, "comp-deact", false, nil)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("CompleteDeactivation(with active dependent) got code %v, want FailedPrecondition", status.Code(err))
	}

	// 5. Force complete deactivation on a separate component with active dependent
	specForceTarget := sampleSpec("Force target")
	if _, err := ms.SetComponentIntent(ctx, "comp-force", specForceTarget, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent(comp-force) failed: %v", err)
	}
	specForceDep := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "@depends-on: comp-force",
	}
	if _, err := ms.SetComponentIntent(ctx, "dep-force", specForceDep, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent(dep-force) failed: %v", err)
	}
	if _, err := ms.PrepareDeactivation(ctx, "comp-force", true, nil); err != nil {
		t.Fatalf("PrepareDeactivation(comp-force) failed: %v", err)
	}
	if err := ms.CompleteDeactivation(ctx, "comp-force", true, nil); err != nil {
		t.Fatalf("CompleteDeactivation(comp-force, force=true) unexpected error: %v", err)
	}
	recForce, err := ms.GetComponent(ctx, "comp-force")
	if err != nil {
		t.Fatalf("GetComponent(comp-force) failed: %v", err)
	}
	if recForce.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("comp-force State = %v, want DISABLED", recForce.State)
	}

	// 6. Deactivate and complete dependent first
	if _, err := ms.PrepareDeactivation(ctx, "dep-active", false, nil); err != nil {
		t.Fatalf("PrepareDeactivation(dep-active) failed: %v", err)
	}
	if err := ms.CompleteDeactivation(ctx, "dep-active", false, nil); err != nil {
		t.Fatalf("CompleteDeactivation(dep-active) failed: %v", err)
	}

	// 7. Now complete deactivation on root -> succeeds
	if err := ms.CompleteDeactivation(ctx, "comp-deact", false, nil); err != nil {
		t.Fatalf("CompleteDeactivation unexpected error: %v", err)
	}

	rec, err := ms.GetComponent(ctx, "comp-deact")
	if err != nil {
		t.Fatalf("GetComponent failed: %v", err)
	}
	if rec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("State = %v, want DISABLED", rec.State)
	}
	if rec.Status.GetState() != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("Status.State = %v, want DISABLED", rec.Status.GetState())
	}
	if rec.Status.GetLastTransitionTime() == nil {
		t.Error("Status.LastTransitionTime is nil, want non-nil timestamp")
	}

	// 7. PrepareDeactivation on already DISABLED component is a no-op and preserves DISABLED state
	if _, err := ms.PrepareDeactivation(ctx, "comp-deact", false, nil); err != nil {
		t.Fatalf("PrepareDeactivation(already DISABLED) failed: %v", err)
	}
	recAfter, err := ms.GetComponent(ctx, "comp-deact")
	if err != nil {
		t.Fatalf("GetComponent failed: %v", err)
	}
	if recAfter.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("State after PrepareDeactivation on DISABLED component = %v, want DISABLED", recAfter.State)
	}

	// 8. CompleteDeactivation on already DISABLED component is idempotent and succeeds
	if err := ms.CompleteDeactivation(ctx, "comp-deact", false, nil); err != nil {
		t.Errorf("CompleteDeactivation(already DISABLED) unexpected error: %v", err)
	}

	// 9. Nonexistent component
	err = ms.CompleteDeactivation(ctx, "nonexistent", false, nil)
	if status.Code(err) != codes.NotFound {
		t.Errorf("CompleteDeactivation(nonexistent) got code %v, want NotFound", status.Code(err))
	}
}

func TestMemoryStore_ExecuteDeletion(t *testing.T) {
	ctx := context.Background()
	ms := newMemoryStoreWithDependencies(t, sampleDependency)

	// Component 1: Protected
	spec1 := sampleSpec("Protected")
	if _, err := ms.SetComponentIntent(ctx, "comp-prot", spec1, map[string]string{"xmc.dev/deletion-protection": "true"}, 0); err != nil {
		t.Fatalf("SetComponentIntent(comp-prot) failed: %v", err)
	}

	// Attempt deletion without force -> ErrDeletionProtected
	err := ms.ExecuteDeletion(ctx, "comp-prot", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false, nil)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("ExecuteDeletion(protected, force=false) got code %v, want FailedPrecondition", status.Code(err))
	}

	// Deletion with force -> succeeds
	if err := ms.ExecuteDeletion(ctx, "comp-prot", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, true, nil); err != nil {
		t.Fatalf("ExecuteDeletion(protected, force=true) unexpected error: %v", err)
	}

	// Component 2: With active dependent & Non-cascading policy
	specRoot := sampleSpec("Root for deletion")
	if _, err := ms.SetComponentIntent(ctx, "root-del", specRoot, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent(root-del) failed: %v", err)
	}
	specDep := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "@depends-on: root-del",
	}
	if _, err := ms.SetComponentIntent(ctx, "dep-del", specDep, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent(dep-del) failed: %v", err)
	}

	// Attempt non-cascading deletion without force -> ErrHasActiveDependents
	err = ms.ExecuteDeletion(ctx, "root-del", componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, false, nil)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("ExecuteDeletion(with dep, orphan policy, force=false) got code %v, want FailedPrecondition", status.Code(err))
	}

	// Cascading deletion without force -> succeeds and deletes both root and dependent
	if err := ms.ExecuteDeletion(ctx, "root-del", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false, nil); err != nil {
		t.Fatalf("ExecuteDeletion(with dep, delete policy, force=false) unexpected error: %v", err)
	}

	// Verify GetComponent returns NotFound for both root and cascaded deleted dependent
	if _, err := ms.GetComponent(ctx, "root-del"); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(root-del) got code %v, want NotFound", status.Code(err))
	}
	if _, err := ms.GetComponent(ctx, "dep-del"); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(dep-del) got code %v, want NotFound", status.Code(err))
	}

	// Component 3: Cascading deletion with protected dependent
	specParent := sampleSpec("Parent with protected child")
	if _, err := ms.SetComponentIntent(ctx, "parent-prot", specParent, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent(parent-prot) failed: %v", err)
	}
	specChild := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "@depends-on: parent-prot",
	}
	if _, err := ms.SetComponentIntent(ctx, "child-prot", specChild, map[string]string{"xmc.dev/deletion-protection": "true"}, 0); err != nil {
		t.Fatalf("SetComponentIntent(child-prot) failed: %v", err)
	}

	// Cascading deletion without force -> ErrDeletionProtected
	err = ms.ExecuteDeletion(ctx, "parent-prot", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false, nil)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("ExecuteDeletion(cascading with protected child, force=false) got code %v, want FailedPrecondition", status.Code(err))
	}

	// Cascading deletion with force -> succeeds and deletes both parent and child
	if err := ms.ExecuteDeletion(ctx, "parent-prot", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, true, nil); err != nil {
		t.Fatalf("ExecuteDeletion(cascading with protected child, force=true) unexpected error: %v", err)
	}
	if _, err := ms.GetComponent(ctx, "parent-prot"); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(parent-prot) got code %v, want NotFound", status.Code(err))
	}
	if _, err := ms.GetComponent(ctx, "child-prot"); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(child-prot) got code %v, want NotFound", status.Code(err))
	}

	// Component 4: Cascading deletion with disabled dependent
	specRoot4 := sampleSpec("Root 4")
	if _, err := ms.SetComponentIntent(ctx, "root-4", specRoot4, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent(root-4) failed: %v", err)
	}
	specChild4 := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "@depends-on: root-4",
	}
	if _, err := ms.SetComponentIntent(ctx, "child-4", specChild4, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent(child-4) failed: %v", err)
	}
	// Disable child-4
	if _, err := ms.PrepareDeactivation(ctx, "child-4", false, nil); err != nil {
		t.Fatalf("PrepareDeactivation(child-4) failed: %v", err)
	}
	if err := ms.CompleteDeactivation(ctx, "child-4", false, nil); err != nil {
		t.Fatalf("CompleteDeactivation(child-4) failed: %v", err)
	}
	// Cascading deletion of root-4 should delete disabled child-4 too
	if err := ms.ExecuteDeletion(ctx, "root-4", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false, nil); err != nil {
		t.Fatalf("ExecuteDeletion(root-4) failed: %v", err)
	}
	if _, err := ms.GetComponent(ctx, "root-4"); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(root-4) got code %v, want NotFound", status.Code(err))
	}
	if _, err := ms.GetComponent(ctx, "child-4"); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(child-4) got code %v, want NotFound", status.Code(err))
	}

	// Nonexistent deletion
	err = ms.ExecuteDeletion(ctx, "nonexistent", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false, nil)
	if status.Code(err) != codes.NotFound {
		t.Errorf("ExecuteDeletion(nonexistent) got code %v, want NotFound", status.Code(err))
	}
}

func TestMemoryStore_ResolveComponentSnapshot(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	spec1 := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Spec Gen 1",
	}
	if _, err := ms.SetComponentIntent(ctx, "comp-snap", spec1, map[string]string{"env": "prod"}, 1); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	coords1 := &componentspb.ResolvedComponentCoordinates{
		ClusterName: "gke-1",
		Namespace:   "default",
		ServiceName: "snap-svc-v1",
		Port:        8080,
	}
	if _, err := ms.UpdateObservedGeneration(ctx, "comp-snap", 1, &componentspb.ComponentAccessStatus{Coordinates: coords1, Ready: true}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Reconciled Gen 1", nil); err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	// 1. Resolve Gen 1 snapshot
	snap1, snapCoords1, err := ms.ResolveComponentSnapshot(ctx, "comp-snap", 1)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot(1) failed: %v", err)
	}
	if snap1.Generation != 1 {
		t.Errorf("snap1.Generation = %d, want 1", snap1.Generation)
	}
	if snap1.Spec.GetVersion() != "v1.0.0" {
		t.Errorf("snap1.Spec.Version = %q, want 'v1.0.0'", snap1.Spec.GetVersion())
	}
	if snapCoords1 == nil || snapCoords1.GetServiceName() != "snap-svc-v1" {
		t.Errorf("snapCoords1.ServiceName = %v, want 'snap-svc-v1'", snapCoords1)
	}

	// 2. Update to Gen 2
	spec2 := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v2.0.0",
		Description: "Spec Gen 2",
	}
	if _, err := ms.SetComponentIntent(ctx, "comp-snap", spec2, map[string]string{"env": "prod-v2"}, 2); err != nil {
		t.Fatalf("SetComponentIntent(2) failed: %v", err)
	}
	coords2 := &componentspb.ResolvedComponentCoordinates{
		ClusterName: "gke-1",
		Namespace:   "default",
		ServiceName: "snap-svc-v2",
		Port:        9090,
	}
	if _, err := ms.UpdateObservedGeneration(ctx, "comp-snap", 2, &componentspb.ComponentAccessStatus{Coordinates: coords2, Ready: true}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Reconciled Gen 2", nil); err != nil {
		t.Fatalf("UpdateObservedGeneration(2) failed: %v", err)
	}

	// 3. Verify Gen 1 historical snapshot remains completely immutable
	snap1Historical, snapCoords1Historical, err := ms.ResolveComponentSnapshot(ctx, "comp-snap", 1)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot(1 historical) failed: %v", err)
	}
	if snap1Historical.Generation != 1 {
		t.Errorf("snap1Historical.Generation = %d, want 1", snap1Historical.Generation)
	}
	if snap1Historical.Spec.GetVersion() != "v1.0.0" {
		t.Errorf("snap1Historical.Spec.Version = %q, want 'v1.0.0'", snap1Historical.Spec.GetVersion())
	}
	if snap1Historical.Spec.GetDescription() != "Spec Gen 1" {
		t.Errorf("snap1Historical.Spec.Description = %q, want 'Spec Gen 1'", snap1Historical.Spec.GetDescription())
	}
	if snapCoords1Historical == nil || snapCoords1Historical.GetServiceName() != "snap-svc-v1" {
		t.Errorf("snapCoords1Historical.ServiceName = %v, want 'snap-svc-v1'", snapCoords1Historical)
	}

	// 4. Resolve Gen 2 snapshot (and generation=0 for latest)
	snap2, snapCoords2, err := ms.ResolveComponentSnapshot(ctx, "comp-snap", 0)
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot(0 latest) failed: %v", err)
	}
	if snap2.Generation != 2 {
		t.Errorf("snap2.Generation = %d, want 2", snap2.Generation)
	}
	if snap2.Spec.GetVersion() != "v2.0.0" {
		t.Errorf("snap2.Spec.Version = %q, want 'v2.0.0'", snap2.Spec.GetVersion())
	}
	if snapCoords2 == nil || snapCoords2.GetServiceName() != "snap-svc-v2" {
		t.Errorf("snapCoords2.ServiceName = %v, want 'snap-svc-v2'", snapCoords2)
	}
}

func TestMemoryStore_ValidateComponentRequirement(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.4.0",
	}
	if _, err := ms.SetComponentIntent(ctx, "comp-req", spec, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	// 1. Pending reconciliation -> not satisfied
	resPending, err := ms.ValidateComponentRequirement(ctx, "comp-req", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "v1.0.0")
	if err != nil {
		t.Fatalf("ValidateComponentRequirement unexpected error: %v", err)
	}
	if resPending.IsSatisfied {
		t.Error("IsSatisfied = true for pending reconciliation component, want false")
	}

	// 2. Transition to ENABLED -> satisfied
	if _, err := ms.UpdateObservedGeneration(ctx, "comp-req", 1, &componentspb.ComponentAccessStatus{Ready: true}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "OK", nil); err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}
	resEnabled, err := ms.ValidateComponentRequirement(ctx, "comp-req", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "v1.2.0")
	if err != nil {
		t.Fatalf("ValidateComponentRequirement unexpected error: %v", err)
	}
	if !resEnabled.IsSatisfied {
		t.Errorf("IsSatisfied = false for ENABLED component, want true (reason: %q)", resEnabled.Reason)
	}

	// 3. Higher version requirement -> unsatisfied
	resHigher, err := ms.ValidateComponentRequirement(ctx, "comp-req", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "v2.0.0")
	if err != nil {
		t.Fatalf("ValidateComponentRequirement unexpected error: %v", err)
	}
	if resHigher.IsSatisfied {
		t.Error("IsSatisfied = true for higher version requirement, want false")
	}
}

func TestMemoryStore_GetPlatformHealth(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("health-comp-%d", i)
		spec := &componentspb.ComponentSpec{
			Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version: "v1.0.0",
		}
		if _, err := ms.SetComponentIntent(ctx, name, spec, nil, 0); err != nil {
			t.Fatalf("SetComponentIntent(%s) failed: %v", name, err)
		}
		state := componentspb.ComponentState_COMPONENT_STATE_ENABLED
		ready := i%2 == 0
		if _, err := ms.UpdateObservedGeneration(ctx, name, 1, &componentspb.ComponentAccessStatus{Ready: ready}, state, "OK", nil); err != nil {
			t.Fatalf("UpdateObservedGeneration(%s) failed: %v", name, err)
		}
	}

	resp, err := ms.GetPlatformHealth(ctx, "state=ENABLED", 2, "")
	if err != nil {
		t.Fatalf("GetPlatformHealth failed: %v", err)
	}
	if len(resp.GetComponents()) != 2 {
		t.Errorf("len(Components) = %d, want 2", len(resp.GetComponents()))
	}
	if resp.GetTotalHealthy() != 3 {
		t.Errorf("TotalHealthy = %d, want 3", resp.GetTotalHealthy())
	}
	if resp.GetTotalUnhealthy() != 2 {
		t.Errorf("TotalUnhealthy = %d, want 2", resp.GetTotalUnhealthy())
	}
	if resp.GetNextPageToken() == "" {
		t.Error("NextPageToken is empty, want token")
	}
}
