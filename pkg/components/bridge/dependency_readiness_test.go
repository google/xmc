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

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// readyComponent returns a baseline enabled Component at generation 2 with Ready=True and Degraded=False
// at that generation. mutate applies a targeted delta.
func readyComponent(name string, mutate func(u *unstructured.Unstructured)) unstructured.Unstructured {
	u := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": name, "namespace": "xmc", "generation": int64(2)},
		"spec":     map[string]any{"enabled": true},
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": ConditionTypeReady, "status": ConditionStatusTrue, "observedGeneration": int64(2)},
			map[string]any{"type": ConditionTypeDegraded, "status": ConditionStatusFalse, "observedGeneration": int64(2)},
		}},
	}}
	if mutate != nil {
		mutate(&u)
	}
	return u
}

func setConditionStatus(u *unstructured.Unstructured, index int, key string, value any) {
	u.Object["status"].(map[string]any)["conditions"].([]any)[index].(map[string]any)[key] = value
}

// withDependencies returns a readyComponent delta that sets spec.dependencies.
func withDependencies(deps ...string) func(u *unstructured.Unstructured) {
	return func(u *unstructured.Unstructured) {
		raw := make([]any, 0, len(deps))
		for _, d := range deps {
			raw = append(raw, d)
		}
		u.Object["spec"].(map[string]any)["dependencies"] = raw
	}
}

func TestDependencySnapshot_DependencyCycle(t *testing.T) {
	tests := []struct {
		name  string
		items []unstructured.Unstructured
		want  map[string]string
	}{
		{
			name:  "acyclic chain",
			items: []unstructured.Unstructured{readyComponent("alpha", withDependencies("beta")), readyComponent("beta", nil)},
			want:  map[string]string{"alpha": "", "beta": ""},
		},
		{
			name:  "two-node cycle",
			items: []unstructured.Unstructured{readyComponent("alpha", withDependencies("beta")), readyComponent("beta", withDependencies("alpha"))},
			want:  map[string]string{"alpha": "alpha -> beta -> alpha", "beta": "alpha -> beta -> alpha"},
		},
		{
			name: "three-node cycle",
			items: []unstructured.Unstructured{
				readyComponent("alpha", withDependencies("beta")),
				readyComponent("beta", withDependencies("gamma")),
				readyComponent("gamma", withDependencies("alpha")),
			},
			want: map[string]string{"alpha": "alpha -> beta -> gamma -> alpha", "beta": "alpha -> beta -> gamma -> alpha", "gamma": "alpha -> beta -> gamma -> alpha"},
		},
		{
			name:  "self-dependency is ignored",
			items: []unstructured.Unstructured{readyComponent("alpha", withDependencies("alpha"))},
			want:  map[string]string{"alpha": ""},
		},
		{
			name: "dependent of a cycle is not a member",
			items: []unstructured.Unstructured{
				readyComponent("alpha", withDependencies("beta")),
				readyComponent("beta", withDependencies("alpha")),
				readyComponent("delta", withDependencies("alpha")),
			},
			want: map[string]string{"alpha": "alpha -> beta -> alpha", "beta": "alpha -> beta -> alpha", "delta": ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := NewDependencySnapshot(tt.items)
			got := make(map[string]string, len(tt.want))
			for name := range tt.want {
				got[name] = snapshot.DependencyCycle(context.Background(), name)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("DependencyCycle() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDependencySnapshot_IsDependencyReady(t *testing.T) {
	type result struct {
		Ready  bool
		Reason string
	}
	tests := []struct {
		name  string
		items []unstructured.Unstructured
		dep   string
		want  result
	}{
		{
			name:  "ready",
			items: []unstructured.Unstructured{readyComponent("redpanda", nil)},
			dep:   "redpanda",
			want:  result{Ready: true},
		},
		{
			name:  "canonicalizes lookup name",
			items: []unstructured.Unstructured{readyComponent("redpanda", nil)},
			dep:   "components/redpanda",
			want:  result{Ready: true},
		},
		{
			name:  "missing",
			items: []unstructured.Unstructured{readyComponent("clickhouse", nil)},
			dep:   "redpanda",
			want:  result{Reason: "not found"},
		},
		{
			name: "disabled",
			items: []unstructured.Unstructured{readyComponent("redpanda", func(u *unstructured.Unstructured) {
				u.Object["spec"] = map[string]any{"enabled": false}
			})},
			dep:  "redpanda",
			want: result{Reason: "disabled"},
		},
		{
			name: "being deleted",
			items: []unstructured.Unstructured{readyComponent("redpanda", func(u *unstructured.Unstructured) {
				now := metav1.Now()
				u.SetDeletionTimestamp(&now)
			})},
			dep:  "redpanda",
			want: result{Reason: "being deleted"},
		},
		{
			name: "no status",
			items: []unstructured.Unstructured{readyComponent("redpanda", func(u *unstructured.Unstructured) {
				delete(u.Object, "status")
			})},
			dep:  "redpanda",
			want: result{Reason: "not ready"},
		},
		{
			name: "ready false",
			items: []unstructured.Unstructured{readyComponent("redpanda", func(u *unstructured.Unstructured) {
				setConditionStatus(u, 0, "status", ConditionStatusFalse)
			})},
			dep:  "redpanda",
			want: result{Reason: "not ready"},
		},
		{
			name: "stale generation",
			items: []unstructured.Unstructured{readyComponent("redpanda", func(u *unstructured.Unstructured) {
				u.SetGeneration(3)
			})},
			dep:  "redpanda",
			want: result{Reason: "not ready at current generation"},
		},
		{
			name: "degraded",
			items: []unstructured.Unstructured{readyComponent("redpanda", func(u *unstructured.Unstructured) {
				setConditionStatus(u, 1, "status", ConditionStatusTrue)
			})},
			dep:  "redpanda",
			want: result{Reason: "degraded"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ready, reason := NewDependencySnapshot(tt.items).IsDependencyReady(context.Background(), tt.dep)
			if diff := cmp.Diff(tt.want, result{Ready: ready, Reason: reason}); diff != "" {
				t.Errorf("IsDependencyReady(%q) mismatch (-want +got):\n%s", tt.dep, diff)
			}
		})
	}
}
