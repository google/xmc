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

	"xmanager_cloud/pkg/components/store"
)

// validateMatrix validates SemVer compatibility matrix constraints and requirements.
func (l *Linter) validateMatrix(compManifests []*ComponentManifest, compMap map[string]*ComponentManifest, res *ValidationResult) {
	validateComponentVersions(compManifests, res)
	validateManifestDependencies(compManifests, compMap, res)
}

// validateComponentVersions parses and validates all declared Component.spec.version strings
// across manifests using store.Parse, recording INVALID_VERSION diagnostics for malformed strings.
func validateComponentVersions(compManifests []*ComponentManifest, res *ValidationResult) {
	for _, c := range compManifests {
		if c == nil || c.Spec.Version == "" {
			continue
		}
		line := max(c.Line, 1)
		if _, err := store.Parse(c.Spec.Version); err != nil {
			res.AddError(c.FilePath, line, "INVALID_VERSION", fmt.Sprintf("invalid semver version %q for component %q: %v", c.Spec.Version, c.Metadata.Name, err))
		}
	}
}

// validateManifestDependencies iterates over all enabled components and validates that each declared
// dependency in spec.dependencies satisfies version constraints against the target component in the manifest set.
func validateManifestDependencies(compManifests []*ComponentManifest, compMap map[string]*ComponentManifest, res *ValidationResult) {
	for _, c := range compManifests {
		if c == nil || !c.IsEnabled() {
			continue
		}
		line := max(c.Line, 1)
		cName := store.CanonicalComponentName(c.Metadata.Name)
		for _, dep := range c.Spec.Dependencies {
			validateSingleDependency(c, line, cName, dep, compMap, res)
		}
	}
}

// validateSingleDependency evaluates a single manifest dependency constraint against the resolved
// target dependency component, recording INVALID_VERSION_CONSTRAINT or VERSION_INCOMPATIBILITY
// diagnostics while safely skipping unversioned or disabled target dependencies.
func validateSingleDependency(c *ComponentManifest, line int, cName string, dep ComponentDependencyRef, compMap map[string]*ComponentManifest, res *ValidationResult) {
	if dep.Version == "" {
		return
	}
	depName := store.CanonicalComponentName(dep.Name)
	depComp, exists := compMap[depName]
	if !exists || depComp == nil || !depComp.IsEnabled() || depComp.Spec.Version == "" {
		return
	}
	if _, err := store.Parse(depComp.Spec.Version); err != nil {
		return
	}
	satisfied, err := MatchConstraint(depComp.Spec.Version, dep.Version)
	if err != nil {
		res.AddError(c.FilePath, line, "INVALID_VERSION_CONSTRAINT", fmt.Sprintf("invalid version constraint %q for dependency %q of component %q: %v", dep.Version, depName, cName, err))
		return
	}
	if !satisfied {
		res.AddError(c.FilePath, line, "VERSION_INCOMPATIBILITY", fmt.Sprintf("component %q (version %s) does not satisfy dependency constraint %q required by %q", depName, depComp.Spec.Version, dep.Version, cName))
	}
}
