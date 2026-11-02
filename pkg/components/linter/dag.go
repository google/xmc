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

	"xmanager_cloud/pkg/components/graph"
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
			checkStabilityInversion(c, depComp, cName, depName, line, res)
		}
	}
}

// checkStabilityInversion enforces the XMC Component Stability Contract (matching runtime CMS
// ValidateIntentStability): STABLE and PREVIEW components must not depend on EXPERIMENTAL
// components, while STABLE components are permitted to depend on PREVIEW components.
func checkStabilityInversion(c, depComp *ComponentManifest, cName, depName string, line int, res *ValidationResult) {
	if c == nil || depComp == nil || depComp.EffectiveStability() != "EXPERIMENTAL" {
		return
	}
	cStability := c.EffectiveStability()
	if cStability == "STABLE" || cStability == "PREVIEW" {
		res.AddError(c.FilePath, line, "STABILITY_INVERSION", fmt.Sprintf("stability inversion: %s component %q cannot depend on EXPERIMENTAL component %q", cStability, cName, depName))
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

func buildDependencyGraph(compMap map[string]*ComponentManifest) graph.DependencyGraph {
	adj := make(graph.DependencyGraph, len(compMap))
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

func findDependencyCycles(nodes []string, adj graph.DependencyGraph, compMap map[string]*ComponentManifest, res *ValidationResult) {
	findings := graph.DetectAllCycles(nodes, adj, func(target string) bool {
		_, exists := compMap[target]
		return exists
	})
	for _, f := range findings {
		filePath := ""
		line := 1
		if comp := compMap[f.SourceNode]; comp != nil {
			filePath = comp.FilePath
			if comp.Line > 0 {
				line = comp.Line
			}
		}
		res.AddError(filePath, line, "CYCLIC_DEPENDENCY", fmt.Sprintf("dependency cycle detected: %s", graph.FormatCycle(f.CyclePath)))
	}
}
