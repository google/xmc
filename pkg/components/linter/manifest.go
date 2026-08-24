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
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

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

// ComponentDependencyRef specifies a dependency name and optional SemVer constraint.
type ComponentDependencyRef struct {
	Name     string `yaml:"name" json:"name"`
	Version  string `yaml:"version,omitempty" json:"version,omitempty"`
	Optional bool   `yaml:"optional,omitempty" json:"optional,omitempty"`
}

// UnmarshalYAML implements custom decoding for ComponentDependencyRef supporting strings or mappings.
func (d *ComponentDependencyRef) UnmarshalYAML(node *yaml.Node) error {
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	if node.Kind == yaml.ScalarNode {
		d.Name = node.Value
		return nil
	}
	if node.Kind == yaml.MappingNode {
		type alias ComponentDependencyRef
		var a alias
		if err := node.Decode(&a); err != nil {
			return err
		}
		*d = ComponentDependencyRef(a)
		return nil
	}
	return fmt.Errorf("invalid dependency reference format")
}

// ComponentWrapperRef configures reference metadata for third-party CRDs and controllers.
type ComponentWrapperRef struct {
	CrdGroup        string `yaml:"crdGroup" json:"crdGroup"`
	CrdVersion      string `yaml:"crdVersion" json:"crdVersion"`
	CrdKind         string `yaml:"crdKind" json:"crdKind"`
	TargetNamespace string `yaml:"targetNamespace,omitempty" json:"targetNamespace,omitempty"`
	TargetName      string `yaml:"targetName" json:"targetName"`
}

// UnmarshalYAML decodes ComponentWrapperRef with fallback aliases.
func (w *ComponentWrapperRef) UnmarshalYAML(node *yaml.Node) error {
	type rawWrapper struct {
		CrdGroup        string `yaml:"crdGroup"`
		CrdVersion      string `yaml:"crdVersion"`
		CrdKind         string `yaml:"crdKind"`
		TargetNamespace string `yaml:"targetNamespace"`
		TargetName      string `yaml:"targetName"`
		Group           string `yaml:"group"`
		Version         string `yaml:"version"`
		Kind            string `yaml:"kind"`
		Namespace       string `yaml:"namespace"`
		Name            string `yaml:"name"`
	}
	var raw rawWrapper
	if err := node.Decode(&raw); err != nil {
		return err
	}
	w.CrdGroup = firstNonEmpty(raw.CrdGroup, raw.Group)
	w.CrdVersion = firstNonEmpty(raw.CrdVersion, raw.Version)
	w.CrdKind = firstNonEmpty(raw.CrdKind, raw.Kind)
	w.TargetNamespace = firstNonEmpty(raw.TargetNamespace, raw.Namespace)
	w.TargetName = firstNonEmpty(raw.TargetName, raw.Name)
	return nil
}

// ComponentLifecycleCtrl defines lifecycle timeouts and drain control.
type ComponentLifecycleCtrl struct {
	DrainTimeoutSeconds int  `yaml:"drainTimeoutSeconds,omitempty" json:"drainTimeoutSeconds,omitempty"`
	GracePeriodSeconds  int  `yaml:"gracePeriodSeconds,omitempty" json:"gracePeriodSeconds,omitempty"`
	ForceDeletion       bool `yaml:"forceDeletion,omitempty" json:"forceDeletion,omitempty"`
}

// UnmarshalYAML decodes lifecycle controls supporting numeric seconds or duration strings.
func (c *ComponentLifecycleCtrl) UnmarshalYAML(node *yaml.Node) error {
	type rawCtrl struct {
		DrainTimeoutSeconds int       `yaml:"drainTimeoutSeconds,omitempty"`
		GracePeriodSeconds  int       `yaml:"gracePeriodSeconds,omitempty"`
		ForceDeletion       bool      `yaml:"forceDeletion,omitempty"`
		DrainingTimeout     yaml.Node `yaml:"drainingTimeout,omitempty"`
		GracePeriod         yaml.Node `yaml:"gracePeriod,omitempty"`
	}
	var raw rawCtrl
	if err := node.Decode(&raw); err != nil {
		return err
	}
	c.DrainTimeoutSeconds = raw.DrainTimeoutSeconds
	c.GracePeriodSeconds = raw.GracePeriodSeconds
	c.ForceDeletion = raw.ForceDeletion

	if c.DrainTimeoutSeconds == 0 {
		sec, err := parseDurationNode(&raw.DrainingTimeout, "drainingTimeout")
		if err != nil {
			return err
		}
		c.DrainTimeoutSeconds = sec
	}
	if c.GracePeriodSeconds == 0 {
		sec, err := parseDurationNode(&raw.GracePeriod, "gracePeriod")
		if err != nil {
			return err
		}
		c.GracePeriodSeconds = sec
	}
	return nil
}

func parseDurationNode(node *yaml.Node, fieldName string) (int, error) {
	if node == nil || node.Kind == 0 {
		return 0, nil
	}
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	if node.Kind != yaml.ScalarNode {
		return 0, fmt.Errorf("%s must be a scalar value", fieldName)
	}
	if node.Value == "" {
		return 0, nil
	}
	sec, err := parseDurationSeconds(node.Value)
	if err != nil {
		return 0, fmt.Errorf("failed to parse %s %q: %w", fieldName, node.Value, err)
	}
	return sec, nil
}

