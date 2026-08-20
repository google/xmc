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

	"xmanager_cloud/pkg/components/store"
)

// runPass2 validates DAG construction, enabled-state completeness, and dependency cycles.
func (l *Linter) runPass2(compManifests []*ComponentManifest, compMap map[string]*ComponentManifest, res *ValidationResult) {
	for _, c := range compManifests {
		cName := store.CanonicalComponentName(c.Metadata.Name)
		cEnabled := c.IsEnabled()

		for _, dep := range c.Spec.Dependencies {
			depName := store.CanonicalComponentName(dep.Name)
			if depName == "" {
				continue
			}
			depComp, exists := compMap[depName]
			if !exists {
				if !dep.Optional {
					res.AddError(c.FilePath, 1, "MISSING_DEPENDENCY", fmt.Sprintf("component %q has missing dependency %q", cName, depName))
				}
				continue
			}
			if cEnabled && !depComp.IsEnabled() && !dep.Optional {
				res.AddError(c.FilePath, 1, "DISABLED_DEPENDENCY", fmt.Sprintf("enabled component %q depends on disabled component %q", cName, depName))
			}
		}
	}

	l.detectCycles(compMap, res)
}

func (l *Linter) detectCycles(compMap map[string]*ComponentManifest, res *ValidationResult) {
	adj := make(map[string][]string)
	for name, comp := range compMap {
		var deps []string
		for _, dep := range comp.Spec.Dependencies {
			dName := store.CanonicalComponentName(dep.Name)
			if dName != "" {
				deps = append(deps, dName)
			}
		}
		adj[name] = deps
	}

	var nodes []string
	for k := range compMap {
		nodes = append(nodes, k)
	}
	sort.Strings(nodes)

	findDependencyCycles(nodes, adj, compMap, res)
}

func findDependencyCycles(nodes []string, adj map[string][]string, compMap map[string]*ComponentManifest, res *ValidationResult) {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)

	state := make(map[string]int)
	path := []string{}
	reported := make(map[string]bool)

	var dfs func(u string)
	dfs = func(u string) {
		state[u] = visiting
		path = append(path, u)

		for _, v := range adj[u] {
			if _, inMap := compMap[v]; !inMap {
				continue
			}
			if state[v] == visiting {
				handleVisitingNode(u, v, path, compMap, reported, res)
			} else if state[v] == unvisited {
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
	cycleStart := -1
	for i, node := range path {
		if node == v {
			cycleStart = i
			break
		}
	}
	if cycleStart == -1 {
		return
	}
	cycleNodes := append([]string{}, path[cycleStart:]...)
	cycleNodes = append(cycleNodes, v)
	cycleStr := strings.Join(cycleNodes, " -> ")
	if reported[cycleStr] {
		return
	}
	reported[cycleStr] = true
	filePath := ""
	if comp := compMap[u]; comp != nil {
		filePath = comp.FilePath
	}
	res.AddError(filePath, 1, "CYCLIC_DEPENDENCY", fmt.Sprintf("dependency cycle detected: %s", cycleStr))
}
