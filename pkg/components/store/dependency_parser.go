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

package store

import (
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

const (
	annotationDependsOn    = "xmc.dev/depends-on"
	annotationDependencies = "xmc.dev/dependencies"
)

var descDirectivePattern = regexp.MustCompile(`(?i)(?:^|\n)\s*(?:[-*+]\s+)?(?:@|xmc:|xmc\.dev/)?(?:depends-on|dependencies)\s*:\s*([^\n]+)`)

// ExtractDependencies extracts all component dependency references from the component spec and labels.
// It deduplicates, canonicalizes, filters out self-references, and returns sorted dependencies.
func ExtractDependencies(spec *componentspb.ComponentSpec, labels map[string]string, selfName string) []string {
	var rawDeps []string
	if spec != nil {
		if len(spec.GetManifests()) > 0 {
			rawDeps = append(rawDeps, ExtractDependenciesFromManifests(spec.GetManifests())...)
		}
		if spec.GetDescription() != "" {
			rawDeps = append(rawDeps, ExtractDependenciesFromDescription(spec.GetDescription())...)
		}
	}
	if len(labels) > 0 {
		rawDeps = append(rawDeps, ExtractDependenciesFromLabels(labels)...)
	}
	return NormalizeDependencies(rawDeps, selfName)
}

// ExtractDependenciesFromManifests parses raw YAML manifests (supporting multi-document streams)
// and extracts dependency declarations from metadata annotations and spec.dependencies.
func ExtractDependenciesFromManifests(manifests []string) []string {
	var raw []string
	for _, manifest := range manifests {
		trimmed := strings.TrimSpace(manifest)
		if trimmed == "" {
			continue
		}
		dec := yaml.NewDecoder(strings.NewReader(trimmed))
		for {
			var doc any
			if err := dec.Decode(&doc); err != nil {
				break
			}
			if m, ok := doc.(map[string]any); ok && m != nil {
				raw = append(raw, extractFromDoc(m)...)
			}
		}
	}
	return raw
}

// extractFromDoc extracts annotations and spec dependencies from an unmarshaled YAML document.
func extractFromDoc(doc map[string]any) []string {
	var deps []string
	deps = append(deps, extractFromDocAnnotations(doc)...)
	deps = append(deps, extractFromDocSpec(doc)...)
	return deps
}

func extractFromDocAnnotations(doc map[string]any) []string {
	meta, ok := doc["metadata"].(map[string]any)
	if !ok {
		return nil
	}
	annotations, ok := meta["annotations"].(map[string]any)
	if !ok {
		return nil
	}
	var deps []string
	if val, ok := annotations[annotationDependsOn].(string); ok {
		deps = append(deps, val)
	}
	if val, ok := annotations[annotationDependencies].(string); ok {
		deps = append(deps, val)
	}
	return deps
}

func extractFromDocSpec(doc map[string]any) []string {
	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		return nil
	}
	depVal, ok := spec["dependencies"]
	if !ok {
		return nil
	}
	return parseSpecDependencies(depVal)
}

// parseSpecDependencies extracts dependencies from string, list of strings, list of named objects, or maps.
func parseSpecDependencies(val any) []string {
	var res []string
	switch v := val.(type) {
	case string:
		res = append(res, v)
	case []any:
		for _, item := range v {
			switch elem := item.(type) {
			case string:
				res = append(res, elem)
			case map[string]any:
				if name, ok := elem["name"].(string); ok {
					res = append(res, name)
				}
			}
		}
	case map[string]any:
		for k := range v {
			res = append(res, k)
		}
	}
	return res
}

// ExtractDependenciesFromDescription extracts dependency directives from component description strings.
func ExtractDependenciesFromDescription(desc string) []string {
	var deps []string
	matches := descDirectivePattern.FindAllStringSubmatch(desc, -1)
	for _, match := range matches {
		if len(match) > 1 {
			deps = append(deps, match[1])
		}
	}
	return deps
}

// ExtractDependenciesFromLabels extracts dependency references from well-known label keys.
func ExtractDependenciesFromLabels(labels map[string]string) []string {
	var deps []string
	if val, ok := labels[annotationDependsOn]; ok {
		deps = append(deps, val)
	}
	if val, ok := labels[annotationDependencies]; ok {
		deps = append(deps, val)
	}
	return deps
}

// NormalizeDependencies tokenizes, canonicalizes, deduplicates, filters out self-references,
// validates component names, and sorts the dependency list alphabetically.
func NormalizeDependencies(rawDeps []string, selfName string) []string {
	canonicalSelf := CanonicalComponentName(selfName)
	seen := make(map[string]bool)
	result := []string{}

	for _, raw := range rawDeps {
		cleaned := cleanRawDependencyString(raw)
		for _, token := range strings.Fields(cleaned) {
			canonical := CanonicalComponentName(token)
			if canonical == "" || canonical == canonicalSelf {
				continue
			}
			if !IsValidComponentName(canonical) {
				continue
			}
			if !seen[canonical] {
				seen[canonical] = true
				result = append(result, canonical)
			}
		}
	}
	sort.Strings(result)
	return result
}

// cleanRawDependencyString strips comments and replaces delimiters and enclosing brackets/quotes with spaces.
func cleanRawDependencyString(raw string) string {
	if idx := strings.Index(raw, "#"); idx != -1 {
		raw = raw[:idx]
	}
	return strings.Map(func(r rune) rune {
		if r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == '[' || r == ']' || r == '"' || r == '\'' {
			return ' '
		}
		return r
	}, raw)
}
