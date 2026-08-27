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
	"sort"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	colorWhite = 0 // unvisited
	colorGray  = 1 // visiting / in current recursion stack
	colorBlack = 2 // visited / completely processed
)

// TODO(b/549523881): Extract pure 3-color DFS cycle detection and DAG traversal into a shared
// pkg/components/graph package to eliminate code duplication with pkg/components/linter (PR #514).

// DependencyGraph represents an adjacency mapping of component names to their dependencies or dependents.
type DependencyGraph map[string][]string

// BuildDependencyGraph constructs an adjacency map of component -> list of its dependencies.
// Soft-deleted and deleted components are excluded from the graph.
func BuildDependencyGraph(records []*ComponentRecord) DependencyGraph {
	graph := make(DependencyGraph, len(records))
	for _, rec := range records {
		if rec == nil || isDeletedRecord(rec) {
			continue
		}
		canonical := CanonicalComponentName(rec.Name)
		if canonical == "" {
			continue
		}
		deps := make([]string, len(rec.Dependencies))
		copy(deps, rec.Dependencies)
		graph[canonical] = deps
	}
	return graph
}

// BuildDependencyGraphFromMap constructs an adjacency map of component -> list of its dependencies directly from a component record map.
// Soft-deleted and deleted components are excluded from the graph.
func BuildDependencyGraphFromMap(records map[string]*ComponentRecord) DependencyGraph {
	graph := make(DependencyGraph, len(records))
	for _, rec := range records {
		if rec == nil || isDeletedRecord(rec) {
			continue
		}
		canonical := CanonicalComponentName(rec.Name)
		if canonical == "" {
			continue
		}
		deps := make([]string, len(rec.Dependencies))
		copy(deps, rec.Dependencies)
		graph[canonical] = deps
	}
	return graph
}

// BuildDependentsGraph constructs an inverted adjacency map of component -> list of its dependents.
// Soft-deleted and deleted components are excluded from the graph.
func BuildDependentsGraph(records []*ComponentRecord) DependencyGraph {
	dependents := make(DependencyGraph)
	for _, rec := range records {
		if rec == nil || isDeletedRecord(rec) {
			continue
		}
		canonical := CanonicalComponentName(rec.Name)
		if canonical == "" {
			continue
		}
		if _, ok := dependents[canonical]; !ok {
			dependents[canonical] = nil
		}
		for _, dep := range rec.Dependencies {
			depCanonical := CanonicalComponentName(dep)
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
func DetectCycles(graph DependencyGraph) ([]string, bool) {
	nodes := collectAllNodes(graph)
	colors := make(map[string]int)
	var path []string

	for _, node := range nodes {
		if colors[node] == colorWhite {
			if cycle, found := dfsVisit(node, graph, colors, &path); found {
				return cycle, true
			}
		}
	}
	return nil, false
}

// dfsVisit recursively visits nodes tracking current DFS stack for cycle detection.
func dfsVisit(node string, graph DependencyGraph, colors map[string]int, path *[]string) ([]string, bool) {
	colors[node] = colorGray
	*path = append(*path, node)

	neighbors := getSortedNeighbors(graph[node])
	for _, neighbor := range neighbors {
		if colors[neighbor] == colorGray {
			return extractCyclePath(*path, neighbor), true
		}
		if colors[neighbor] == colorWhite {
			if cycle, found := dfsVisit(neighbor, graph, colors, path); found {
				return cycle, true
			}
		}
	}

	colors[node] = colorBlack
	*path = (*path)[:len(*path)-1]
	return nil, false
}

// extractCyclePath slices the recursion stack to isolate the detected cycle path.
func extractCyclePath(path []string, closingNode string) []string {
	startIdx := 0
	for i, node := range path {
		if node == closingNode {
			startIdx = i
			break
		}
	}
	cycle := make([]string, 0, len(path)-startIdx+1)
	cycle = append(cycle, path[startIdx:]...)
	cycle = append(cycle, closingNode)
	return cycle
}

// collectAllNodes gathers and alphabetically sorts all unique nodes in graph keys and edge targets.
func collectAllNodes(graph DependencyGraph) []string {
	nodeSet := make(map[string]bool)
	for k, deps := range graph {
		nodeSet[k] = true
		for _, dep := range deps {
			nodeSet[dep] = true
		}
	}
	nodes := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	return nodes
}

// getSortedNeighbors returns a sorted copy of neighbor nodes for deterministic traversal.
func getSortedNeighbors(neighbors []string) []string {
	if len(neighbors) == 0 {
		return nil
	}
	cp := make([]string, len(neighbors))
	copy(cp, neighbors)
	sort.Strings(cp)
	return cp
}

// formatCycle converts a cycle slice into a readable arrow-separated string.
func formatCycle(cycle []string) string {
	return strings.Join(cycle, " -> ")
}

// ValidateProspectiveDependencies validates that adding or updating dependencies on targetName
// will not introduce self-cycles or transitive cycles into the component dependency graph.
func ValidateProspectiveDependencies(existing []*ComponentRecord, targetName string, newDeps []string) error {
	canonicalTarget := CanonicalComponentName(targetName)
	if canonicalTarget == "" {
		return status.Error(codes.InvalidArgument, "target component name is required")
	}

	normalized := NormalizeDependencies(newDeps)
	graph := BuildDependencyGraph(existing)
	return validateProspectiveGraph(graph, canonicalTarget, normalized)
}

// ValidateProspectiveDependenciesFromMap validates prospective dependencies directly from a component record map
// avoiding intermediate slice allocations.
func ValidateProspectiveDependenciesFromMap(existing map[string]*ComponentRecord, targetName string, newDeps []string) error {
	canonicalTarget := CanonicalComponentName(targetName)
	if canonicalTarget == "" {
		return status.Error(codes.InvalidArgument, "target component name is required")
	}

	normalized := NormalizeDependencies(newDeps)
	graph := BuildDependencyGraphFromMap(existing)
	return validateProspectiveGraph(graph, canonicalTarget, normalized)
}

func validateProspectiveGraph(graph DependencyGraph, canonicalTarget string, normalized []string) error {
	for _, dep := range normalized {
		if dep == canonicalTarget {
			return status.Errorf(codes.FailedPrecondition, "dependency cycle detected: %s -> %s", canonicalTarget, canonicalTarget)
		}
	}

	graph[canonicalTarget] = normalized

	if cycle, found := DetectCycles(graph); found {
		return status.Errorf(codes.FailedPrecondition, "dependency cycle detected: %s", formatCycle(cycle))
	}
	return nil
}

// FindTransitiveDependents returns all components that transitively depend on root.
// The result is deduplicated, sorted, and excludes the root itself.
func FindTransitiveDependents(dependentsGraph DependencyGraph, root string) []string {
	canonicalRoot := CanonicalComponentName(root)
	if canonicalRoot == "" {
		return nil
	}

	visited := make(map[string]bool)
	queue := []string{canonicalRoot}
	visited[canonicalRoot] = true
	var dependents []string

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, dep := range dependentsGraph[curr] {
			if !visited[dep] {
				visited[dep] = true
				dependents = append(dependents, dep)
				queue = append(queue, dep)
			}
		}
	}

	sort.Strings(dependents)
	return dependents
}
