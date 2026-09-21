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

package statussync

import (
	"testing"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
)

func TestBuildStatus(t *testing.T) {
	res := &bridge.ReconcileResult{
		ObservedGeneration: 5,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Access:             &bridge.AccessStatus{EnvVars: map[string]string{"PORT": "8080"}},
		Conditions: []bridge.Condition{
			{
				Type:               bridge.ConditionTypeReady,
				Status:             bridge.ConditionStatusTrue,
				ObservedGeneration: 5,
				Reason:             "ComponentReady",
				Message:            "All good",
				LastTransitionTime: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
			},
		},
	}

	status := BuildStatus(res)
	if status.ObservedGeneration != 5 {
		t.Errorf("expected ObservedGeneration 5, got %v", status.ObservedGeneration)
	}
	if status.State != "COMPONENT_STATE_ENABLED" {
		t.Errorf("expected State COMPONENT_STATE_ENABLED, got %v", status.State)
	}
	if status.Access == nil || status.Access.EnvVars["PORT"] != "8080" {
		t.Errorf("expected Access.EnvVars[PORT]=8080, got %+v", status.Access)
	}
	if len(status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(status.Conditions))
	}
	cond := status.Conditions[0]
	if cond.Type != bridge.ConditionTypeReady || cond.Status != string(bridge.ConditionStatusTrue) || cond.ObservedGeneration != 5 || cond.Reason != "ComponentReady" || cond.Message != "All good" {
		t.Errorf("unexpected condition content: %+v", cond)
	}
}

func TestBuildStatusMap(t *testing.T) {
	res := &bridge.ReconcileResult{
		ObservedGeneration: 5,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Access:             &bridge.AccessStatus{EnvVars: map[string]string{"PORT": "8080"}},
		Conditions: []bridge.Condition{
			{
				Type:               bridge.ConditionTypeReady,
				Status:             bridge.ConditionStatusTrue,
				ObservedGeneration: 5,
				Reason:             "ComponentReady",
				Message:            "All good",
				LastTransitionTime: time.Now(),
			},
		},
	}

	statusMap := BuildStatusMap(res)
	if statusMap["observedGeneration"] != int64(5) {
		t.Errorf("expected observedGeneration 5, got %v", statusMap["observedGeneration"])
	}
	if statusMap["state"] != "COMPONENT_STATE_ENABLED" {
		t.Errorf("expected state COMPONENT_STATE_ENABLED, got %v", statusMap["state"])
	}

	accessMap, ok := statusMap["access"].(map[string]any)
	if !ok {
		t.Fatalf("expected access map in status, got %v", statusMap["access"])
	}
	envVars, ok := accessMap["envVars"].(map[string]any)
	if !ok || envVars["PORT"] != "8080" {
		t.Errorf("expected envVars[PORT]=8080, got %v", accessMap["envVars"])
	}

	conds, ok := statusMap["conditions"].([]any)
	if !ok || len(conds) != 1 {
		t.Fatalf("expected 1 condition in status, got %v", statusMap["conditions"])
	}
	condMap, ok := conds[0].(map[string]any)
	if !ok || condMap["type"] != bridge.ConditionTypeReady || condMap["status"] != string(bridge.ConditionStatusTrue) {
		t.Errorf("unexpected condition content: %+v", condMap)
	}
}
