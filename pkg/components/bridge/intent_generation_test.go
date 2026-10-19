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

// In-process integration tests for Reconciler intent generation and CMS state synchronization.
// Production targets: reconciler.go (Reconciler.ReconcileComponent) and lifecycle.go
// (applyComponentIntent).
// Test type: hermetic in-process integration test (wires Reconciler to an in-process cms.Server +
// store.MemoryStore via NewCMSClientAdapter, without a Kubernetes cluster). Kept separate from
// reconciler_test.go and lifecycle_test.go (which use mockCMS) to keep multi-pass bridge <-> CMS
// generation and ownership scenarios self-contained.
package bridge

import (
	"context"
	"testing"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	cmsserver "xmanager_cloud/pkg/components/cms"
	"xmanager_cloud/pkg/components/store"
)

func TestReconcileComponent_IntentCarriesCRGeneration(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "enabled", enabled: true},
		{name: "disabled", enabled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cms := &mockCMS{}
			r, err := NewReconciler(cms, nil, nil)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			in := ReconcileComponentInput{
				Namespace: "xmc", Name: "redpanda", Generation: 7, Enabled: tt.enabled,
				Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
			}
			if _, err := r.ReconcileComponent(context.Background(), in); err != nil {
				t.Fatalf("ReconcileComponent() error = %v", err)
			}
			if got := cms.lastSetReq.GetSpec().GetGeneration(); got != in.Generation {
				t.Errorf("SetComponentIntent spec.generation = %d, want CR generation %d", got, in.Generation)
			}
		})
	}
}

// TestReconcileComponent_UnchangedIntentKeepsCMSRecord verifies against the real CMS server and
// MemoryStore that repeated passes over an unchanged Component neither bump the CMS generation past
// the CR generation nor reset the observed ENABLED state.
func TestReconcileComponent_UnchangedIntentKeepsCMSRecord(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	r, err := NewReconciler(NewCMSClientAdapter(cmsserver.NewServer(ms, cmsserver.WithAuthorizer(cmsserver.NewDisabledAuthorizer()))), nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	in := ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 3, Enabled: true,
		Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
	}
	for pass := 1; pass <= 3; pass++ {
		res, err := r.ReconcileComponent(ctx, in)
		if err != nil {
			t.Fatalf("pass %d: ReconcileComponent() error = %v", pass, err)
		}
		rec, err := ms.GetComponent(ctx, "redpanda")
		if err != nil {
			t.Fatalf("pass %d: GetComponent() error = %v", pass, err)
		}
		if rec.Generation != in.Generation || rec.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
			t.Errorf("pass %d: CMS generation = %d, state = %v; want %d, COMPONENT_STATE_ENABLED", pass, rec.Generation, rec.State, in.Generation)
		}
		in.ExistingConditions = res.Conditions
	}
}

