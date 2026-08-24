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

var clusterScopedKinds = map[string]bool{
	"clusterrole":                      true,
	"clusterrolebinding":               true,
	"namespace":                        true,
	"customresourcedefinition":         true,
	"validatingadmissionpolicy":        true,
	"validatingadmissionpolicybinding": true,
	"mutatingwebhookconfiguration":     true,
	"validatingwebhookconfiguration":   true,
	"persistentvolume":                 true,
	"storageclass":                     true,
	"priorityclass":                    true,
	"runtimeclass":                     true,
	"volumesnapshotclass":              true,
	"volumeattributesclass":            true,
	"volumesnapshotcontent":            true,
	"ingressclass":                     true,
	"gatewayclass":                     true,
	"clusterissuer":                    true,
	"apiservice":                       true,
	"flowschema":                       true,
	"prioritylevelconfiguration":       true,
	"node":                             true,
	"clustercidr":                      true,
	"certificatesigningrequest":        true,
}

// validateSchema validates YAML structural correctness, namespace scope, and schema conformance.
func (l *Linter) validateSchema(rawManifests []*RawManifest, compManifests []*ComponentManifest, res *ValidationResult) {
	for _, m := range rawManifests {
		l.validateRawManifest(m, res)
	}
	for _, c := range compManifests {
		l.validateComponentManifest(c, res)
	}
}

func (l *Linter) validateRawManifest(m *RawManifest, res *ValidationResult) {
	line := m.Line
	if line <= 0 {
		line = 1
	}
	if strings.TrimSpace(m.APIVersion) == "" {
		res.AddError(m.FilePath, line, "REQUIRED_FIELD", "apiVersion is required and cannot be empty")
	}
	if strings.TrimSpace(m.Kind) == "" {
		res.AddError(m.FilePath, line, "REQUIRED_FIELD", "kind is required and cannot be empty")
	}
	if strings.TrimSpace(m.Metadata.Name) == "" {
		res.AddError(m.FilePath, line, "REQUIRED_FIELD", "metadata.name is required and cannot be empty")
	}
	if l.rules != nil && l.rules.StaticChecks.EnforceNamespaceScope {
		normalizedKind := strings.ToLower(strings.TrimSpace(m.Kind))
		if clusterScopedKinds[normalizedKind] {
			res.AddError(m.FilePath, line, "CLUSTER_SCOPED_KIND_REJECTED", fmt.Sprintf("cluster-scoped kind %q is forbidden when namespace scope is enforced", m.Kind))
		}
	}
}

func (l *Linter) validateComponentManifest(c *ComponentManifest, res *ValidationResult) {
	line := c.Line
	if line <= 0 {
		line = 1
	}
	if !store.IsValidComponentName(c.Metadata.Name) {
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
	validateLifecycleControl(c.Spec.LifecycleControl, c.FilePath, line, l.rules, res)
	if l.rules != nil && l.rules.StaticChecks.EnforceDeletionProtection {
		if !c.Spec.DeletionProtection {
			res.AddError(c.FilePath, line, "DELETION_PROTECTION_REQUIRED", "spec.deletionProtection must be true when deletion protection is enforced")
		}
	}
}

func validateLifecycleControl(ctrl *ComponentLifecycleCtrl, filePath string, line int, rules *RulesConfig, res *ValidationResult) {
	if ctrl == nil {
		return
	}
	if ctrl.DrainTimeoutSeconds < 0 {
		res.AddError(filePath, line, "INVALID_DRAIN_TIMEOUT", "spec.lifecycleControl.drainTimeoutSeconds must be non-negative")
	}
	if ctrl.GracePeriodSeconds < 0 {
		res.AddError(filePath, line, "INVALID_GRACE_PERIOD", "spec.lifecycleControl.gracePeriodSeconds must be non-negative")
	}
	if rules != nil && rules.StaticChecks.MaxDrainTimeoutSeconds > 0 && ctrl.DrainTimeoutSeconds > rules.StaticChecks.MaxDrainTimeoutSeconds {
		res.AddError(filePath, line, "DRAIN_TIMEOUT_EXCEEDED", fmt.Sprintf("spec.lifecycleControl.drainTimeoutSeconds (%d) exceeds max allowed (%d)", ctrl.DrainTimeoutSeconds, rules.StaticChecks.MaxDrainTimeoutSeconds))
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

func isValidComponentType(ct string) bool {
	switch strings.ToUpper(strings.TrimSpace(ct)) {
	case "COMPONENT_TYPE_UNSPECIFIED",
		"COMPONENT_TYPE_CUSTOM_1P_COMPONENT", "CUSTOM_1P_COMPONENT", "1P", "1P_COMPONENT",
		"COMPONENT_TYPE_CUSTOM_3P_WRAPPER", "CUSTOM_3P_WRAPPER", "3P", "3P_WRAPPER":
		return true
	default:
		return false
	}
}

func is3PWrapper(ct string) bool {
	switch strings.ToUpper(strings.TrimSpace(ct)) {
	case "COMPONENT_TYPE_CUSTOM_3P_WRAPPER", "CUSTOM_3P_WRAPPER", "3P", "3P_WRAPPER":
		return true
	default:
		return false
	}
}

func isValidDeletionPolicy(dp string) bool {
	switch strings.ToUpper(strings.TrimSpace(dp)) {
	case "DELETION_POLICY_UNSPECIFIED", "UNSPECIFIED",
		"DELETION_POLICY_DELETE", "DELETE",
		"DELETION_POLICY_ORPHAN", "ORPHAN":
		return true
	default:
		return false
	}
}
