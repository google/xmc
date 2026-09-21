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

// Package graph provides pure 3-color DFS cycle detection and DAG traversal operations.
package graph

import (
	"sort"
	"strings"
)

const (
	colorWhite = 0 // unvisited
	colorGray  = 1 // visiting / in current recursion stack
	colorBlack = 2 // visited / completely processed
)

// DependencyGraph represents an adjacency mapping of node names to their dependencies or dependents.
type DependencyGraph map[string][]string

// CycleFinding represents a detected cycle and the edge source node that closed the cycle.
type CycleFinding struct {
	SourceNode string
	CyclePath  []string
}

// DetectCycles performs a deterministic 3-color DFS to detect cycles in a directed dependency graph.
// Returns the first cycle path (e.g. ["a", "b", "a"]) and true if a cycle is found, or nil and false otherwise.
func DetectCycles(graph DependencyGraph) ([]string, bool) {
	sortedGraph := make(DependencyGraph, len(graph))
	for k, deps := range graph {
		sortedGraph[k] = getSortedNeighbors(deps)
	}
	findings := detectCyclesInternal(collectAllNodes(graph), sortedGraph, nil, true)
	if len(findings) == 0 {
		return nil, false
	}
	return findings[0].CyclePath, true
}

// DetectAllCycles performs a 3-color DFS across nodes and returns all unique detected cycles.
// If isValidTarget is non-nil, edges pointing to targets where isValidTarget(v) is false are skipped.
func DetectAllCycles(nodes []string, graph DependencyGraph, isValidTarget func(string) bool) []CycleFinding {
	return detectCyclesInternal(nodes, graph, isValidTarget, false)
}

type cycleDetector struct {
	graph         DependencyGraph
	isValidTarget func(string) bool
	stopOnFirst   bool
	colors        map[string]int
	path          []string
	reported      map[string]bool
	findings      []CycleFinding
}

func detectCyclesInternal(nodes []string, graph DependencyGraph, isValidTarget func(string) bool, stopOnFirst bool) []CycleFinding {
	detector := &cycleDetector{
		graph:         graph,
		isValidTarget: isValidTarget,
		stopOnFirst:   stopOnFirst,
		colors:        make(map[string]int, len(nodes)),
		path:          make([]string, 0, len(nodes)),
		reported:      make(map[string]bool),
	}

	for _, node := range nodes {
		if detector.colors[node] == colorWhite {
			detector.dfsVisit(node)
			if detector.stopOnFirst && len(detector.findings) > 0 {
				return detector.findings
			}
		}
	}
	return detector.findings
}

func (d *cycleDetector) dfsVisit(u string) {
	d.colors[u] = colorGray
	d.path = append(d.path, u)

	for _, v := range d.graph[u] {
		if d.isValidTarget != nil && !d.isValidTarget(v) {
			continue
		}
		if d.colors[v] == colorGray {
			d.recordCycleFinding(u, v)
			if d.stopOnFirst {
				return
			}
			continue
		}
		if d.colors[v] == colorWhite {
			d.dfsVisit(v)
			if d.stopOnFirst && len(d.findings) > 0 {
				return
			}
		}
	}

	d.path = d.path[:len(d.path)-1]
	d.colors[u] = colorBlack
}

func (d *cycleDetector) recordCycleFinding(u, v string) {
	cyclePath := extractCyclePath(d.path, v)
	cycleStr := FormatCycle(cyclePath)
	if cycleStr == "" || d.reported[cycleStr] {
		return
	}
	d.reported[cycleStr] = true
	d.findings = append(d.findings, CycleFinding{SourceNode: u, CyclePath: cyclePath})
}

func extractCyclePath(path []string, closingNode string) []string {
	startIdx := -1
	for i, node := range path {
		if node == closingNode {
			startIdx = i
			break
		}
	}
	if startIdx == -1 {
		return nil
	}
	cycle := make([]string, 0, len(path)-startIdx+1)
	cycle = append(cycle, path[startIdx:]...)
	cycle = append(cycle, closingNode)
	return cycle
}

func collectAllNodes(graph DependencyGraph) []string {
	nodeSet := make(map[string]bool, len(graph))
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

func getSortedNeighbors(neighbors []string) []string {
	if len(neighbors) == 0 {
		return nil
	}
	cp := make([]string, len(neighbors))
	copy(cp, neighbors)
	sort.Strings(cp)
	return cp
}

// FormatCycle converts a cycle slice into a readable arrow-separated string (e.g. "a -> b -> a").
func FormatCycle(cycle []string) string {
	return strings.Join(cycle, " -> ")
}

// FindTransitiveDependents returns all nodes that transitively depend on root in dependentsGraph.
// The result is deduplicated, sorted alphabetically, and excludes root itself.
func FindTransitiveDependents(dependentsGraph DependencyGraph, root string) []string {
	if root == "" {
		return nil
	}
	visited := map[string]bool{root: true}
	queue := []string{root}
	var dependents []string

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, dep := range dependentsGraph[curr] {
			if visited[dep] {
				continue
			}
			visited[dep] = true
			dependents = append(dependents, dep)
			queue = append(queue, dep)
		}
	}

	sort.Strings(dependents)
	return dependents
}
