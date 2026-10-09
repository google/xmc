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

package bridge

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// TestReconcileComponent_EnabledCMSSyncRetriesAfterFailure checks that a failed observed-state
// report of an enabled component fails the pass and is retried on the next pass, which is fed the
// status the bridge persisted, rather than after the CMS heartbeat expires: the failed pass keeps
// the observed Ready status and reason and clears the Ready lastTransitionTime. Once a retry
// succeeds, a later unchanged pass makes no UpdateObservedGeneration call. The 3P wrapper row
// runs without a status detector, so it reports through handle3PReadyStatus. The disabled path is
// covered by TestReconcileComponent_DisabledCMSSyncRetriesAfterFailure.
func TestReconcileComponent_EnabledCMSSyncRetriesAfterFailure(t *testing.T) {
	tests := []struct {
		name       string
		in         ReconcileComponentInput
		wantStatus string
		wantReason string
	}{
		{
			name:       "healthy",
			in:         sourcedRedpandaInput("uid-a", time.Unix(1, 0), 1),
			wantStatus: ConditionStatusTrue,
			wantReason: ReasonComponentHealthy,
		},
		{
			name: "ready 3P wrapper",
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ray-1", Generation: 1, HasFinalizer: true, Enabled: true,
				Spec: &componentspb.ComponentSpec{
					Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					WrapperRef: &componentspb.WrapperRef{CrdGroup: "ray.io", CrdVersion: "v1", CrdKind: "RayCluster", TargetName: "ray-1", TargetNamespace: "xmc"},
				},
			},
			wantStatus: ConditionStatusTrue,
			wantReason: ReasonComponentHealthy,
		},
		{
			name: "gated on an unregistered dependency",
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, HasFinalizer: true, Enabled: true,
				Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: []string{"redpanda"}},
				Dependencies: NewDependencySnapshot(nil),
			},
			wantStatus: ConditionStatusFalse,
			wantReason: ReasonDependenciesNotReady,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failed, err := reconcileWithCMS(&mockCMS{obsErr: status.Error(codes.Unavailable, "cms down")}, tt.in)
			if err == nil {
				t.Fatal("failed pass: ReconcileComponent() error = nil, want the UpdateObservedGeneration failure")
			}
			ready := findCondition(failed.Conditions, ConditionTypeReady)
			if ready == nil || ready.Status != tt.wantStatus || ready.Reason != tt.wantReason || !ready.LastTransitionTime.IsZero() {
				t.Fatalf("failed pass: Ready condition = %+v, want status %q, reason %q and a zero lastTransitionTime", ready, tt.wantStatus, tt.wantReason)
			}

			retry := persistedInput(tt.in, failed)
			cms := &mockCMS{}
			retried, err := reconcileWithCMS(cms, retry)
			if err != nil || cms.obsCalls != 1 {
				t.Fatalf("retry pass: ReconcileComponent() error = %v, UpdateObservedGeneration calls = %d; want nil, 1", err, cms.obsCalls)
			}
			checkReadyTransition(t, retry, retried, transitionReset)

			cms = &mockCMS{}
			if _, err := reconcileWithCMS(cms, persistedInput(tt.in, retried)); err != nil || cms.obsCalls != 0 {
				t.Errorf("steady pass: ReconcileComponent() error = %v, UpdateObservedGeneration calls = %d; want nil, 0", err, cms.obsCalls)
			}
		})
	}
}

// reconcileWithCMS runs one pass over in against cms, without a Helm manager.
func reconcileWithCMS(cms *mockCMS, in ReconcileComponentInput) (*ReconcileResult, error) {
	r, err := NewReconciler(cms, nil, nil)
	if err != nil {
		return nil, err
	}
	return r.ReconcileComponent(context.Background(), in)
}

// persistedInput returns the next pass over in after the bridge persisted res: its conditions and
// CMS generation are read back from the status map that BuildStatusMap writes.
func persistedInput(in ReconcileComponentInput, res *ReconcileResult) ReconcileComponentInput {
	persisted := BuildStatusMap(res)
	next := withExisting(in, ParseConditionsFromUnstructured(persisted))
	return withComponentGeneration(next, ParseComponentGeneration(persisted))
}
