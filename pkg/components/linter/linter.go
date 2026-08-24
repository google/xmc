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
)

// Linter executes multi-pass static validation against manifest directories.
type Linter struct{}

// NewLinter creates a new static linter instance.
func NewLinter() *Linter {
	return &Linter{}
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
	_, _, err = l.collectManifests(dir, res)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (l *Linter) collectManifests(dir string, res *ValidationResult) ([]*RawManifest, []*ComponentManifest, error) {
	var rawManifests []*RawManifest
	var compManifests []*ComponentManifest

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			res.AddError(path, 1, "FILE_READ_ERROR", fmt.Sprintf("failed to read file: %v", readErr))
			return nil
		}

		raws, comps := l.parseManifestFile(path, data, res)
		rawManifests = append(rawManifests, raws...)
		compManifests = append(compManifests, comps...)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to scan manifests in %q: %w", dir, err)
	}
	return rawManifests, compManifests, nil
}

func (l *Linter) parseManifestFile(path string, data []byte, res *ValidationResult) ([]*RawManifest, []*ComponentManifest) {
	var rawManifests []*RawManifest
	var compManifests []*ComponentManifest

	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			res.AddError(path, 1, "YAML_SYNTAX", fmt.Sprintf("failed to parse YAML in %s: %v", path, err))
			break
		}
		if node.Kind == yaml.DocumentNode && (len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode) {
			continue
		}
		line := node.Line
		if line <= 0 {
			line = 1
		}

		var raw RawManifest
		if err := node.Decode(&raw); err != nil {
			res.AddError(path, line, "SCHEMA_ERROR", fmt.Sprintf("failed to decode manifest in %s: %v", path, err))
			continue
		}
		if raw.APIVersion == "" && raw.Kind == "" && raw.Metadata.Name == "" {
			continue
		}
		raw.FilePath = path
		raw.Line = line
		rawManifests = append(rawManifests, &raw)

		if strings.EqualFold(raw.Kind, "Component") && strings.HasPrefix(raw.APIVersion, "components.xmc.dev/") {
			var comp ComponentManifest
			if err := node.Decode(&comp); err != nil {
				res.AddError(path, line, "SCHEMA_ERROR", fmt.Sprintf("failed to decode Component in %s: %v", path, err))
				continue
			}
			comp.FilePath = path
			comp.Line = line
			compManifests = append(compManifests, &comp)
		}
	}
	return rawManifests, compManifests
}
