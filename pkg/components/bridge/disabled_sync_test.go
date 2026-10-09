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
	"log/slog"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

const componentDisabledMessage = "Component disabled"

// readyTransitionCheck is what a disabled sync test expects of the Ready lastTransitionTime, the
// CMS heartbeat, relative to the existing Ready condition.
type readyTransitionCheck int

const (
	transitionUnchecked readyTransitionCheck = iota
	// transitionKept expects the existing time: no sync restarted the heartbeat.
	transitionKept
	// transitionReset expects a time within the last minute: a sync restarted the heartbeat.
	transitionReset
)

// TestReconcileComponent_DisabledCMSSync checks that a disabled component syncs CMS and logs at
// Info on a pass that changes its Ready or Degraded condition, such as the one that disables it,
// and that a later unchanged pass makes no UpdateObservedGeneration call, keeps the heartbeat and
// logs at Debug unless the CMS heartbeat expired or the CMS component generation changed. An
// ownership release on the disabled path is covered by
// TestReconcileComponent_DisabledStaleObservationReleasesOwnership.
func TestReconcileComponent_DisabledCMSSync(t *testing.T) {
	first := disabledInput()
	first.ExistingConditions = []Condition{{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy}}
	steady := disabledSteadyInput(t, first)
	tests := []struct {
		name         string
		in           ReconcileComponentInput
		obsErr       error
		wantObsCalls int
		wantLevel    slog.Level
		wantReady    readyTransitionCheck
	}{
		{name: "first disable", in: first, wantObsCalls: 1, wantLevel: slog.LevelInfo, wantReady: transitionReset},
		{
			name: "first disable of a component unknown to CMS", in: first, obsErr: status.Error(codes.NotFound, "component not found"),
			wantObsCalls: 1, wantLevel: slog.LevelInfo, wantReady: transitionReset,
		},
		{name: "steady disabled pass", in: steady, wantObsCalls: 0, wantLevel: slog.LevelDebug, wantReady: transitionKept},
		{
			name: "heartbeat expired",
			in: withExisting(steady, withCondition(steady.ExistingConditions, ConditionTypeReady, func(c *Condition) {
				c.LastTransitionTime = time.Now().Add(-defaultCMSHeartbeatInterval)
			})),
			wantObsCalls: 1, wantLevel: slog.LevelDebug, wantReady: transitionReset,
		},
		{name: "component generation changed", in: withComponentGeneration(steady, steady.ComponentGeneration-1), wantObsCalls: 1, wantLevel: slog.LevelDebug},
		{
			name:         "Degraded condition changed",
			in:           withExisting(steady, withCondition(steady.ExistingConditions, ConditionTypeDegraded, func(c *Condition) { c.Status = ConditionStatusTrue })),
			wantObsCalls: 1, wantLevel: slog.LevelInfo,
		},
		{
			// Same Ready status, generation, component generation and a fresh heartbeat: only the
			// reason changes, which shouldSyncToCMS alone would not sync.
			name:         "Ready reason changed from DeactivationIncomplete",
			in:           withExisting(steady, withCondition(steady.ExistingConditions, ConditionTypeReady, func(c *Condition) { c.Reason = ReasonDeactivationIncomplete })),
			wantObsCalls: 1, wantLevel: slog.LevelInfo, wantReady: transitionReset,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cms := &mockCMS{obsErr: tt.obsErr}
			r, err := NewReconciler(cms, nil, &mockHelm{})
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			ctx, logs := recordLogs()
			res, err := r.ReconcileComponent(ctx, tt.in)
			if err != nil {
				t.Fatalf("ReconcileComponent() error = %v", err)
			}
			if res.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
				t.Errorf("ReconcileComponent() state = %v, want %v", res.State, componentspb.ComponentState_COMPONENT_STATE_DISABLED)
			}
			if cms.obsCalls != tt.wantObsCalls {
				t.Errorf("UpdateObservedGeneration calls = %d, want %d", cms.obsCalls, tt.wantObsCalls)
			}
			if got := logs.singleRecord(t, componentDisabledMessage); got.level != tt.wantLevel {
				t.Errorf("%q level = %v, want %v", componentDisabledMessage, got.level, tt.wantLevel)
			}
			checkReadyTransition(t, tt.in, res, tt.wantReady)
		})
	}
}

