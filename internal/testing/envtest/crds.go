// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package envtest provides test harness and CRD loading utilities for Kubernetes envtest.
package envtest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bazelbuild/rules_go/go/runfiles"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// resolveRunfilePath attempts to locate a file in Bazel runfiles with fallback paths.
func resolveRunfilePath(rpath string, fallbacks ...string) (string, error) {
	if r, err := runfiles.New(); err == nil {
		if resolved, err := r.Rlocation(rpath); err == nil && resolved != "" {
			if _, err := os.Stat(resolved); err == nil {
				return resolved, nil
			}
		}
	}

	for _, fb := range fallbacks {
		if _, err := os.Stat(fb); err == nil {
			return fb, nil
		}
		if wsDir := os.Getenv("BUILD_WORKSPACE_DIRECTORY"); wsDir != "" {
			fullPath := filepath.Join(wsDir, fb)
			if _, err := os.Stat(fullPath); err == nil {
				return fullPath, nil
			}
		}
	}

	return "", fmt.Errorf("file not found in runfiles (%s) or fallback paths", rpath)
}

// StripHelmDirectives removes Helm template directives ({{ ... }}) from YAML content.
func StripHelmDirectives(data []byte) []byte {
	var buf bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") {
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// LoadComponentCRD loads and decodes the 1P Component CRD from deploy/helm/templates/components/crd.yaml.
func LoadComponentCRD() (*apiextensionsv1.CustomResourceDefinition, error) {
	path, err := resolveRunfilePath(
		"xmanager_cloud/deploy/helm/templates/components/crd.yaml",
		"deploy/helm/templates/components/crd.yaml",
		"../../deploy/helm/templates/components/crd.yaml",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to find Component CRD manifest: %w", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read Component CRD file at %s: %w", path, err)
	}

	cleaned := StripHelmDirectives(raw)
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(cleaned), 4096)
	var crd apiextensionsv1.CustomResourceDefinition
	if err := decoder.Decode(&crd); err != nil {
		return nil, fmt.Errorf("failed to decode Component CRD: %w", err)
	}

	return &crd, nil
}

// parseCRDsFromManifest decodes all CustomResourceDefinition objects from multi-document YAML.
func parseCRDsFromManifest(data []byte) ([]*apiextensionsv1.CustomResourceDefinition, error) {
	var crds []*apiextensionsv1.CustomResourceDefinition
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)

	for {
		var raw map[string]any
		if err := decoder.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("failed to parse YAML document: %w", err)
		}
		if raw == nil {
			continue
		}

		u := &unstructured.Unstructured{Object: raw}
		if u.GetKind() != "CustomResourceDefinition" {
			continue
		}

		jsonBytes, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to re-encode CRD to JSON: %w", err)
		}

		var crd apiextensionsv1.CustomResourceDefinition
		if err := json.Unmarshal(jsonBytes, &crd); err != nil {
			return nil, fmt.Errorf("failed to unmarshal CRD %q: %w", u.GetName(), err)
		}

		crds = append(crds, &crd)
	}

	return crds, nil
}

var jobSetCELBugRegex = regexp.MustCompile(`self\.templates\.all\(t,\s*!has\(t\.metadata\.namespace\)[^=]*==\s*0\)`)

// LoadJobSetCRDs loads JobSet CRDs from upstream release manifests.
func LoadJobSetCRDs() ([]*apiextensionsv1.CustomResourceDefinition, error) {
	path, err := resolveRunfilePath(
		"jobset_manifests/manifests.yaml",
		"external/jobset_manifests/manifests.yaml",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to find JobSet manifests: %w", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read JobSet manifests from %s: %w", path, err)
	}

	// Upstream JobSet v0.12.0 has a known CEL validation bug on Kubernetes 1.30+ where t.metadata.namespace
	// is referenced in CEL but namespace is not declared in the template metadata schema.
	raw = jobSetCELBugRegex.ReplaceAll(raw, []byte("'true'"))

	return parseCRDsFromManifest(raw)
}

// LoadKueueCRDs loads Kueue CRDs from upstream release manifests.
func LoadKueueCRDs() ([]*apiextensionsv1.CustomResourceDefinition, error) {
	path, err := resolveRunfilePath(
		"kueue_manifests/manifests.yaml",
		"external/kueue_manifests/manifests.yaml",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to find Kueue manifests: %w", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read Kueue manifests from %s: %w", path, err)
	}

	return parseCRDsFromManifest(raw)
}

// LoadAllCRDs loads Component CRD, JobSet CRDs, and Kueue CRDs.
func LoadAllCRDs() ([]*apiextensionsv1.CustomResourceDefinition, error) {
	var allCRDs []*apiextensionsv1.CustomResourceDefinition

	componentCRD, err := LoadComponentCRD()
	if err != nil {
		return nil, fmt.Errorf("failed to load Component CRD: %w", err)
	}
	allCRDs = append(allCRDs, componentCRD)

	jobsetCRDs, err := LoadJobSetCRDs()
	if err != nil {
		return nil, fmt.Errorf("failed to load JobSet CRDs: %w", err)
	}
	allCRDs = append(allCRDs, jobsetCRDs...)

	kueueCRDs, err := LoadKueueCRDs()
	if err != nil {
		return nil, fmt.Errorf("failed to load Kueue CRDs: %w", err)
	}
	allCRDs = append(allCRDs, kueueCRDs...)

	return allCRDs, nil
}
