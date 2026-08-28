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

	"xmanager_cloud/pkg/components/store"
)

// validateSchema validates YAML structural correctness and schema conformance.
func (l *Linter) validateSchema(rawManifests []*RawManifest, compManifests []*ComponentManifest, res *ValidationResult) {
	rules := l.rules
	if rules == nil {
		rules = &RulesConfig{}
	}
	for _, m := range rawManifests {
		l.validateRawManifest(m, rules, res)
	}
	for _, c := range compManifests {
		l.validateComponentManifest(c, rules, res)
	}
}

func (l *Linter) validateRawManifest(m *RawManifest, rules *RulesConfig, res *ValidationResult) {
	line := m.Line
	if line <= 0 {
		line = 1
	}
	requireNonEmptyField(m.FilePath, line, "apiVersion", m.APIVersion, res)
	requireNonEmptyField(m.FilePath, line, "kind", m.Kind, res)
	requireNonEmptyField(m.FilePath, line, "metadata.name", m.Metadata.Name, res)
}

func requireNonEmptyField(filePath string, line int, fieldName, val string, res *ValidationResult) {
	if strings.TrimSpace(val) == "" {
		res.AddError(filePath, line, "REQUIRED_FIELD", fmt.Sprintf("%s is required and cannot be empty", fieldName))
	}
}

func (l *Linter) validateComponentManifest(c *ComponentManifest, rules *RulesConfig, res *ValidationResult) {
	line := c.Line
	if line <= 0 {
		line = 1
	}
	if c.Metadata.Name != "" && !store.IsValidComponentName(c.Metadata.Name) {
		res.AddError(c.FilePath, line, "INVALID_COMPONENT_NAME", fmt.Sprintf("invalid component name %q: must conform to DNS-1123", c.Metadata.Name))
	}
	if c.Spec.ComponentType != "" && !isValidComponentType(c.Spec.ComponentType) {
		res.AddError(c.FilePath, line, "INVALID_COMPONENT_TYPE", fmt.Sprintf("invalid spec.componentType %q", c.Spec.ComponentType))
	}
	if c.Spec.DeletionPolicy != "" && !isValidDeletionPolicy(c.Spec.DeletionPolicy) {
		res.AddError(c.FilePath, line, "INVALID_DELETION_POLICY", fmt.Sprintf("invalid spec.deletionPolicy %q", c.Spec.DeletionPolicy))
	}
	if is3PWrapper(c.Spec.ComponentType) {
		l.validate3PWrapper(c, line, res)
	} else if c.Spec.WrapperRef != nil {
		res.AddError(c.FilePath, line, "INVALID_COMPONENT_TYPE", "spec.wrapperRef is only allowed for 3P wrapper components")
	}
	validateLifecycleControl(c.Spec.LifecycleControl, c.FilePath, line, rules, res)
	if rules.StaticChecks.EnforceDeletionProtection {
		if !c.Spec.DeletionProtection {
			res.AddError(c.FilePath, line, "DELETION_PROTECTION_REQUIRED", "spec.deletionProtection must be true when deletion protection is enforced")
		}
	}
}

func validateLifecycleControl(ctrl *ComponentLifecycleCtrl, filePath string, line int, rules *RulesConfig, res *ValidationResult) {
	if ctrl == nil {
		return
	}
	if ctrl.DrainTimeoutSeconds != nil {
		drain := *ctrl.DrainTimeoutSeconds
		if drain < 0 {
			res.AddError(filePath, line, "INVALID_DRAIN_TIMEOUT", "spec.lifecycleControl.drainTimeoutSeconds must be non-negative")
		} else if rules != nil && rules.StaticChecks.MaxDrainTimeoutSeconds > 0 && drain > rules.StaticChecks.MaxDrainTimeoutSeconds {
			res.AddError(filePath, line, "DRAIN_TIMEOUT_EXCEEDED", fmt.Sprintf("spec.lifecycleControl.drainTimeoutSeconds (%d) exceeds max allowed (%d)", drain, rules.StaticChecks.MaxDrainTimeoutSeconds))
		}
	}
	if ctrl.GracePeriodSeconds != nil && *ctrl.GracePeriodSeconds < 0 {
		res.AddError(filePath, line, "INVALID_GRACE_PERIOD", "spec.lifecycleControl.gracePeriodSeconds must be non-negative")
	}
}

func (l *Linter) validate3PWrapper(c *ComponentManifest, line int, res *ValidationResult) {
	if c.Spec.WrapperRef == nil {
		res.AddError(c.FilePath, line, "MISSING_WRAPPER_REF", "spec.wrapperRef is required for 3P wrapper components")
		return
	}
	w := c.Spec.WrapperRef
	if strings.TrimSpace(w.CrdGroup) == "" {
		res.AddError(c.FilePath, line, "INVALID_WRAPPER_REF", "spec.wrapperRef.crdGroup is required")
	}
	if strings.TrimSpace(w.CrdVersion) == "" {
		res.AddError(c.FilePath, line, "INVALID_WRAPPER_REF", "spec.wrapperRef.crdVersion is required")
	}
	if strings.TrimSpace(w.CrdKind) == "" {
		res.AddError(c.FilePath, line, "INVALID_WRAPPER_REF", "spec.wrapperRef.crdKind is required")
	}
	if strings.TrimSpace(w.TargetName) == "" {
		res.AddError(c.FilePath, line, "INVALID_WRAPPER_REF", "spec.wrapperRef.targetName is required")
	}
}

func is1PComponent(ct string) bool {
	switch strings.ToUpper(strings.TrimSpace(ct)) {
	case "COMPONENT_TYPE_UNSPECIFIED",
		"COMPONENT_TYPE_CUSTOM_1P_COMPONENT", "CUSTOM_1P_COMPONENT":
		return true
	default:
		return false
	}
}

func is3PWrapper(ct string) bool {
	switch strings.ToUpper(strings.TrimSpace(ct)) {
	case "COMPONENT_TYPE_CUSTOM_3P_WRAPPER", "CUSTOM_3P_WRAPPER":
		return true
	default:
		return false
	}
}

func isValidComponentType(ct string) bool {
	return is1PComponent(ct) || is3PWrapper(ct)
}

func isValidDeletionPolicy(dp string) bool {
	switch strings.ToUpper(strings.TrimSpace(dp)) {
	case "DELETION_POLICY_UNSPECIFIED",
		"DELETION_POLICY_DELETE", "DELETE",
		"DELETION_POLICY_ORPHAN", "ORPHAN":
		return true
	default:
		return false
	}
}