func parseDurationSeconds(s string) (int, error) {
	if val, err := strconv.Atoi(s); err == nil {
		if val < 0 {
			return 0, fmt.Errorf("duration cannot be negative: %d", val)
		}
		return val, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("duration cannot be negative: %s", s)
	}
	seconds := int(d.Round(time.Second).Seconds())
	if d > 0 && seconds == 0 {
		seconds = 1
	}
	return seconds, nil
}

// ComponentManifestSpec holds the specification body of a Component manifest.
type ComponentManifestSpec struct {
	Enabled            *bool                    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	ComponentType      string                   `yaml:"componentType,omitempty" json:"componentType,omitempty"`
	Version            string                   `yaml:"version,omitempty" json:"version,omitempty"`
	DeletionPolicy     string                   `yaml:"deletionPolicy,omitempty" json:"deletionPolicy,omitempty"`
	DeletionProtection bool                     `yaml:"deletionProtection,omitempty" json:"deletionProtection,omitempty"`
	WrapperRef         *ComponentWrapperRef     `yaml:"wrapperRef,omitempty" json:"wrapperRef,omitempty"`
	LifecycleControl   *ComponentLifecycleCtrl  `yaml:"lifecycleControl,omitempty" json:"lifecycleControl,omitempty"`
	Dependencies       []ComponentDependencyRef `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	Manifests          []string                 `yaml:"manifests,omitempty" json:"manifests,omitempty"`
	Description        string                   `yaml:"description,omitempty" json:"description,omitempty"`
}

// UnmarshalYAML handles flexible dependencies formats (list of strings, list of objects, maps).
func (s *ComponentManifestSpec) UnmarshalYAML(node *yaml.Node) error {
	type rawSpec struct {
		Enabled            *bool                   `yaml:"enabled,omitempty"`
		Type               string                  `yaml:"type,omitempty"`
		ComponentType      string                  `yaml:"componentType,omitempty"`
		Version            string                  `yaml:"version,omitempty"`
		DeletionPolicy     string                  `yaml:"deletionPolicy,omitempty"`
		DeletionProtection bool                    `yaml:"deletionProtection,omitempty"`
		WrapperRef         *ComponentWrapperRef    `yaml:"wrapperRef,omitempty"`
		LifecycleControl   *ComponentLifecycleCtrl `yaml:"lifecycleControl,omitempty"`
		Dependencies       yaml.Node               `yaml:"dependencies,omitempty"`
		Manifests          []string                `yaml:"manifests,omitempty"`
		Description        string                  `yaml:"description,omitempty"`
	}
	var raw rawSpec
	if err := node.Decode(&raw); err != nil {
		return err
	}
	s.Enabled = raw.Enabled
	s.ComponentType = firstNonEmpty(raw.ComponentType, raw.Type)
	s.Version = raw.Version
	s.DeletionPolicy = raw.DeletionPolicy
	s.DeletionProtection = raw.DeletionProtection
	s.WrapperRef = raw.WrapperRef
	s.LifecycleControl = raw.LifecycleControl
	s.Manifests = raw.Manifests
	s.Description = raw.Description
	return s.decodeDependencies(&raw.Dependencies)
}

func (s *ComponentManifestSpec) decodeDependencies(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	if node.Kind == 0 {
		return nil
	}
	if node.Kind == yaml.SequenceNode {
		for _, item := range node.Content {
			var dep ComponentDependencyRef
			if err := item.Decode(&dep); err != nil {
				return err
			}
			s.Dependencies = append(s.Dependencies, dep)
		}
		return nil
	}
	if node.Kind == yaml.MappingNode {
		return s.decodeMappingDependencies(node)
	}
	if node.Kind == yaml.ScalarNode && node.Value != "" {
		s.Dependencies = append(s.Dependencies, ComponentDependencyRef{Name: node.Value})
	}
	return nil
}

func (s *ComponentManifestSpec) decodeMappingDependencies(node *yaml.Node) error {
	for i := 0; i < len(node.Content); i += 2 {
		if i+1 >= len(node.Content) {
			break
		}
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		dep := ComponentDependencyRef{Name: keyNode.Value}
		if valNode.Kind == yaml.ScalarNode {
			dep.Version = valNode.Value
		} else if valNode.Kind == yaml.MappingNode {
			var inner struct {
				Version  string `yaml:"version"`
				Optional bool   `yaml:"optional"`
			}
			if err := valNode.Decode(&inner); err != nil {
				return err
			}
			dep.Version = inner.Version
			dep.Optional = inner.Optional
		} else {
			return fmt.Errorf("unexpected dependency value format for %q", keyNode.Value)
		}
		s.Dependencies = append(s.Dependencies, dep)
	}
	return nil
}

// ComponentManifest models an XMC Component custom resource document.
type ComponentManifest struct {
	APIVersion string                `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                `yaml:"kind" json:"kind"`
	Metadata   ObjectMetadata        `yaml:"metadata" json:"metadata"`
	Spec       ComponentManifestSpec `yaml:"spec" json:"spec"`
	FilePath   string                `yaml:"-" json:"-"`
	Line       int                   `yaml:"-" json:"-"`
}

// IsEnabled returns whether the component is enabled (defaults to true if spec.enabled is omitted).
func (m *ComponentManifest) IsEnabled() bool {
	if m.Spec.Enabled == nil {
		return true
	}
	return *m.Spec.Enabled
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
