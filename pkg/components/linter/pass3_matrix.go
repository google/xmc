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

// runPass3 validates SemVer compatibility matrix constraints and requirements.
func (l *Linter) runPass3(compManifests []*ComponentManifest, compMap map[string]*ComponentManifest, res *ValidationResult) {
	for _, c := range compManifests {
		if c.Spec.Version != "" {
			if _, err := store.Parse(c.Spec.Version); err != nil {
				res.AddError(c.FilePath, 1, "INVALID_VERSION", fmt.Sprintf("invalid semver version %q for component %q: %v", c.Spec.Version, c.Metadata.Name, err))
			}
		}
	}

	for _, c := range compManifests {
		cName := store.CanonicalComponentName(c.Metadata.Name)
		for _, dep := range c.Spec.Dependencies {
			if dep.Version == "" {
				continue
			}
			depName := store.CanonicalComponentName(dep.Name)
			depComp, exists := compMap[depName]
			if !exists || depComp.Spec.Version == "" {
				continue
			}
			satisfied, err := MatchConstraint(depComp.Spec.Version, dep.Version)
			if err != nil {
				res.AddError(c.FilePath, 1, "INVALID_VERSION_CONSTRAINT", fmt.Sprintf("invalid version constraint %q for dependency %q of component %q: %v", dep.Version, depName, cName, err))
			} else if !satisfied {
				res.AddError(c.FilePath, 1, "VERSION_INCOMPATIBILITY", fmt.Sprintf("component %q (version %s) does not satisfy dependency constraint %q required by %q", depName, depComp.Spec.Version, dep.Version, cName))
			}
		}
	}

	if l.rules != nil {
		l.evaluateMatrixRules(compMap, res)
	}
}

func (l *Linter) evaluateMatrixRules(compMap map[string]*ComponentManifest, res *ValidationResult) {
	l.evaluateComponentDependencies(compMap, res)
	l.evaluatePlatformRequirements(compMap, res)
}

func (l *Linter) evaluateComponentDependencies(compMap map[string]*ComponentManifest, res *ValidationResult) {
	for _, rule := range l.rules.CompatibilityMatrix.ComponentDependencies {
		ruleCompName := store.CanonicalComponentName(rule.Component)
		comp, exists := compMap[ruleCompName]
		if !exists || !comp.IsEnabled() {
			continue
		}
		if rule.ComponentVersion != "" && comp.Spec.Version != "" {
			match, err := MatchConstraint(comp.Spec.Version, rule.ComponentVersion)
			if err != nil || !match {
				continue
			}
		}
		for _, req := range rule.Requires {
			evaluateDependencyRequirement(comp, ruleCompName, req, compMap, res)
		}
	}
}

func evaluateDependencyRequirement(comp *ComponentManifest, ruleCompName string, req DependencyRequirement, compMap map[string]*ComponentManifest, res *ValidationResult) {
	reqCompName := store.CanonicalComponentName(req.Component)
	reqComp, reqExists := compMap[reqCompName]
	if !reqExists || !reqComp.IsEnabled() {
		if !req.Optional {
			res.AddError(comp.FilePath, 1, "REQUIRED_COMPONENT_MISSING", fmt.Sprintf("component %q requires missing or disabled component %q (%s)", ruleCompName, reqCompName, req.Version))
		}
		return
	}
	if req.Version == "" || reqComp.Spec.Version == "" {
		return
	}
	satisfied, err := MatchConstraint(reqComp.Spec.Version, req.Version)
	if err != nil {
		res.AddError(comp.FilePath, 1, "INVALID_VERSION_CONSTRAINT", fmt.Sprintf("invalid version constraint %q in compatibility rule for %q: %v", req.Version, reqCompName, err))
	} else if !satisfied {
		res.AddError(comp.FilePath, 1, "VERSION_INCOMPATIBILITY", fmt.Sprintf("component %q requires %q version %q, but found version %q", ruleCompName, reqCompName, req.Version, reqComp.Spec.Version))
	}
}

func (l *Linter) evaluatePlatformRequirements(compMap map[string]*ComponentManifest, res *ValidationResult) {
	for _, rule := range l.rules.CompatibilityMatrix.PlatformRequirements {
		ruleCompName := store.CanonicalComponentName(rule.Component)
		comp, exists := compMap[ruleCompName]
		if !exists || !comp.IsEnabled() {
			continue
		}
		if rule.Platform.MinK8sVersion != "" {
			if _, err := store.Parse(rule.Platform.MinK8sVersion); err != nil {
				res.AddError(comp.FilePath, 1, "INVALID_VERSION_CONSTRAINT", fmt.Sprintf("invalid minK8sVersion %q for component %q: %v", rule.Platform.MinK8sVersion, ruleCompName, err))
			}
		}
		if rule.Platform.KubernetesVersion != "" {
			if _, err := MatchConstraint("1.0.0", rule.Platform.KubernetesVersion); err != nil {
				res.AddError(comp.FilePath, 1, "INVALID_VERSION_CONSTRAINT", fmt.Sprintf("invalid kubernetesVersion constraint %q for component %q: %v", rule.Platform.KubernetesVersion, ruleCompName, err))
			}
		}
	}
}
