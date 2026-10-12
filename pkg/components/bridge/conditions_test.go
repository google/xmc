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

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func TestSetReadiness_KstatusConditions(t *testing.T) {
	tests := []struct {
		name            string
		ready           string
		reason          string
		wantReconciling string
		wantStalled     string
	}{
		{name: "healthy settled", ready: ConditionStatusTrue, reason: ReasonComponentHealthy},
		{name: "disabled settled", ready: ConditionStatusFalse, reason: reasonComponentDisabled},
		{name: "initial sync pending", ready: ConditionStatusUnknown, reason: ReasonInitialSyncPending, wantReconciling: ReasonProgressing},
		{name: "helm upgrade pending", ready: ConditionStatusFalse, reason: ReasonHelmUpgradePending, wantReconciling: ReasonUpgradePending},
		{name: "dependencies not ready", ready: ConditionStatusFalse, reason: ReasonDependenciesNotReady, wantReconciling: ReasonDependenciesPending},
		{name: "deactivation incomplete", ready: ConditionStatusFalse, reason: ReasonDeactivationIncomplete, wantReconciling: ReasonDisabling},
		{name: "helm deletion failed", ready: ConditionStatusFalse, reason: "HelmDeletionFailed", wantReconciling: ReasonDisabling},
		{name: "helm orphan failed", ready: ConditionStatusFalse, reason: "HelmOrphanFailed", wantReconciling: ReasonDisabling},
		{name: "helm release not found", ready: ConditionStatusFalse, reason: ReasonHelmReleaseNotFound, wantReconciling: ReasonProgressing},
		{name: "helm release not ready", ready: ConditionStatusFalse, reason: ReasonHelmReleaseNotReady, wantReconciling: ReasonProgressing},
		{name: "helm release drifted", ready: ConditionStatusFalse, reason: ReasonHelmReleaseDrifted, wantReconciling: ReasonProgressing},
		{name: "helm status unavailable", ready: ConditionStatusFalse, reason: ReasonHelmStatusUnavailable, wantReconciling: ReasonProgressing},
		{name: "3P resource not found", ready: ConditionStatusFalse, reason: "ResourceNotFound", wantReconciling: ReasonProgressing},
		{name: "3P resource not ready", ready: ConditionStatusFalse, reason: "ResourceNotReady", wantReconciling: ReasonProgressing},
		{name: "3P status detection failed", ready: ConditionStatusFalse, reason: ReasonStatusDetectionFailed, wantReconciling: ReasonProgressing},
		{name: "health probe failed", ready: ConditionStatusFalse, reason: ReasonHealthProbeFailed, wantReconciling: ReasonProgressing},
		{name: "3P probe failed", ready: ConditionStatusFalse, reason: "ProbeFailed", wantReconciling: ReasonProgressing},
		{name: "3P probe unhealthy", ready: ConditionStatusFalse, reason: "ProbeUnhealthy", wantReconciling: ReasonProgressing},
		{name: "helm upgrade failed", ready: ConditionStatusFalse, reason: ReasonHelmUpgradeFailed, wantStalled: ReasonHelmUpgradeFailed},
		{name: "helm release stuck", ready: ConditionStatusFalse, reason: ReasonHelmReleaseStuck, wantStalled: ReasonHelmReleaseStuck},
		{name: "helm runner uninitialized", ready: ConditionStatusFalse, reason: ReasonHelmRunnerUninitialized, wantStalled: ReasonHelmRunnerUninitialized},
		{name: "invalid component ref", ready: ConditionStatusFalse, reason: ReasonInvalidComponentRef, wantStalled: ReasonInvalidComponentRef},
		{name: "dependency cycle", ready: ConditionStatusFalse, reason: ReasonDependencyCycle, wantStalled: ReasonDependencyCycle},
		{name: "deactivation blocked", ready: ConditionStatusFalse, reason: ReasonDeactivationBlocked, wantStalled: ReasonDeactivationBlocked},
		{name: "contract broken", ready: ConditionStatusFalse, reason: ReasonContractBroken, wantStalled: ReasonContractBroken},
		{name: "invalid wrapper spec", ready: ConditionStatusFalse, reason: ReasonInvalidWrapperSpec, wantStalled: ReasonInvalidWrapperSpec},
		{name: "invalid wrapper ref", ready: ConditionStatusFalse, reason: "InvalidWrapperRef", wantStalled: "InvalidWrapperRef"},
		{name: "cross namespace forbidden", ready: ConditionStatusFalse, reason: "CrossNamespaceForbidden", wantStalled: "CrossNamespaceForbidden"},
		{name: "3P CRD not found", ready: ConditionStatusFalse, reason: "CRDNotFound", wantStalled: "CRDNotFound"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &ReconcileResult{}
			setReadiness(res, ReconcileComponentInput{Generation: 3}, tt.ready, tt.reason, "msg")
			readyCond := findCondition(res.Conditions, ConditionTypeReady)
			if readyCond == nil || readyCond.Status != tt.ready || readyCond.Reason != tt.reason {
				t.Errorf("Ready condition = %+v, want status=%s reason=%s", readyCond, tt.ready, tt.reason)
			}
			recCond := findCondition(res.Conditions, ConditionTypeReconciling)
			if tt.wantReconciling == "" && recCond != nil {
				t.Errorf("unexpected Reconciling condition: %+v", recCond)
			}
			if tt.wantReconciling != "" && (recCond == nil || recCond.Status != ConditionStatusTrue || recCond.Reason != tt.wantReconciling) {
				t.Errorf("Reconciling condition = %+v, want True/%s", recCond, tt.wantReconciling)
			}
			stalledCond := findCondition(res.Conditions, ConditionTypeStalled)
			if tt.wantStalled == "" && stalledCond != nil {
				t.Errorf("unexpected Stalled condition: %+v", stalledCond)
			}
			if tt.wantStalled != "" && (stalledCond == nil || stalledCond.Status != ConditionStatusTrue || stalledCond.Reason != tt.wantStalled) {
				t.Errorf("Stalled condition = %+v, want True/%s", stalledCond, tt.wantStalled)
			}
		})
	}
}

