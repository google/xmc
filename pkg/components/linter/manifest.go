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

import "strings"

// ObjectMetadata represents standard Kubernetes metadata on manifests.
type ObjectMetadata struct {
	Name        string            `yaml:"name" json:"name"`
	Namespace   string            `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// RawManifest represents an unspecialized Kubernetes or component YAML manifest.
type RawManifest struct {
	APIVersion string         `yaml:"apiVersion" json:"apiVersion"`
	Kind       string         `yaml:"kind" json:"kind"`
	Metadata   ObjectMetadata `yaml:"metadata" json:"metadata"`
	FilePath   string         `yaml:"-" json:"-"`
	Line       int            `yaml:"-" json:"-"`
}

// ComponentManifest represents a Component custom resource declaration.
type ComponentManifest struct {
	APIVersion string                `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                `yaml:"kind" json:"kind"`
	Metadata   ObjectMetadata        `yaml:"metadata" json:"metadata"`
	Spec       ComponentManifestSpec `yaml:"spec" json:"spec"`
	FilePath   string                `yaml:"-" json:"-"`
	Line       int                   `yaml:"-" json:"-"`
}

// IsEnabled returns true if the component is explicitly enabled or defaults to enabled.
func (c *ComponentManifest) IsEnabled() bool {
	if c.Spec.Enabled == nil {
		return true
	}
	return *c.Spec.Enabled
}

// EffectiveStability returns the normalized stability tier ("EXPERIMENTAL", "PREVIEW", or "STABLE"),
// or the trimmed spec.stability value when unset or unrecognized ("" if c is nil).
func (c *ComponentManifest) EffectiveStability() string {
	if c == nil {
		return ""
	}
	trimmed := strings.TrimSpace(c.Spec.Stability)
	switch trimmed {
	case "EXPERIMENTAL", "COMPONENT_STABILITY_EXPERIMENTAL":
		return "EXPERIMENTAL"
	case "PREVIEW", "COMPONENT_STABILITY_PREVIEW":
		return "PREVIEW"
	case "STABLE", "COMPONENT_STABILITY_STABLE":
		return "STABLE"
	default:
		return trimmed
	}
}

// ComponentDependencyRef specifies a dependency name and optional SemVer constraint.
type ComponentDependencyRef struct {
	Name     string `yaml:"name" json:"name"`
	Version  string `yaml:"version,omitempty" json:"version,omitempty"`
	Optional bool   `yaml:"optional,omitempty" json:"optional,omitempty"`
}

// ComponentWrapperRef configures reference metadata for third-party CRDs and controllers.
type ComponentWrapperRef struct {
	CrdGroup                 string `yaml:"crdGroup" json:"crdGroup"`
	CrdVersion               string `yaml:"crdVersion" json:"crdVersion"`
	CrdKind                  string `yaml:"crdKind" json:"crdKind"`
	TargetNamespace          string `yaml:"targetNamespace,omitempty" json:"targetNamespace,omitempty"`
	TargetName               string `yaml:"targetName" json:"targetName"`
	ObservedGenerationPath   string `yaml:"observedGenerationPath,omitempty" json:"observedGenerationPath,omitempty"`
	ReadyConditionPath       string `yaml:"readyConditionPath,omitempty" json:"readyConditionPath,omitempty"`
	ExpectedReadyValue       string `yaml:"expectedReadyValue,omitempty" json:"expectedReadyValue,omitempty"`
	EndpointJsonPath         string `yaml:"endpointJsonPath,omitempty" json:"endpointJsonPath,omitempty"`
	ConnectionSecretJsonPath string `yaml:"connectionSecretJsonPath,omitempty" json:"connectionSecretJsonPath,omitempty"`
}

// ComponentLifecycleCtrl defines lifecycle timeouts and drain control.
type ComponentLifecycleCtrl struct {
	DrainTimeoutSeconds *int `yaml:"drainTimeoutSeconds,omitempty" json:"drainTimeoutSeconds,omitempty"`
	GracePeriodSeconds  *int `yaml:"gracePeriodSeconds,omitempty" json:"gracePeriodSeconds,omitempty"`
	ForceDeletion       bool `yaml:"forceDeletion,omitempty" json:"forceDeletion,omitempty"`
}

// ComponentManifestSpec holds the specification body of a Component manifest.
type ComponentManifestSpec struct {
	Enabled                 *bool                    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	ComponentType           string                   `yaml:"componentType,omitempty" json:"componentType,omitempty"`
	ComponentClass          string                   `yaml:"componentClass,omitempty" json:"componentClass,omitempty"`
	Version                 string                   `yaml:"version,omitempty" json:"version,omitempty"`
	ChartVersion            string                   `yaml:"chartVersion,omitempty" json:"chartVersion,omitempty"`
	ImageTag                string                   `yaml:"imageTag,omitempty" json:"imageTag,omitempty"`
	Stability               string                   `yaml:"stability,omitempty" json:"stability,omitempty"`
	AcknowledgeExperimental bool                     `yaml:"acknowledgeExperimental,omitempty" json:"acknowledgeExperimental,omitempty"`
	DeletionPolicy          string                   `yaml:"deletionPolicy,omitempty" json:"deletionPolicy,omitempty"`
	DeletionProtection      bool                     `yaml:"deletionProtection,omitempty" json:"deletionProtection,omitempty"`
	WrapperRef              *ComponentWrapperRef     `yaml:"wrapperRef,omitempty" json:"wrapperRef,omitempty"`
	LifecycleControl        *ComponentLifecycleCtrl  `yaml:"lifecycleControl,omitempty" json:"lifecycleControl,omitempty"`
	Parameters              map[string]string        `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	Dependencies            []ComponentDependencyRef `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	Manifests               []string                 `yaml:"manifests,omitempty" json:"manifests,omitempty"`
	Description             string                   `yaml:"description,omitempty" json:"description,omitempty"`
}
