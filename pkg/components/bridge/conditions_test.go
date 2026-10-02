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

func TestHealthConditionStatuses(t *testing.T) {
	tests := []struct {
		healthy      bool
		reason       string
		wantReady    string
		wantDegraded string
	}{
		{healthy: true, reason: ReasonComponentHealthy, wantReady: ConditionStatusTrue, wantDegraded: ConditionStatusFalse},
		{reason: ReasonHelmUpgradePending, wantReady: ConditionStatusFalse, wantDegraded: ConditionStatusFalse},
		{reason: ReasonHelmStatusUnavailable, wantReady: ConditionStatusFalse, wantDegraded: ConditionStatusFalse},
		{reason: ReasonHelmReleaseStuck, wantReady: ConditionStatusFalse, wantDegraded: ConditionStatusTrue},
		{reason: ReasonHelmReleaseNotReady, wantReady: ConditionStatusFalse, wantDegraded: ConditionStatusTrue},
	}
	for _, tt := range tests {
		ready, degraded := healthConditionStatuses(tt.healthy, tt.reason)
		if ready != tt.wantReady || degraded != tt.wantDegraded {
			t.Errorf("healthConditionStatuses(%v, %q) = (%s, %s), want (%s, %s)", tt.healthy, tt.reason, ready, degraded, tt.wantReady, tt.wantDegraded)
		}
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
	setCondition(&conds, existing, ConditionTypeDegraded, ConditionStatusTrue, 2, "D", "DegradedMsg")
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
		{Type: ConditionTypeDegraded, Status: ConditionStatusFalse, LastTransitionTime: past},
		{Type: ConditionTypeReady, Status: ConditionStatusTrue, LastTransitionTime: past},
	}
	resetReadyTransitionTime(conditions)
	if time.Since(conditions[1].LastTransitionTime) > 5*time.Second {
		t.Errorf("expected Ready LastTransitionTime to be reset to now, got %v", conditions[1].LastTransitionTime)
	}
	if !conditions[0].LastTransitionTime.Equal(past) {
		t.Errorf("expected Degraded LastTransitionTime to remain unchanged, got %v", conditions[0].LastTransitionTime)
	}
}