func TestReconcileComponent_SourcedIntentAndSameClusterRecreateAdoption(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	cmsClient := NewCMSClientAdapter(cmsserver.NewServer(ms, cmsserver.WithAuthorizer(cmsserver.NewDisabledAuthorizer())))
	cluster := &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "primary", Epoch: 1}
	fence, err := NewClusterFence(cmsClient, cluster, ClusterFenceOptions{})
	if err != nil {
		t.Fatalf("NewClusterFence() error = %v", err)
	}

	const (
		uidA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		uidB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	)
	liveUID := uidA
	r, err := NewReconciler(
		cmsClient,
		nil,
		nil,
		WithClusterFence(fence),
		WithLiveUIDReader(func(context.Context, string, string) (string, error) { return liveUID, nil }),
	)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}

	specV1 := func() *componentspb.ComponentSpec {
		return &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "1.0.0"}
	}
	specV2 := func() *componentspb.ComponentSpec {
		return &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "2.0.0"}
	}

	// Pass 1: uidA at CR generation 1 creates the CMS record at componentGeneration 1.
	res1, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 1, UID: uidA, Enabled: true, Spec: specV1(),
	})
	if err != nil {
		t.Fatalf("pass 1: ReconcileComponent() error = %v", err)
	}
	if res1.ObservedGeneration != 1 || res1.ComponentGeneration != 1 {
		t.Fatalf("pass 1 generations = (obs=%d, comp=%d), want (1, 1)", res1.ObservedGeneration, res1.ComponentGeneration)
	}
	rec1, err := ms.GetComponent(ctx, "redpanda")
	if err != nil {
		t.Fatalf("GetComponent() error = %v", err)
	}
	if rec1.Owner == nil || rec1.Owner.UID != uidA || rec1.ObservedGeneration == nil || *rec1.ObservedGeneration != 1 {
		t.Fatalf("pass 1 CMS record = (owner=%+v, obsGen=%v), want uidA and obsGen 1", rec1.Owner, rec1.ObservedGeneration)
	}

	// Pass 2: uidA updates spec at CR generation 2 -> componentGeneration becomes 2.
	res2, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 2, UID: uidA, ComponentGeneration: res1.ComponentGeneration,
		Enabled: true, ExistingConditions: res1.Conditions, Spec: specV2(),
	})
	if err != nil {
		t.Fatalf("pass 2: ReconcileComponent() error = %v", err)
	}
	if res2.ComponentGeneration != 2 {
		t.Fatalf("pass 2 ComponentGeneration = %d, want 2", res2.ComponentGeneration)
	}

	// Pass 3a: Stale local informer event from uidB while liveUID is uidA -> StaleIncarnation=true (no SourceConflict status overwrite).
	resStaleLocal, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 1, UID: uidB, ComponentGeneration: 2,
		Enabled: true, Spec: specV2(),
	})
	if err != nil {
		t.Fatalf("pass 3a (stale local incarnation): unexpected error = %v", err)
	}
	if !resStaleLocal.StaleIncarnation || hasSourceConflict(resStaleLocal) {
		t.Fatalf("pass 3a StaleIncarnation=%v hasSourceConflict=%v, want true/false", resStaleLocal.StaleIncarnation, hasSourceConflict(resStaleLocal))
	}
	recStale, err := ms.GetComponent(ctx, "redpanda")
	if err != nil {
		t.Fatalf("pass 3a GetComponent() error = %v", err)
	}
	if recStale.Owner == nil || recStale.Owner.UID != uidA {
		t.Fatalf("pass 3a CMS owner = %+v, want uidA: a stale incarnation must not take over the record", recStale.Owner)
	}

	// Pass 3b: Standby cluster takes over fence at epoch 2 -> primary data RPC returns CLUSTER_FENCED and notifies fence.Changes() immediately.
	if _, err := ms.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: testStandbyClusterUID, Name: "standby", Epoch: 2}); err != nil {
		t.Fatalf("DeclareClusterEpoch(standby epoch 2) error = %v", err)
	}
	resFencedDataRPC, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 2, UID: uidA,
		Enabled: true, Spec: specV2(),
	})
	if err != nil {
		t.Fatalf("pass 3b (fenced data RPC): unexpected error = %v", err)
	}
	assertSourceConflictConditions(t, resFencedDataRPC.Conditions, ReasonClusterFenced)
	select {
	case <-fence.Changes():
	default:
		t.Errorf("expected data RPC CLUSTER_FENCED to notify fence.Changes() immediately")
	}

	// Re-declare primary cluster at epoch 3 so fence is active again.
	fence, err = NewClusterFence(cmsClient, &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "primary", Epoch: 3}, ClusterFenceOptions{})
	if err != nil {
		t.Fatalf("NewClusterFence(epoch 3) error = %v", err)
	}
	r.fence = fence

	// Pass 4: CR is recreated in-cluster (liveUID is now uidB) at CR generation 1 with v1 spec -> bumps componentGeneration to 3 and syncs observed generation.
	liveUID = uidB
	resAdopted, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 1, UID: uidB, Enabled: true,
		ExistingConditions: resFencedDataRPC.Conditions, Spec: specV1(),
	})
	if err != nil {
		t.Fatalf("pass 4 (adopted recreation): ReconcileComponent() error = %v", err)
	}
	if resAdopted.ComponentGeneration != 3 {
		t.Errorf("pass 4 ComponentGeneration = %d, want 3 after spec change on adoption", resAdopted.ComponentGeneration)
	}
	if sc := findCondition(resAdopted.Conditions, ConditionTypeSourceConflict); sc != nil {
		t.Errorf("pass 4 SourceConflict = %+v, want pruned after adoption", sc)
	}
	recAdopted, err := ms.GetComponent(ctx, "redpanda")
	if err != nil {
		t.Fatalf("GetComponent() error = %v", err)
	}
	if recAdopted.ComponentID != rec1.ComponentID || recAdopted.Owner == nil || recAdopted.Owner.UID != uidB || recAdopted.ObservedGeneration == nil || *recAdopted.ObservedGeneration != 3 {
		t.Errorf("pass 4 CMS record = (id=%d, owner=%+v, obsGen=%v), want id=%d, owner uidB, obsGen 3", recAdopted.ComponentID, recAdopted.Owner, recAdopted.ObservedGeneration, rec1.ComponentID)
	}
}

