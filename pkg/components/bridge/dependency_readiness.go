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
	"maps"
	"slices"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"xmanager_cloud/pkg/components/graph"
	"xmanager_cloud/pkg/components/naming"
)

// DependencyReadiness reports whether a dependency Component can be depended upon.
type DependencyReadiness interface {
	// IsDependencyReady returns true if the canonically named Component is ready,
	// otherwise false and a short human-readable reason (e.g. "disabled", "not found").
	IsDependencyReady(ctx context.Context, name string) (bool, string)
	// DependencyCycle returns the dependency cycle path (e.g. "a -> b -> a") containing the
	// canonically named Component, or "" if it is not part of a detected cycle.
	DependencyCycle(ctx context.Context, name string) string
}

// DependencySnapshot is an immutable, point-in-time DependencyReadiness view built from a
// single Component list. Component names are global in CMS, so entries are keyed by
// canonical name only. The bridge lists one namespace, so a dependency on a Component outside
// it resolves as "not found".
type DependencySnapshot struct {
	// unreadyReasons maps canonical Component names to "" when ready, or to the unready reason.
	unreadyReasons map[string]string
	// cycles maps canonical Component names to the formatted cycle they belong to.
	cycles map[string]string
}

var _ DependencyReadiness = (*DependencySnapshot)(nil)

// NewDependencySnapshot evaluates the readiness of every Component in items and detects multi-node
// spec.dependencies cycles among them (self-dependencies are ignored, as in ReconcileComponent).
func NewDependencySnapshot(items []unstructured.Unstructured) *DependencySnapshot {
	reasons := make(map[string]string, len(items))
	depGraph := make(graph.DependencyGraph, len(items))
	for i := range items {
		name := naming.CanonicalComponentName(items[i].GetName())
		spec, _, enabled := ParseSpecFromUnstructured(items[i].Object["spec"])
		reasons[name] = componentUnreadyReason(&items[i], enabled)
		depGraph[name] = slices.DeleteFunc(spec.GetDependencies(), func(dep string) bool { return dep == name })
	}
	return &DependencySnapshot{unreadyReasons: reasons, cycles: findCycleMembers(depGraph)}
}

// findCycleMembers maps every node on a detected cycle to its formatted cycle path. Nodes that only
// depend on a cycle are not members; they simply wait on their unready dependencies.
func findCycleMembers(depGraph graph.DependencyGraph) map[string]string {
	members := make(map[string]string)
	for _, finding := range graph.DetectAllCycles(slices.Sorted(maps.Keys(depGraph)), depGraph, nil) {
		path := graph.FormatCycle(finding.CyclePath)
		for _, name := range finding.CyclePath {
			if _, ok := members[name]; !ok {
				members[name] = path
			}
		}
	}
	return members
}

// IsDependencyReady implements DependencyReadiness.
func (s *DependencySnapshot) IsDependencyReady(_ context.Context, name string) (bool, string) {
	reason, ok := s.unreadyReasons[naming.CanonicalComponentName(name)]
	if !ok {
		return false, "not found"
	}
	return reason == "", reason
}

// DependencyCycle implements DependencyReadiness.
func (s *DependencySnapshot) DependencyCycle(_ context.Context, name string) string {
	return s.cycles[naming.CanonicalComponentName(name)]
}

// componentUnreadyReason returns "" if obj is enabled (as pre-parsed from its spec), not being
// deleted, Ready=True at its current metadata.generation and not Degraded; otherwise it returns
// the first failing reason.
func componentUnreadyReason(obj *unstructured.Unstructured, enabled bool) string {
	if obj.GetDeletionTimestamp() != nil {
		return "being deleted"
	}
	if !enabled {
		return "disabled"
	}
	conds := ParseConditionsFromUnstructured(obj.Object["status"])
	ready := findCondition(conds, ConditionTypeReady)
	if ready == nil || ready.Status != ConditionStatusTrue {
		return "not ready"
	}
	if ready.ObservedGeneration != obj.GetGeneration() {
		return "not ready at current generation"
	}
	if degraded := findCondition(conds, ConditionTypeDegraded); degraded != nil && degraded.Status == ConditionStatusTrue {
		return "degraded"
	}
	return ""
}

// findCondition returns the first condition of condType, or nil if absent.
func findCondition(conds []Condition, condType string) *Condition {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}
