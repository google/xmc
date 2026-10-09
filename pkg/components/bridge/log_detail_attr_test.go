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

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// TestReconcileComponent_LogDetailAttr checks that log lines carrying a free-form message pass it
// as a "detail" attribute: a "message" attribute would duplicate the logger's JSON message key.
func TestReconcileComponent_LogDetailAttr(t *testing.T) {
	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	healthy := []Condition{
		{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
		{Type: ConditionTypeDegraded, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
	}
	tests := []struct {
		name       string
		helm       *mockHelm
		in         ReconcileComponentInput
		logMessage string
	}{
		{
			name: "gated on an unregistered dependency",
			helm: &mockHelm{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, HasFinalizer: true, Enabled: true,
				Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: []string{"redpanda"}},
				Dependencies: NewDependencySnapshot(nil),
			},
			logMessage: "Component reconciliation gated",
		},
		{
			name: "transient Helm status error",
			helm: &mockHelm{unhealthyReason: ReasonHelmStatusUnavailable},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P, HasFinalizer: true, Enabled: true,
				ExistingConditions: healthy,
			},
			logMessage: "Transient component health check failure; keeping previous Ready/Degraded conditions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewReconciler(&mockCMS{}, nil, tt.helm)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			ctx, logs := recordLogs()
			if _, err := r.ReconcileComponent(ctx, tt.in); err != nil {
				t.Fatalf("ReconcileComponent() error = %v", err)
			}
			got := logs.singleRecord(t, tt.logMessage)
			if detail, _ := got.attrs["detail"].(string); detail == "" {
				t.Errorf("%q detail = %v, want a non-empty string", tt.logMessage, got.attrs["detail"])
			}
			if msg, ok := got.attrs["message"]; ok {
				t.Errorf("%q has a \"message\" attribute %v, want none", tt.logMessage, msg)
			}
		})
	}
}
