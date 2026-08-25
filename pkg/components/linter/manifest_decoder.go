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

// UnmarshalYAML implements flexible duration and numeric seconds unmarshaling for lifecycle control.
func (c *ComponentLifecycleCtrl) UnmarshalYAML(node *yaml.Node) error {
	type rawCtrl struct {
		DrainTimeoutSeconds yaml.Node `yaml:"drainTimeoutSeconds,omitempty"`
		GracePeriodSeconds  yaml.Node `yaml:"gracePeriodSeconds,omitempty"`
		ForceDeletion       bool      `yaml:"forceDeletion,omitempty"`
		DrainingTimeout     yaml.Node `yaml:"drainingTimeout,omitempty"`
		GracePeriod         yaml.Node `yaml:"gracePeriod,omitempty"`
	}
	var raw rawCtrl
	if err := node.Decode(&raw); err != nil {
		return err
	}
	c.ForceDeletion = raw.ForceDeletion

	drainNode := firstNonEmptyNode(&raw.DrainTimeoutSeconds, &raw.DrainingTimeout)
	if drainNode != nil {
		sec, err := parseDurationNode(drainNode, "drainTimeoutSeconds")
		if err != nil {
			return err
		}
		c.DrainTimeoutSeconds = &sec
	}

	graceNode := firstNonEmptyNode(&raw.GracePeriodSeconds, &raw.GracePeriod)
	if graceNode != nil {
		sec, err := parseDurationNode(graceNode, "gracePeriodSeconds")
		if err != nil {
			return err
		}
		c.GracePeriodSeconds = &sec
	}
	return nil
}

func firstNonEmptyNode(nodes ...*yaml.Node) *yaml.Node {
	for _, n := range nodes {
		if n != nil && n.Kind != 0 {
			return n
		}
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
	if node.Tag == "!!null" || node.Value == "~" || node.Value == "null" {
		return 0, nil
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
	seconds := int(d.Round(time.Second) / time.Second)
	if d > 0 && seconds == 0 {
		seconds = 1
	}
	return seconds, nil
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
	s.Version, s.DeletionPolicy, s.DeletionProtection = raw.Version, raw.DeletionPolicy, raw.DeletionProtection
	s.WrapperRef, s.LifecycleControl, s.Manifests, s.Description = raw.WrapperRef, raw.LifecycleControl, raw.Manifests, raw.Description
	return s.decodeDependencies(&raw.Dependencies)
}

func (s *ComponentManifestSpec) decodeDependencies(node *yaml.Node) error {
	if node == nil || node.Kind == 0 {
		return nil
	}
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	if node.Tag == "!!null" || node.Value == "~" || node.Value == "null" {
		return nil
	}
	if node.Kind == yaml.SequenceNode {
		for _, elem := range node.Content {
			var dep ComponentDependencyRef
			if err := elem.Decode(&dep); err != nil {
				return err
			}
			s.Dependencies = append(s.Dependencies, dep)
		}
		return nil
	}
	if node.Kind == yaml.MappingNode {
		return s.decodeDependenciesMap(node)
	}
	return fmt.Errorf("dependencies must be a sequence or a mapping")
}

func (s *ComponentManifestSpec) decodeDependenciesMap(node *yaml.Node) error {
	if len(node.Content)%2 != 0 {
		return fmt.Errorf("invalid mapping node: odd number of elements")
	}
	for i := 0; i < len(node.Content); i += 2 {
		dep, err := decodeMappingDependency(node.Content[i], node.Content[i+1])
		if err != nil {
			return err
		}
		s.Dependencies = append(s.Dependencies, *dep)
	}
	return nil
}

func decodeMappingDependency(keyNode, valNode *yaml.Node) (*ComponentDependencyRef, error) {
	if keyNode == nil || valNode == nil {
		return nil, fmt.Errorf("invalid mapping dependency: key or value node is nil")
	}
	for keyNode.Kind == yaml.AliasNode && keyNode.Alias != nil {
		keyNode = keyNode.Alias
	}
	for valNode.Kind == yaml.AliasNode && valNode.Alias != nil {
		valNode = valNode.Alias
	}
	dep := &ComponentDependencyRef{Name: keyNode.Value}
	if valNode.Tag == "!!null" || valNode.Value == "~" || valNode.Value == "null" {
		return dep, nil
	}
	if valNode.Kind == yaml.ScalarNode {
		dep.Version = valNode.Value
		return dep, nil
	}
	if valNode.Kind == yaml.MappingNode {
		return decodeMappingDependencyDetails(dep, valNode)
	}
	return nil, fmt.Errorf("invalid dependency detail format for %q", keyNode.Value)
}

func decodeMappingDependencyDetails(dep *ComponentDependencyRef, valNode *yaml.Node) (*ComponentDependencyRef, error) {
	type rawDepDetail struct {
		Version  string `yaml:"version"`
		Optional bool   `yaml:"optional"`
	}
	var detail rawDepDetail
	if err := valNode.Decode(&detail); err != nil {
		return nil, err
	}
	dep.Version, dep.Optional = detail.Version, detail.Optional
	return dep, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
