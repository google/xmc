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
	"strings"

	"github.com/Masterminds/semver/v3"

	"xmanager_cloud/pkg/components/naming"
	componentsemver "xmanager_cloud/pkg/components/semver"
)

// validateMatrix validates SemVer compatibility matrix constraints and requirements.
func (l *Linter) validateMatrix(compManifests []*ComponentManifest, compMap map[string]*ComponentManifest, res *ValidationResult) {
	validateComponentVersions(compManifests, res)
	validateManifestDependencies(compManifests, compMap, res)
	if l.rules != nil {
		l.evaluateMatrixRules(compMap, res)
	}
}

// validateComponentVersions parses and validates all declared Component.spec.version strings
// across manifests using componentsemver.Parse, recording INVALID_VERSION diagnostics for malformed strings.
func validateComponentVersions(compManifests []*ComponentManifest, res *ValidationResult) {
	for _, c := range compManifests {
		if c == nil || c.Spec.Version == "" {
			continue
		}
		line := max(c.Line, 1)
		if _, err := componentsemver.Parse(c.Spec.Version); err != nil {
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
		cName := naming.CanonicalComponentName(c.Metadata.Name)
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
	depName := naming.CanonicalComponentName(dep.Name)
	depComp, exists := compMap[depName]
	if !exists || depComp == nil || !depComp.IsEnabled() || depComp.Spec.Version == "" {
		return
	}
	if _, err := componentsemver.Parse(depComp.Spec.Version); err != nil {
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

// evaluateMatrixRules evaluates profile-level compatibility matrix rules configured in rules.yaml.
func (l *Linter) evaluateMatrixRules(compMap map[string]*ComponentManifest, res *ValidationResult) {
	l.evaluateComponentDependencies(compMap, res)
	l.evaluatePlatformRequirements(compMap, res)
}

// evaluateComponentDependencies iterates through all ComponentDependencies rules defined in rules.yaml,
// applying each rule to enabled target components that match the rule's version filter.
func (l *Linter) evaluateComponentDependencies(compMap map[string]*ComponentManifest, res *ValidationResult) {
	for _, rule := range l.rules.CompatibilityMatrix.ComponentDependencies {
		ruleCompName := naming.CanonicalComponentName(rule.Component)
		comp, exists := compMap[ruleCompName]
		if !exists || comp == nil || !comp.IsEnabled() {
			continue
		}
		line := comp.Line
		if line <= 0 {
			line = 1
		}
		if !shouldEvaluateRule(comp, line, rule, ruleCompName, res) {
			continue
		}
		for _, req := range rule.Requires {
			evaluateDependencyRequirement(comp, line, ruleCompName, req, compMap, res)
		}
	}
}

// shouldEvaluateRule reports whether a compatibility rule applies to the given component,
// evaluating the optional rule.ComponentVersion SemVer constraint filter against comp.Spec.Version.
func shouldEvaluateRule(comp *ComponentManifest, line int, rule ComponentDependencyRule, ruleCompName string, res *ValidationResult) bool {
	if rule.ComponentVersion == "" {
		return true
	}
	if comp.Spec.Version == "" {
		return false
	}
	if _, err := componentsemver.Parse(comp.Spec.Version); err != nil {
		return false
	}
	match, err := MatchConstraint(comp.Spec.Version, rule.ComponentVersion)
	if err != nil {
		res.AddError(comp.FilePath, line, "INVALID_VERSION_CONSTRAINT", fmt.Sprintf("invalid componentVersion constraint %q in compatibility rule for %q: %v", rule.ComponentVersion, ruleCompName, err))
		return false
	}
	return match
}

// evaluateDependencyRequirement validates a single dependency requirement against the component map,
// asserting presence (unless optional: true), enabled status, and SemVer compatibility.
func evaluateDependencyRequirement(comp *ComponentManifest, line int, ruleCompName string, req DependencyRequirement, compMap map[string]*ComponentManifest, res *ValidationResult) {
	reqCompName := naming.CanonicalComponentName(req.Component)
	reqComp, reqExists := compMap[reqCompName]
	if !reqExists || reqComp == nil || !reqComp.IsEnabled() {
		if !req.Optional {
			reportMissingRequiredComponent(comp.FilePath, line, ruleCompName, reqCompName, req, res)
		}
		return
	}
	if req.Version == "" || reqComp.Spec.Version == "" {
		return
	}
	if _, err := componentsemver.Parse(reqComp.Spec.Version); err != nil {
		return
	}
	satisfied, err := MatchConstraint(reqComp.Spec.Version, req.Version)
	if err != nil {
		res.AddError(comp.FilePath, line, "INVALID_VERSION_CONSTRAINT", fmt.Sprintf("invalid version constraint %q for dependency %q in compatibility rule for %q: %v", req.Version, reqCompName, ruleCompName, err))
		return
	}
	if !satisfied {
		reportVersionIncompatibility(comp.FilePath, line, ruleCompName, reqCompName, req, reqComp.Spec.Version, res)
	}
}

func reportMissingRequiredComponent(filePath string, line int, ruleCompName, reqCompName string, req DependencyRequirement, res *ValidationResult) {
	msg := fmt.Sprintf("component %q requires missing or disabled component %q", ruleCompName, reqCompName)
	if req.Version != "" {
		msg = fmt.Sprintf("component %q requires missing or disabled component %q (%s)", ruleCompName, reqCompName, req.Version)
	}
	if req.Reason != "" {
		msg = fmt.Sprintf("%s; reason: %s", msg, req.Reason)
	}
	res.AddError(filePath, line, "REQUIRED_COMPONENT_MISSING", msg)
}

func reportVersionIncompatibility(filePath string, line int, ruleCompName, reqCompName string, req DependencyRequirement, actualVersion string, res *ValidationResult) {
	msg := fmt.Sprintf("component %q requires %q version %q, but found version %q", ruleCompName, reqCompName, req.Version, actualVersion)
	if req.Reason != "" {
		msg = fmt.Sprintf("%s; reason: %s", msg, req.Reason)
	}
	res.AddError(filePath, line, "VERSION_INCOMPATIBILITY", msg)
}

// evaluatePlatformRequirements iterates over PlatformRequirements rules in rules.yaml,
// validating Kubernetes version bounds and required CRD specifications for all enabled components.
func (l *Linter) evaluatePlatformRequirements(compMap map[string]*ComponentManifest, res *ValidationResult) {
	for _, rule := range l.rules.CompatibilityMatrix.PlatformRequirements {
		ruleCompName := naming.CanonicalComponentName(rule.Component)
		comp, exists := compMap[ruleCompName]
		if !exists || !comp.IsEnabled() {
			continue
		}
		line := comp.Line
		if line <= 0 {
			line = 1
		}
		validatePlatformRule(comp.FilePath, line, ruleCompName, rule.Platform, res)
	}
}

// validatePlatformRule orchestrates K8s version bounds, constraint syntax, and CRD format checks for a platform rule.
func validatePlatformRule(filePath string, line int, compName string, p PlatformRequirementConfig, res *ValidationResult) {
	validateK8sVersions(filePath, line, compName, p.MinK8sVersion, p.MaxK8sVersion, res)
	validateK8sConstraint(filePath, line, compName, p.KubernetesVersion, res)
	validateRequiredCRDs(filePath, line, compName, p.RequiredCRDs, res)
}

// validateK8sVersions validates the SemVer syntax of minK8sVersion and maxK8sVersion,
// and asserts that minK8sVersion is not greater than maxK8sVersion.
func validateK8sVersions(filePath string, line int, compName, minK8s, maxK8s string, res *ValidationResult) {
	var minVer, maxVer *semver.Version
	if minK8s != "" {
		v, err := semver.NewVersion(strings.TrimSpace(minK8s))
		if err != nil {
			res.AddError(filePath, line, "INVALID_VERSION_CONSTRAINT", fmt.Sprintf("invalid minK8sVersion %q for component %q: %v", minK8s, compName, err))
		} else {
			minVer = v
		}
	}
	if maxK8s != "" {
		v, err := semver.NewVersion(strings.TrimSpace(maxK8s))
		if err != nil {
			res.AddError(filePath, line, "INVALID_VERSION_CONSTRAINT", fmt.Sprintf("invalid maxK8sVersion %q for component %q: %v", maxK8s, compName, err))
		} else {
			maxVer = v
		}
	}
	if minVer != nil && maxVer != nil && minVer.GreaterThan(maxVer) {
		res.AddError(filePath, line, "INVALID_VERSION_CONSTRAINT", fmt.Sprintf("minK8sVersion %q cannot be greater than maxK8sVersion %q for component %q", minK8s, maxK8s, compName))
	}
}

// validateK8sConstraint verifies that the kubernetesVersion SemVer constraint expression is syntactically valid.
func validateK8sConstraint(filePath string, line int, compName, k8sConstraint string, res *ValidationResult) {
	if k8sConstraint == "" {
		return
	}
	// Use a dummy version to validate that the constraint string is syntactically correct,
	// as the linter doesn't have access to the target cluster's Kubernetes version.
	if _, err := MatchConstraint("1.0.0", k8sConstraint); err != nil {
		res.AddError(filePath, line, "INVALID_VERSION_CONSTRAINT", fmt.Sprintf("invalid kubernetesVersion constraint %q for component %q: %v", k8sConstraint, compName, err))
	}
}

// validateRequiredCRDs validates all required CRD identifier strings in a platform requirement rule.
func validateRequiredCRDs(filePath string, line int, compName string, requiredCRDs []string, res *ValidationResult) {
	for _, crd := range requiredCRDs {
		validateSingleRequiredCRD(filePath, line, compName, crd, res)
	}
}

// validateSingleRequiredCRD enforces Kubernetes RFC-1123 DNS subdomain label format (<plural>.<group>)
// and length limits (<= 253 chars total, <= 63 chars per segment) on a required CRD string.
func validateSingleRequiredCRD(filePath string, line int, compName, crd string, res *ValidationResult) {
	trimmed := strings.TrimSpace(crd)
	if trimmed == "" {
		res.AddError(filePath, line, "INVALID_CRD_REQUIREMENT", fmt.Sprintf("empty requiredCRD in platform rule for component %q", compName))
		return
	}
	if len(trimmed) > 253 {
		res.AddError(filePath, line, "INVALID_CRD_REQUIREMENT", fmt.Sprintf("invalid requiredCRD %q for component %q: total length cannot exceed 253 characters", trimmed, compName))
		return
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 {
		res.AddError(filePath, line, "INVALID_CRD_REQUIREMENT", fmt.Sprintf("invalid requiredCRD %q for component %q: must be in <plural>.<group> format", trimmed, compName))
		return
	}
	for _, part := range parts {
		if len(part) > 63 {
			res.AddError(filePath, line, "INVALID_CRD_REQUIREMENT", fmt.Sprintf("invalid requiredCRD %q for component %q: segment %q exceeds 63 characters", trimmed, compName, part))
			return
		}
		if part == "" || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			res.AddError(filePath, line, "INVALID_CRD_REQUIREMENT", fmt.Sprintf("invalid requiredCRD %q for component %q: segments must be non-empty and cannot start or end with '-'", trimmed, compName))
			return
		}
		for _, r := range part {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
				res.AddError(filePath, line, "INVALID_CRD_REQUIREMENT", fmt.Sprintf("invalid requiredCRD %q for component %q: segments must contain only lowercase alphanumeric characters or '-'", trimmed, compName))
				return
			}
		}
	}
}
