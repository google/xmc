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
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/google/xmc/pkg/components/naming"
)

// Linter executes multi-pass static validation against manifest directories.
type Linter struct {
	rules *RulesConfig
}

// NewLinter creates a new static linter with the provided rules configuration.
func NewLinter(rules *RulesConfig) *Linter {
	if rules == nil {
		rules = &RulesConfig{}
	}
	return &Linter{rules: rules}
}

// ValidateDirectory validates all YAML manifests located within the specified directory.
func (l *Linter) ValidateDirectory(dir string) (*ValidationResult, error) {
	if dir == "" {
		return nil, fmt.Errorf("directory path cannot be empty")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("directory stat failed: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path %q is not a directory", dir)
	}

	res := NewValidationResult()
	rawManifests, compManifests, err := l.collectManifests(dir, res)
	if err != nil {
		return nil, err
	}
	l.validateSchema(rawManifests, compManifests, res)
	compMap := l.buildComponentMap(compManifests, res)
	l.validateDAG(compManifests, compMap, res)
	l.validateMatrix(compManifests, compMap, res)
	return res, nil
}

// manifestSet holds the raw and Component manifests decoded from a group of files.
type manifestSet struct {
	raws  []*RawManifest
	comps []*ComponentManifest
}

func (l *Linter) collectManifests(dir string, res *ValidationResult) ([]*RawManifest, []*ComponentManifest, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve absolute path of %q: %w", dir, err)
	}

	// Manifests of nested chart roots are kept apart so that mergeNestedManifests can drop the
	// Components their enclosing chart already renders.
	var top, nested manifestSet
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !isManifestFile(path) {
			return walkErr
		}
		chartRoot := findChartRoot(path)
		set := &top
		if chartRoot != "" && isNestedChartRoot(absDir, chartRoot) {
			set = &nested
		}
		raws, comps := l.readManifestFile(path, chartRoot, res)
		set.raws = append(set.raws, raws...)
		set.comps = append(set.comps, comps...)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to scan manifests in %q: %w", dir, err)
	}
	merged := mergeNestedManifests(top, nested)
	return merged.raws, merged.comps, nil
}

// isManifestFile reports whether path is a YAML file that may contain Kubernetes manifests.
func isManifestFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return (ext == ".yaml" || ext == ".yml") && !isChartOrRulesConfigFile(path)
}

// readManifestFile reads path, renders it against chartRoot when it belongs to a Helm chart, and
// decodes the result. Read and render failures are reported on res.
func (l *Linter) readManifestFile(path, chartRoot string, res *ValidationResult) ([]*RawManifest, []*ComponentManifest) {
	data, err := os.ReadFile(path)
	if err != nil {
		res.AddError(path, 1, "FILE_READ_ERROR", fmt.Sprintf("failed to read file: %v", err))
		return nil, nil
	}
	if chartRoot != "" {
		if data, err = RenderChartTemplate(chartRoot, path); err != nil {
			res.AddError(path, 1, "YAML_SYNTAX", fmt.Sprintf("failed to render chart template in %s: %v", path, err))
			return nil, nil
		}
	}
	return l.parseManifestFile(path, data, res)
}

// mergeNestedManifests appends the manifests of nested chart roots to top, except each nested
// Component (and its raw document) whose canonical name a top-level chart already renders. The
// enclosing chart renders that Component itself, typically with profile-level values, so linting
// the nested copy too would only report DUPLICATE_COMPONENT_NAME. All other nested documents are
// kept, so nested templates are still rendered and checked.
func mergeNestedManifests(top, nested manifestSet) manifestSet {
	rendered := make(map[string]bool, len(top.comps))
	for _, c := range top.comps {
		if name := naming.CanonicalComponentName(c.Metadata.Name); name != "" {
			rendered[name] = true
		}
	}
	for _, r := range nested.raws {
		isComponent := isComponentGroup(r.APIVersion) && r.Kind == "Component"
		if !isComponent || !rendered[naming.CanonicalComponentName(r.Metadata.Name)] {
			top.raws = append(top.raws, r)
		}
	}
	for _, c := range nested.comps {
		if !rendered[naming.CanonicalComponentName(c.Metadata.Name)] {
			top.comps = append(top.comps, c)
		}
	}
	return top
}

func (l *Linter) parseManifestFile(path string, data []byte, res *ValidationResult) ([]*RawManifest, []*ComponentManifest) {
	var rawManifests []*RawManifest
	var compManifests []*ComponentManifest

	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			if !errors.Is(err, io.EOF) {
				res.AddError(path, 1, "YAML_SYNTAX", fmt.Sprintf("failed to parse YAML in %s: %v", path, err))
			}
			break
		}
		raw, comp := decodeDocumentNode(path, &node, res)
		if raw != nil {
			rawManifests = append(rawManifests, raw)
		}
		if comp != nil {
			compManifests = append(compManifests, comp)
		}
	}
	return rawManifests, compManifests
}

func decodeDocumentNode(path string, node *yaml.Node, res *ValidationResult) (*RawManifest, *ComponentManifest) {
	raw := decodeRawDocumentNode(path, node, res)
	if raw == nil {
		return nil, nil
	}
	hasComponentGroup := isComponentGroup(raw.APIVersion)
	if hasComponentGroup && raw.Kind != "Component" {
		res.AddError(path, raw.Line, "SCHEMA_ERROR", fmt.Sprintf("invalid kind %q for apiVersion %q; expected \"Component\"", raw.Kind, raw.APIVersion))
		return raw, nil
	}
	if !hasComponentGroup || raw.Kind != "Component" {
		return raw, nil
	}
	var comp ComponentManifest
	if err := node.Decode(&comp); err != nil {
		res.AddError(path, raw.Line, "SCHEMA_ERROR", fmt.Sprintf("failed to decode Component in %s: %v", path, err))
		return raw, nil
	}
	comp.FilePath, comp.Line = raw.FilePath, raw.Line
	return raw, &comp
}

func isComponentGroup(apiVersion string) bool {
	return strings.HasPrefix(apiVersion, "xmc.dev/") || apiVersion == "xmc.dev"
}

func decodeRawDocumentNode(path string, node *yaml.Node, res *ValidationResult) *RawManifest {
	if node.Kind == yaml.DocumentNode && (len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode) {
		return nil
	}
	line := node.Line
	if line <= 0 {
		line = 1
	}
	var raw RawManifest
	if err := node.Decode(&raw); err != nil {
		res.AddError(path, line, "SCHEMA_ERROR", fmt.Sprintf("failed to decode manifest in %s: %v", path, err))
		return nil
	}
	if raw.APIVersion == "" && raw.Kind == "" && raw.Metadata.Name == "" {
		return nil
	}
	raw.FilePath, raw.Line = path, line
	return &raw
}

func (l *Linter) buildComponentMap(compManifests []*ComponentManifest, res *ValidationResult) map[string]*ComponentManifest {
	compMap := make(map[string]*ComponentManifest, len(compManifests))
	for _, c := range compManifests {
		canonical := naming.CanonicalComponentName(c.Metadata.Name)
		if canonical == "" {
			continue
		}
		if existing, found := compMap[canonical]; found {
			line := c.Line
			if line <= 0 {
				line = 1
			}
			res.AddError(c.FilePath, line, "DUPLICATE_COMPONENT_NAME", fmt.Sprintf("duplicate component definition for %q (also defined in %s)", canonical, existing.FilePath))
			continue
		}
		compMap[canonical] = c
	}
	return compMap
}
