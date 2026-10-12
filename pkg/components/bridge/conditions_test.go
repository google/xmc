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
	"testing"
	"time"
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
		{name: "helm release not ready", ready: ConditionStatusFalse, reason: ReasonHelmReleaseNotReady, wantReconciling: ReasonProgressing},
		{name: "helm release drifted", ready: ConditionStatusFalse, reason: ReasonHelmReleaseDrifted, wantReconciling: ReasonProgressing},
		{name: "helm status unavailable", ready: ConditionStatusFalse, reason: ReasonHelmStatusUnavailable, wantReconciling: ReasonProgressing},
		{name: "helm release stuck", ready: ConditionStatusFalse, reason: ReasonHelmReleaseStuck, wantStalled: ReasonHelmReleaseStuck},
		{name: "dependency cycle", ready: ConditionStatusFalse, reason: ReasonDependencyCycle, wantStalled: ReasonDependencyCycle},
		{name: "deactivation blocked", ready: ConditionStatusFalse, reason: ReasonDeactivationBlocked, wantStalled: ReasonDeactivationBlocked},
		{name: "contract broken", ready: ConditionStatusFalse, reason: ReasonContractBroken, wantStalled: ReasonContractBroken},
		{name: "invalid wrapper spec", ready: ConditionStatusFalse, reason: ReasonInvalidWrapperSpec, wantStalled: ReasonInvalidWrapperSpec},
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

func TestShouldSyncToCMS(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name         string
		conditions   []Condition
		targetStatus string
		currentGen   int64
		wantSync     bool
	}{
		{
			name:         "initial reconciliation with empty conditions",
			conditions:   nil,
			targetStatus: ConditionStatusTrue,
			currentGen:   1,
			wantSync:     true,
		},
		{
			name: "steady state unchanged health within heartbeat interval",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 1,
					LastTransitionTime: now.Add(-1 * time.Minute),
				},
			},
			targetStatus: ConditionStatusTrue,
			currentGen:   1,
			wantSync:     false,
		},
		{
			name: "readiness state transition from False to True",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusFalse,
					ObservedGeneration: 1,
					LastTransitionTime: now.Add(-1 * time.Minute),
				},
			},
			targetStatus: ConditionStatusTrue,
			currentGen:   1,
			wantSync:     true,
		},
		{
			name: "readiness state transition from True to False",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 1,
					LastTransitionTime: now.Add(-1 * time.Minute),
				},
			},
			targetStatus: ConditionStatusFalse,
			currentGen:   1,
			wantSync:     true,
		},
		{
			name: "metadata generation increment",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 1,
					LastTransitionTime: now.Add(-1 * time.Minute),
				},
			},
			targetStatus: ConditionStatusTrue,
			currentGen:   2,
			wantSync:     true,
		},
		{
			name: "coarse heartbeat expired after 5 minutes",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 1,
					LastTransitionTime: now.Add(-6 * time.Minute),
				},
			},
			targetStatus: ConditionStatusTrue,
			currentGen:   1,
			wantSync:     true,
		},
		{
			name: "zero LastTransitionTime triggers sync",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 1,
					LastTransitionTime: time.Time{},
				},
			},
			targetStatus: ConditionStatusTrue,
			currentGen:   1,
			wantSync:     true,
		},
		{
			name: "Synced=False recovery triggers sync even when Ready is unchanged within heartbeat",
			conditions: []Condition{
				{
					Type:               ConditionTypeReady,
					Status:             ConditionStatusTrue,
					ObservedGeneration: 2,
					LastTransitionTime: now.Add(-5 * time.Second),
				},
				{
					Type:               ConditionTypeSynced,
					Status:             ConditionStatusFalse,
					ObservedGeneration: 2,
					Reason:             ReasonCMSSyncFailed,
				},
			},
			targetStatus: ConditionStatusTrue,
			currentGen:   2,
			wantSync:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSyncToCMS(tt.conditions, tt.targetStatus, tt.currentGen)
			if got != tt.wantSync {
				t.Errorf("shouldSyncToCMS() = %v, want %v", got, tt.wantSync)
			}
		})
	}
}

func TestResetReadyTransitionTime(t *testing.T) {
	past := time.Now().UTC().Add(-10 * time.Minute)
	conditions := []Condition{
		{Type: ConditionTypeSynced, Status: ConditionStatusTrue, LastTransitionTime: past},
		{Type: ConditionTypeReady, Status: ConditionStatusTrue, LastTransitionTime: past},
	}
	resetReadyTransitionTime(conditions)
	if time.Since(conditions[1].LastTransitionTime) > 5*time.Second {
		t.Errorf("expected Ready LastTransitionTime to be reset to now, got %v", conditions[1].LastTransitionTime)
	}
	if !conditions[0].LastTransitionTime.Equal(past) {
		t.Errorf("expected Synced LastTransitionTime to remain unchanged, got %v", conditions[0].LastTransitionTime)
	}
}
