// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func buildFakeComponentCR(name, namespace string, enabled bool, gen, obsGen int64, readyStatus, readyReason, degradedStatus string) *unstructured.Unstructured {
	var conds []any
	if readyStatus != "" {
		conds = append(conds, map[string]any{
			"type":    "Ready",
			"status":  readyStatus,
			"reason":  readyReason,
			"message": "ready message",
		})
	}
	if degradedStatus != "" {
		conds = append(conds, map[string]any{
			"type":    "Degraded",
			"status":  degradedStatus,
			"reason":  "DegradedReason",
			"message": "degraded message",
		})
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata": map[string]any{
				"name":       name,
				"namespace":  namespace,
				"generation": gen,
			},
			"spec": map[string]any{
				"enabled": enabled,
			},
			"status": map[string]any{
				"observedGeneration": obsGen,
				"conditions":         conds,
			},
		},
	}
}

func newFakeDynamicClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		ComponentGVR: "ComponentList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objs...)
}

func TestCheckComponents_DisabledFlag(t *testing.T) {
	cfg := &Config{CheckComponents: false, Namespace: "xmc"}
	rep := CheckComponents(context.Background(), nil, cfg)
	if rep.Status != StatusHealthy || !rep.Healthy || !rep.Skipped {
		t.Fatalf("CheckComponents disabled = %+v, want Healthy+Skipped", rep)
	}
}

func TestCheckComponents_ClientUnavailable(t *testing.T) {
	// Implicit check: gracefully skips when cluster is unconfigured
	cfgImplicit := &Config{CheckComponents: true, CheckComponentsExplicit: false, Namespace: "xmc"}
	repImplicit := CheckComponents(context.Background(), &ComponentChecker{ConnErr: errors.New("no kubeconfig")}, cfgImplicit)
	if repImplicit.Status != StatusHealthy || !repImplicit.Healthy || !repImplicit.Skipped {
		t.Fatalf("CheckComponents implicit unavailable = %+v, want Healthy+Skipped", repImplicit)
	}

	// Explicit check: fails when cluster is unconfigured
	cfgExplicit := &Config{CheckComponents: true, CheckComponentsExplicit: true, Namespace: "xmc"}
	repExplicit := CheckComponents(context.Background(), &ComponentChecker{ConnErr: errors.New("no kubeconfig")}, cfgExplicit)
	if repExplicit.Status != StatusFailed || repExplicit.Healthy {
		t.Fatalf("CheckComponents explicit unavailable = %+v, want StatusFailed", repExplicit)
	}
}

func TestCheckComponents_EmptyListFails(t *testing.T) {
	client := newFakeDynamicClient()
	cfg := &Config{CheckComponents: true, Namespace: "xmc"}
	rep := CheckComponents(context.Background(), &ComponentChecker{Client: client}, cfg)
	if rep.Status != StatusFailed || rep.Healthy {
		t.Fatalf("CheckComponents empty list = %+v, want StatusFailed", rep)
	}
}

func TestCheckComponents_AllHealthy(t *testing.T) {
	c1 := buildFakeComponentCR("metrics", "xmc", true, 2, 2, "True", "Reconciled", "False")
	delete(c1.Object["spec"].(map[string]any), "enabled") // omitted spec.enabled defaults to true
	c2 := buildFakeComponentCR("profiling", "xmc", false, 1, 1, "False", "ComponentDisabled", "False")
	c2.Object["status"].(map[string]any)["state"] = "COMPONENT_STATE_DISABLED"
	client := newFakeDynamicClient(c1, c2)

	cfg := &Config{CheckComponents: true, Namespace: "xmc"}
	rep := CheckComponents(context.Background(), &ComponentChecker{Client: client}, cfg)
	if rep.Status != StatusHealthy || !rep.Healthy {
		t.Fatalf("CheckComponents all healthy = %+v, want StatusHealthy", rep)
	}
	if len(rep.Components) != 2 {
		t.Fatalf("len(Components) = %d, want 2", len(rep.Components))
	}
	if rep.Components[0].Name != "metrics" || !rep.Components[0].Enabled || rep.Components[0].State != "STATE_READY" {
		t.Errorf("Component[0] = %+v, want metrics Enabled=true STATE_READY", rep.Components[0])
	}
	if rep.Components[1].Name != "profiling" || rep.Components[1].State != "STATE_DISABLED" {
		t.Errorf("Component[1] = %+v, want profiling STATE_DISABLED", rep.Components[1])
	}
}

func TestCheckComponents_UnhealthyStates(t *testing.T) {
	tests := []struct {
		name      string
		cr        *unstructured.Unstructured
		wantState string
	}{
		{
			name:      "degraded component",
			cr:        buildFakeComponentCR("metrics", "xmc", true, 1, 1, "False", "InstallFailed", "True"),
			wantState: "STATE_DEGRADED",
		},
		{
			name:      "stale observedGeneration",
			cr:        buildFakeComponentCR("metrics", "xmc", true, 2, 1, "True", "Reconciled", "False"),
			wantState: "STATE_RECONCILING",
		},
		{
			name:      "enabled reconciling",
			cr:        buildFakeComponentCR("metrics", "xmc", true, 1, 1, "False", "Reconciling", "False"),
			wantState: "STATE_RECONCILING",
		},
		{
			name:      "disabled but still ready",
			cr:        buildFakeComponentCR("profiling", "xmc", false, 2, 2, "True", "Reconciled", "False"),
			wantState: "STATE_READY",
		},
		{
			name:      "disabled uninstalling",
			cr:        buildFakeComponentCR("profiling", "xmc", false, 2, 2, "False", "Uninstalling", "False"),
			wantState: "STATE_RECONCILING",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newFakeDynamicClient(tc.cr)
			cfg := &Config{CheckComponents: true, Namespace: "xmc"}
			rep := CheckComponents(context.Background(), &ComponentChecker{Client: client}, cfg)
			if rep.Status != StatusFailed || rep.Healthy {
				t.Fatalf("CheckComponents(%s) = %+v, want StatusFailed", tc.name, rep)
			}
			if len(rep.Components) != 1 || rep.Components[0].State != tc.wantState {
				t.Errorf("Component state = %+v, want %q", rep.Components, tc.wantState)
			}
		})
	}
}