func TestSetCondition(t *testing.T) {
	prev := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	existing := []Condition{
		{Type: ConditionTypeReady, Status: ConditionStatusTrue, LastTransitionTime: prev},
	}
	var conds []Condition

	// Test 1: Preserves existing timestamp when status matches
	setCondition(&conds, existing, ConditionTypeReady, ConditionStatusTrue, 1, "R", "M")
	if len(conds) != 1 || !conds[0].LastTransitionTime.Equal(prev) {
		t.Errorf("expected transition time preserved: %+v", conds)
	}

	// Test 2: Updates condition when status changes
	setCondition(&conds, existing, ConditionTypeReady, ConditionStatusFalse, 2, "R2", "M2")
	if len(conds) != 1 || conds[0].Status != ConditionStatusFalse || conds[0].LastTransitionTime.Equal(prev) {
		t.Errorf("expected updated condition with new transition time: %+v", conds)
	}

	// Test 3: Appends a new condition of a different type
	setCondition(&conds, existing, ConditionTypeReconciling, ConditionStatusTrue, 2, ReasonProgressing, "ProgressingMsg")
	if len(conds) != 2 {
		t.Errorf("expected 2 conditions, got: %+v", conds)
	}
}

func TestCMSSyncTracker_ShouldSyncAndForget(t *testing.T) {
	base := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	enabled := componentspb.ComponentState_COMPONENT_STATE_ENABLED
	pending := componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION

	tests := []struct {
		name        string
		recorded    bool
		age         time.Duration
		recState    componentspb.ComponentState
		recReady    string
		recGen      int64
		targetState componentspb.ComponentState
		targetReady string
		targetGen   int64
		wantSync    bool
	}{
		{name: "initial reconciliation", targetState: enabled, targetReady: ConditionStatusTrue, targetGen: 1, wantSync: true},
		{name: "steady state within heartbeat interval", recorded: true, age: time.Minute, recState: enabled, recReady: ConditionStatusTrue, recGen: 1, targetState: enabled, targetReady: ConditionStatusTrue, targetGen: 1, wantSync: false},
		{name: "readiness transition False to True", recorded: true, age: time.Minute, recState: pending, recReady: ConditionStatusFalse, recGen: 1, targetState: enabled, targetReady: ConditionStatusTrue, targetGen: 1, wantSync: true},
		{name: "readiness transition True to False", recorded: true, age: time.Minute, recState: enabled, recReady: ConditionStatusTrue, recGen: 1, targetState: pending, targetReady: ConditionStatusFalse, targetGen: 1, wantSync: true},
		{name: "state transition with same Ready status", recorded: true, age: time.Minute, recState: pending, recReady: ConditionStatusFalse, recGen: 1, targetState: componentspb.ComponentState_COMPONENT_STATE_DISABLED, targetReady: ConditionStatusFalse, targetGen: 1, wantSync: true},
		{name: "metadata generation increment", recorded: true, age: time.Minute, recState: enabled, recReady: ConditionStatusTrue, recGen: 1, targetState: enabled, targetReady: ConditionStatusTrue, targetGen: 2, wantSync: true},
		{name: "coarse heartbeat expired after 5 minutes", recorded: true, age: 6 * time.Minute, recState: enabled, recReady: ConditionStatusTrue, recGen: 1, targetState: enabled, targetReady: ConditionStatusTrue, targetGen: 1, wantSync: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := base
			tracker := cmsSyncTracker{now: func() time.Time { return now }}
			if tt.recorded {
				tracker.recordSync("xmc/comp", tt.recState, tt.recReady, tt.recGen)
				now = base.Add(tt.age)
			}
			if got := tracker.shouldSync("xmc/comp", tt.targetState, tt.targetReady, tt.targetGen); got != tt.wantSync {
				t.Errorf("shouldSync() = %v, want %v", got, tt.wantSync)
			}
		})
	}

	t.Run("forget clears recorded sync state", func(t *testing.T) {
		tracker := cmsSyncTracker{}
		tracker.recordSync("xmc/comp", enabled, ConditionStatusTrue, 1)
		tracker.forget("xmc/comp")
		if !tracker.shouldSync("xmc/comp", enabled, ConditionStatusTrue, 1) {
			t.Error("shouldSync() after forget = false, want true")
		}
	})
}