func TestReconcileComponent_SourceNamespaceConflictAndStaleGeneration(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	cmsClient := NewCMSClientAdapter(cmsserver.NewServer(ms, cmsserver.WithAuthorizer(cmsserver.NewDisabledAuthorizer())))
	cluster := &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "primary", Epoch: 1}
	fence, err := NewClusterFence(cmsClient, cluster, ClusterFenceOptions{})
	if err != nil {
		t.Fatalf("NewClusterFence() error = %v", err)
	}
	helm := &mockHelm{}
	r, err := NewReconciler(cmsClient, nil, helm, WithClusterFence(fence))
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}

	const (
		uidTeamA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		uidTeamB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	)
	specV1 := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "1.0.0"}
	specV2 := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "2.0.0"}

	// team-a claims redpanda at generation 2.
	if _, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
		Namespace: "team-a", Name: "redpanda", Generation: 2, UID: uidTeamA, Enabled: true, Spec: specV2,
	}); err != nil {
		t.Fatalf("team-a ReconcileComponent() error = %v", err)
	}
	helm.ensureCalls = 0

	// team-b attempts to reconcile redpanda (enabled and disabled) -> SourceNamespaceConflict without Helm side effects.
	conflictTests := []struct {
		name    string
		enabled bool
	}{
		{name: "team-b enabled", enabled: true},
		{name: "team-b disabled", enabled: false},
	}
	for _, tt := range conflictTests {
		t.Run(tt.name, func(t *testing.T) {
			resConflict, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
				Namespace: "team-b", Name: "redpanda", Generation: 1, UID: uidTeamB, Enabled: tt.enabled, Spec: specV1,
			})
			if err != nil {
				t.Fatalf("team-b (enabled=%v) ReconcileComponent() unexpected error: %v", tt.enabled, err)
			}
			if helm.ensureCalls != 0 || helm.deleteCalled {
				t.Errorf("team-b (enabled=%v) actuated Helm on namespace conflict: ensure=%d delete=%v", tt.enabled, helm.ensureCalls, helm.deleteCalled)
			}
			assertSourceConflictConditions(t, resConflict.Conditions, ReasonSourceNamespaceConflict)
		})
	}

	// team-a sends a changed spec at stale generation 1 -> a stale snapshot, not a SourceConflict:
	// no status write (StaleIncarnation) and a short requeue so the cache catches up.
	resStaleGen, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
		Namespace: "team-a", Name: "redpanda", Generation: 1, UID: uidTeamA, Enabled: true, Spec: specV1,
	})
	if err != nil {
		t.Fatalf("team-a stale generation ReconcileComponent() unexpected error: %v", err)
	}
	if !resStaleGen.StaleIncarnation || resStaleGen.RequeueAfter != staleComponentRetryDelay {
		t.Errorf("stale generation result = (StaleIncarnation %v, RequeueAfter %v), want (true, %v)", resStaleGen.StaleIncarnation, resStaleGen.RequeueAfter, staleComponentRetryDelay)
	}
	if c := findCondition(resStaleGen.Conditions, ConditionTypeSourceConflict); c != nil {
		t.Errorf("stale generation reported SourceConflict %+v, want none", c)
	}
	if helm.ensureCalls != 0 {
		t.Errorf("stale generation actuated Helm %d times, want 0", helm.ensureCalls)
	}
}