// TestReconcileComponent_DisabledCMSSyncRetriesAfterFailure checks that a failed observed-state
// report of a disabled component is retried on the next pass, which is fed the result the bridge
// persisted, rather than after the CMS heartbeat expires.
func TestReconcileComponent_DisabledCMSSyncRetriesAfterFailure(t *testing.T) {
	first := disabledInput()
	first.ExistingConditions = []Condition{{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy}}
	r, err := NewReconciler(&mockCMS{obsErr: status.Error(codes.Unavailable, "cms down")}, nil, &mockHelm{})
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	failed, err := r.ReconcileComponent(context.Background(), first)
	if err == nil {
		t.Fatal("first pass: ReconcileComponent() error = nil, want the UpdateObservedGeneration failure")
	}
	if ready := findCondition(failed.Conditions, ConditionTypeReady); ready == nil || ready.Reason != ReasonDeactivationIncomplete {
		t.Fatalf("first pass: Ready condition = %+v, want reason %q", ready, ReasonDeactivationIncomplete)
	}

	cms := &mockCMS{}
	r, err = NewReconciler(cms, nil, &mockHelm{})
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	next := withComponentGeneration(withExisting(first, failed.Conditions), failed.ComponentGeneration)
	res, err := r.ReconcileComponent(context.Background(), next)
	if err != nil {
		t.Fatalf("second pass: ReconcileComponent() error = %v", err)
	}
	if cms.obsCalls != 1 || res.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("second pass: UpdateObservedGeneration calls = %d, state = %v; want 1, %v", cms.obsCalls, res.State, componentspb.ComponentState_COMPONENT_STATE_DISABLED)
	}
}

// TestReconcileComponent_DisabledStaleObservationReleasesOwnership checks that a disabled
// component whose observed-state report CMS rejects as stale releases CMS ownership (reason
// StaleIntent, componentGeneration dropped) without failing the pass or logging it as disabled.
func TestReconcileComponent_DisabledStaleObservationReleasesOwnership(t *testing.T) {
	first := disabledInput()
	first.ExistingConditions = []Condition{{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy}}
	cms := &mockCMS{obsErr: status.Error(codes.FailedPrecondition, `stale observation: component "redpanda" is owned by source "uid-b"`)}
	r, err := NewReconciler(cms, nil, &mockHelm{})
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	ctx, logs := recordLogs()
	res, err := r.ReconcileComponent(ctx, first)
	if err != nil {
		t.Fatalf("ReconcileComponent() error = %v", err)
	}
	if ready := findCondition(res.Conditions, ConditionTypeReady); ready == nil || ready.Reason != ReasonStaleIntent || res.ComponentGeneration != 0 {
		t.Errorf("Ready condition = %+v, componentGeneration = %d; want reason %q, 0", ready, res.ComponentGeneration, ReasonStaleIntent)
	}
	if got := logs.recordsFor(componentDisabledMessage); len(got) != 0 {
		t.Errorf("%q logged %d times, want 0 after an ownership release", componentDisabledMessage, len(got))
	}
}

// checkReadyTransition checks the Ready lastTransitionTime of res against that of in.
func checkReadyTransition(t *testing.T, in ReconcileComponentInput, res *ReconcileResult, want readyTransitionCheck) {
	t.Helper()
	if want == transitionUnchecked {
		return
	}
	got := findCondition(res.Conditions, ConditionTypeReady)
	if got == nil {
		t.Fatal("Ready condition missing")
	}
	if want == transitionKept {
		if existing := findCondition(in.ExistingConditions, ConditionTypeReady); existing == nil || !got.LastTransitionTime.Equal(existing.LastTransitionTime) {
			t.Errorf("Ready lastTransitionTime = %v, want the existing %+v", got.LastTransitionTime, existing)
		}
		return
	}
	if time.Since(got.LastTransitionTime) > time.Minute {
		t.Errorf("Ready lastTransitionTime = %v, want it reset to now", got.LastTransitionTime)
	}
}

// disabledInput returns a pass over a disabled 1P component at generation 2 with no CMS
// generation reported yet.
func disabledInput() ReconcileComponentInput {
	return ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 2, HasFinalizer: true, Enabled: false,
		Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
	}
}

// disabledSteadyInput runs one pass over first, which must disable the component, and returns the
// next pass over it: its conditions and CMS generation are those first reported.
func disabledSteadyInput(t *testing.T, first ReconcileComponentInput) ReconcileComponentInput {
	t.Helper()
	r, err := NewReconciler(&mockCMS{}, nil, &mockHelm{})
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	res, err := r.ReconcileComponent(context.Background(), first)
	if err != nil {
		t.Fatalf("ReconcileComponent() error = %v", err)
	}
	if ready := findCondition(res.Conditions, ConditionTypeReady); ready == nil || ready.Reason != reasonComponentDisabled {
		t.Fatalf("Ready condition = %+v, want reason %q", ready, reasonComponentDisabled)
	}
	next := withExisting(first, res.Conditions)
	return withComponentGeneration(next, res.ComponentGeneration)
}

// withExisting returns a copy of in with ExistingConditions set to conds.
func withExisting(in ReconcileComponentInput, conds []Condition) ReconcileComponentInput {
	in.ExistingConditions = conds
	return in
}

// withComponentGeneration returns a copy of in with ComponentGeneration set to gen.
func withComponentGeneration(in ReconcileComponentInput, gen int64) ReconcileComponentInput {
	in.ComponentGeneration = gen
	return in
}