func TestSyncReadyToCMS_PreservesReadyTransitionTimeAcrossHeartbeats(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	initialLTT := time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)
	cms := &mockCMS{}
	r, err := NewReconciler(cms, nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	r.syncTracker.now = func() time.Time { return now }

	in := ReconcileComponentInput{
		Namespace:  "xmc",
		Name:       "clickhouse",
		Generation: 1,
		Enabled:    true,
		Spec:       &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER},
		ExistingConditions: []Condition{
			{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy, LastTransitionTime: initialLTT},
		},
	}

	// Pass 1: initial sync for this Reconciler sends UpdateObservedGeneration without mutating Ready.LastTransitionTime.
	res1, err := r.ReconcileComponent(context.Background(), in)
	if err != nil {
		t.Fatalf("pass 1: ReconcileComponent() error = %v", err)
	}
	if cms.lastObsReq == nil {
		t.Fatal("pass 1: expected UpdateObservedGeneration call")
	}
	if ready := findCondition(res1.Conditions, ConditionTypeReady); ready == nil || !ready.LastTransitionTime.Equal(initialLTT) {
		t.Errorf("pass 1: Ready.LastTransitionTime = %v, want %v", ready, initialLTT)
	}

	// Pass 2: within heartbeat window skips UpdateObservedGeneration and preserves Ready.LastTransitionTime.
	cms.lastObsReq = nil
	in.ExistingConditions = res1.Conditions
	now = now.Add(time.Minute)
	res2, err := r.ReconcileComponent(context.Background(), in)
	if err != nil {
		t.Fatalf("pass 2: ReconcileComponent() error = %v", err)
	}
	if cms.lastObsReq != nil {
		t.Errorf("pass 2: unexpected UpdateObservedGeneration call within heartbeat interval: %v", cms.lastObsReq)
	}
	if ready := findCondition(res2.Conditions, ConditionTypeReady); ready == nil || !ready.LastTransitionTime.Equal(initialLTT) {
		t.Errorf("pass 2: Ready.LastTransitionTime = %v, want %v", ready, initialLTT)
	}

	// Pass 3: after heartbeat expiration sends UpdateObservedGeneration and still preserves Ready.LastTransitionTime.
	in.ExistingConditions = res2.Conditions
	now = now.Add(defaultCMSHeartbeatInterval)
	res3, err := r.ReconcileComponent(context.Background(), in)
	if err != nil {
		t.Fatalf("pass 3: ReconcileComponent() error = %v", err)
	}
	if cms.lastObsReq == nil {
		t.Fatal("pass 3: expected UpdateObservedGeneration call after heartbeat expiration")
	}
	if ready := findCondition(res3.Conditions, ConditionTypeReady); ready == nil || !ready.LastTransitionTime.Equal(initialLTT) {
		t.Errorf("pass 3: Ready.LastTransitionTime = %v, want %v", ready, initialLTT)
	}
}
