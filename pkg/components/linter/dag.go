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

package linter

import (
	"fmt"
	"sort"
	"strings"

	"xmanager_cloud/pkg/components/naming"
)

// validateDAG validates DAG construction, enabled-state completeness, and dependency cycles.
func (l *Linter) validateDAG(compManifests []*ComponentManifest, compMap map[string]*ComponentManifest, res *ValidationResult) {
	checkDependencyCompleteness(compManifests, compMap, res)
	detectCycles(compMap, res)
}

func checkDependencyCompleteness(compManifests []*ComponentManifest, compMap map[string]*ComponentManifest, res *ValidationResult) {
	for _, c := range compManifests {
		cName := naming.CanonicalComponentName(c.Metadata.Name)
		if cName == "" {
			continue
		}
		cEnabled := c.IsEnabled()
		line := c.Line
		if line <= 0 {
			line = 1
		}

		for _, dep := range c.Spec.Dependencies {
			depName := naming.CanonicalComponentName(dep.Name)
			if depName == "" {
				continue
			}
			depComp, exists := compMap[depName]
			if !exists {
				if cEnabled && !dep.Optional {
					res.AddError(c.FilePath, line, "MISSING_DEPENDENCY", fmt.Sprintf("component %q has missing dependency %q", cName, depName))
				}
				continue
			}
			if cEnabled && !depComp.IsEnabled() && !dep.Optional {
				res.AddError(c.FilePath, line, "DISABLED_DEPENDENCY", fmt.Sprintf("enabled component %q depends on disabled component %q", cName, depName))
			}
		}
	}
}

func detectCycles(compMap map[string]*ComponentManifest, res *ValidationResult) {
	adj := buildDependencyGraph(compMap)
	var nodes []string
	for k := range compMap {
		nodes = append(nodes, k)
	}
	sort.Strings(nodes)
	findDependencyCycles(nodes, adj, compMap, res)
}

func buildDependencyGraph(compMap map[string]*ComponentManifest) map[string][]string {
	adj := make(map[string][]string, len(compMap))
	for name, comp := range compMap {
		numDeps := len(comp.Spec.Dependencies)
		if numDeps == 0 {
			continue
		}
		deps := make([]string, 0, numDeps)
		seen := make(map[string]bool, numDeps)
		for _, dep := range comp.Spec.Dependencies {
			dName := naming.CanonicalComponentName(dep.Name)
			if dName != "" && !seen[dName] {
				seen[dName] = true
				deps = append(deps, dName)
			}
		}
		adj[name] = deps
	}
	return adj
}

func findDependencyCycles(nodes []string, adj map[string][]string, compMap map[string]*ComponentManifest, res *ValidationResult) {
	const (
		unvisited = iota
		visiting
		visited
	)

	state := make(map[string]int, len(nodes))
	path := make([]string, 0, len(nodes))
	reported := make(map[string]bool)

	var dfs func(u string)
	dfs = func(u string) {
		state[u] = visiting
		path = append(path, u)

		for _, v := range adj[u] {
			if _, inMap := compMap[v]; !inMap {
				continue
			}
			switch state[v] {
			case visiting:
				handleVisitingNode(u, v, path, compMap, reported, res)
			case unvisited:
				dfs(v)
			}
		}

		path = path[:len(path)-1]
		state[u] = visited
	}

	for _, node := range nodes {
		if state[node] == unvisited {
			dfs(node)
		}
	}
}

func handleVisitingNode(u, v string, path []string, compMap map[string]*ComponentManifest, reported map[string]bool, res *ValidationResult) {
	cycleStr := formatCyclePath(path, v)
	if cycleStr == "" || reported[cycleStr] {
		return
	}
	reported[cycleStr] = true
	filePath := ""
	line := 1
	if comp := compMap[u]; comp != nil {
		filePath = comp.FilePath
		if comp.Line > 0 {
			line = comp.Line
		}
	}
	res.AddError(filePath, line, "CYCLIC_DEPENDENCY", fmt.Sprintf("dependency cycle detected: %s", cycleStr))
}

func formatCyclePath(path []string, v string) string {
	cycleStart := -1
	for i, node := range path {
		if node == v {
			cycleStart = i
			break
		}
	}
	if cycleStart == -1 {
		return ""
	}
	cycleNodes := make([]string, 0, len(path)-cycleStart+1)
	cycleNodes = append(cycleNodes, path[cycleStart:]...)
	cycleNodes = append(cycleNodes, v)
	return strings.Join(cycleNodes, " -> ")
}
