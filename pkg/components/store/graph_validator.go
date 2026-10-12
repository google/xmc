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

package store

import (
	"context"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/google/xmc/internal/logging"
	"github.com/google/xmc/pkg/components/graph"
	"github.com/google/xmc/pkg/components/naming"
)

// DependencyNotFoundMessagePrefix starts the FailedPrecondition message returned when a component
// declares a dependency that is not registered. The GitOps bridge matches codes.FailedPrecondition
// plus this prefix to report "waiting on dependencies" instead of an error, so keep it stable.
const DependencyNotFoundMessagePrefix = "dependency not found:"

// DependencyGraph represents an adjacency mapping of component names to their dependencies or dependents.
type DependencyGraph = graph.DependencyGraph

// BuildDependencyGraph constructs an adjacency map of component -> list of its dependencies.
// Soft-deleted and deleted components are excluded from the graph.
func BuildDependencyGraph(records []*ComponentRecord) DependencyGraph {
	depGraph := make(DependencyGraph, len(records))
	for _, rec := range records {
		if rec == nil || isDeletedRecord(rec) {
			continue
		}
		canonical := naming.CanonicalComponentName(rec.Name)
		if canonical == "" {
			continue
		}
		deps := make([]string, len(rec.Dependencies))
		copy(deps, rec.Dependencies)
		depGraph[canonical] = deps
	}
	return depGraph
}

// BuildDependencyGraphFromMap constructs an adjacency map of component -> list of its dependencies directly from a component record map.
// Soft-deleted and deleted components are excluded from the graph.
func BuildDependencyGraphFromMap(records map[string]*ComponentRecord) DependencyGraph {
	depGraph := make(DependencyGraph, len(records))
	for _, rec := range records {
		if rec == nil || isDeletedRecord(rec) {
			continue
		}
		canonical := naming.CanonicalComponentName(rec.Name)
		if canonical == "" {
			continue
		}
		deps := make([]string, len(rec.Dependencies))
		copy(deps, rec.Dependencies)
		depGraph[canonical] = deps
	}
	return depGraph
}

// BuildDependentsGraph constructs an inverted adjacency map of component -> list of its dependents.
// Soft-deleted and deleted components are excluded from the graph.
func BuildDependentsGraph(records []*ComponentRecord) DependencyGraph {
	dependents := make(DependencyGraph)
	for _, rec := range records {
		if rec == nil || isDeletedRecord(rec) {
			continue
		}
		canonical := naming.CanonicalComponentName(rec.Name)
		if canonical == "" {
			continue
		}
		if _, ok := dependents[canonical]; !ok {
			dependents[canonical] = nil
		}
		for _, dep := range rec.Dependencies {
			depCanonical := naming.CanonicalComponentName(dep)
			if depCanonical != "" {
				dependents[depCanonical] = append(dependents[depCanonical], canonical)
			}
		}
	}
	for k := range dependents {
		sort.Strings(dependents[k])
	}
	return dependents
}

// DetectCycles performs a 3-color DFS to detect cycles in a directed dependency graph.
// Returns the cycle path (e.g. ["a", "b", "a"]) and true if a cycle is found, or nil and false otherwise.
func DetectCycles(depGraph DependencyGraph) ([]string, bool) {
	return graph.DetectCycles(depGraph)
}

// ValidateProspectiveDependencies validates that adding or updating dependencies on targetName
// will not introduce self-cycles or transitive cycles into the component dependency graph.
func ValidateProspectiveDependencies(existing []*ComponentRecord, targetName string, newDeps []string) error {
	canonicalTarget := naming.CanonicalComponentName(targetName)
	if canonicalTarget == "" {
		return status.Error(codes.InvalidArgument, "target component name is required")
	}

	normalized := NormalizeDependencies(newDeps)
	depGraph := BuildDependencyGraph(existing)
	return validateProspectiveGraph(depGraph, canonicalTarget, normalized)
}

// ValidateProspectiveDependenciesFromMap validates prospective dependencies directly from a component record map
// avoiding intermediate slice allocations.
func ValidateProspectiveDependenciesFromMap(existing map[string]*ComponentRecord, targetName string, newDeps []string) error {
	canonicalTarget := naming.CanonicalComponentName(targetName)
	if canonicalTarget == "" {
		return status.Error(codes.InvalidArgument, "target component name is required")
	}

	normalized := NormalizeDependencies(newDeps)
	depGraph := BuildDependencyGraphFromMap(existing)
	return validateProspectiveGraph(depGraph, canonicalTarget, normalized)
}

func validateProspectiveGraph(depGraph DependencyGraph, canonicalTarget string, normalized []string) error {
	for _, dep := range normalized {
		if dep == canonicalTarget {
			cycleStr := canonicalTarget + " -> " + canonicalTarget
			logging.Warn(context.Background(), "Prospective dependency self-cycle detected", "target", canonicalTarget, "cycle", cycleStr)
			return status.Errorf(codes.FailedPrecondition, "dependency cycle detected: %s", cycleStr)
		}
	}

	depGraph[canonicalTarget] = normalized

	if cycle, found := DetectCycles(depGraph); found {
		cycleStr := graph.FormatCycle(cycle)
		logging.Warn(context.Background(), "Prospective dependency cycle detected", "target", canonicalTarget, "cycle", cycleStr)
		return status.Errorf(codes.FailedPrecondition, "dependency cycle detected: %s", cycleStr)
	}
	return nil
}

// resolveDependencyIDs maps each dependency name to the ComponentID of its registered, non-deleted record.
// It returns a FailedPrecondition error naming the first dependency that is not registered.
func resolveDependencyIDs(catalog map[string]*ComponentRecord, canonicalName string, deps []string) (map[string]int64, error) {
	depIDs := make(map[string]int64, len(deps))
	for _, dep := range deps {
		depRec, ok := catalog[dep]
		if !ok || depRec == nil || isDeletedRecord(depRec) {
			return nil, status.Errorf(codes.FailedPrecondition, DependencyNotFoundMessagePrefix+" component %q depends on %q, which is not registered", canonicalName, dep)
		}
		depIDs[dep] = depRec.ComponentID
	}
	return depIDs, nil
}

// FindTransitiveDependents returns all components that transitively depend on root.
// The result is deduplicated, sorted, and excludes the root itself.
func FindTransitiveDependents(dependentsGraph DependencyGraph, root string) []string {
	canonicalRoot := naming.CanonicalComponentName(root)
	if canonicalRoot == "" {
		return nil
	}
	return graph.FindTransitiveDependents(dependentsGraph, canonicalRoot)
}
