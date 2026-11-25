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

package executionenvironments

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"k8s.io/apimachinery/pkg/util/validation"
	"xmanager_cloud/internal/kubernetes/common/constants"
)

const minTruncHashLength = 10

var reservedMetadataPrefixes = []string{
	"xmc.google.com/",
	"xmc.dev/",
	"kueue.x-k8s.io/",
	"jobset.sigs.k8s.io/",
}

// PlacementConfig defines cluster-administrator accelerator placement defaults
// for the "kubernetes" execution environment.
type PlacementConfig struct {
	GPU AcceleratorPlacement `json:"gpu,omitempty"`
	TPU AcceleratorPlacement `json:"tpu,omitempty"`
}

// AcceleratorPlacement configures scheduling, supplemental device groups, and
// templated Kubernetes metadata for a single accelerator class.
type AcceleratorPlacement struct {
	SchedulerName      string            `json:"schedulerName,omitempty"`
	SupplementalGroups []int64           `json:"supplementalGroups,omitempty"`
	ExclusivePlacement bool              `json:"exclusivePlacement,omitempty"`
	NodeSelector       map[string]string `json:"nodeSelector,omitempty"`
	PodLabels          map[string]string `json:"podLabels,omitempty"`
	PodAnnotations     map[string]string `json:"podAnnotations,omitempty"`
	JobSetLabels       map[string]string `json:"jobSetLabels,omitempty"`
	JobSetAnnotations  map[string]string `json:"jobSetAnnotations,omitempty"`
}

// TemplateValues holds workload coordinates exposed to placement templates.
type TemplateValues struct {
	JobSetName      string
	Namespace       string
	JobName         string
	XID             string
	WID             string
	EID             string
	AcceleratorType string
	Topology        string
	Count           int64
}

type compiledPlacementConfig struct {
	gpu compiledAcceleratorPlacement
	tpu compiledAcceleratorPlacement
}

type compiledAcceleratorPlacement struct {
	schedulerName      string
	supplementalGroups []int64
	exclusivePlacement bool
	nodeSelector       map[string]*template.Template
	podLabels          map[string]*template.Template
	podAnnotations     map[string]*template.Template
	jobSetLabels       map[string]*template.Template
	jobSetAnnotations  map[string]*template.Template
}

// ParsePlacementConfig decodes a JSON placement configuration string.
// An empty or whitespace-only string returns an empty PlacementConfig.
func ParsePlacementConfig(rawJSON string) (PlacementConfig, error) {
	trimmed := strings.TrimSpace(rawJSON)
	if trimmed == "" {
		return PlacementConfig{}, nil
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var cfg PlacementConfig
	if err := dec.Decode(&cfg); err != nil {
		return PlacementConfig{}, fmt.Errorf("failed to parse placement config JSON: %w", err)
	}
	if dec.More() {
		return PlacementConfig{}, fmt.Errorf("unexpected trailing data in placement config JSON")
	}
	return cfg, nil
}

// ExclusiveResourceNames returns the Kubernetes extended resource names whose
// placement profile declares ExclusivePlacement.
func (c PlacementConfig) ExclusiveResourceNames() []string {
	var resources []string
	if c.GPU.ExclusivePlacement {
		resources = append(resources, constants.NvidiaGPUResourceName)
	}
	if c.TPU.ExclusivePlacement {
		resources = append(resources, constants.TPUResourceName)
	}
	return resources
}

func compilePlacementConfig(cfg PlacementConfig) (*compiledPlacementConfig, error) {
	gpu, err := compileAcceleratorPlacement("gpu", cfg.GPU)
	if err != nil {
		return nil, err
	}
	tpu, err := compileAcceleratorPlacement("tpu", cfg.TPU)
	if err != nil {
		return nil, err
	}
	return &compiledPlacementConfig{gpu: gpu, tpu: tpu}, nil
}

func compileAcceleratorPlacement(scope string, p AcceleratorPlacement) (compiledAcceleratorPlacement, error) {
	if err := validateSchedulerAndGroups(scope, p.SchedulerName, p.SupplementalGroups); err != nil {
		return compiledAcceleratorPlacement{}, err
	}
	nodeSelector, err := compileTemplateMap(scope+".nodeSelector", p.NodeSelector, true)
	if err != nil {
		return compiledAcceleratorPlacement{}, err
	}
	podLabels, err := compileTemplateMap(scope+".podLabels", p.PodLabels, true)
	if err != nil {
		return compiledAcceleratorPlacement{}, err
	}
	podAnnotations, err := compileTemplateMap(scope+".podAnnotations", p.PodAnnotations, false)
	if err != nil {
		return compiledAcceleratorPlacement{}, err
	}
	jobSetLabels, err := compileTemplateMap(scope+".jobSetLabels", p.JobSetLabels, true)
	if err != nil {
		return compiledAcceleratorPlacement{}, err
	}
	jobSetAnnotations, err := compileTemplateMap(scope+".jobSetAnnotations", p.JobSetAnnotations, false)
	if err != nil {
		return compiledAcceleratorPlacement{}, err
	}
	groups := make([]int64, len(p.SupplementalGroups))
	copy(groups, p.SupplementalGroups)
	return compiledAcceleratorPlacement{
		schedulerName:      p.SchedulerName,
		supplementalGroups: groups,
		exclusivePlacement: p.ExclusivePlacement,
		nodeSelector:       nodeSelector,
		podLabels:          podLabels,
		podAnnotations:     podAnnotations,
		jobSetLabels:       jobSetLabels,
		jobSetAnnotations:  jobSetAnnotations,
	}, nil
}

func validateSchedulerAndGroups(scope, schedulerName string, groups []int64) error {
	if schedulerName != "" {
		if errs := validation.IsDNS1123Subdomain(schedulerName); len(errs) > 0 {
			return fmt.Errorf("invalid %s.schedulerName %q: %s", scope, schedulerName, strings.Join(errs, "; "))
		}
	}
	for _, gid := range groups {
		if gid <= 0 {
			return fmt.Errorf("invalid %s.supplementalGroups entry %d: supplemental group ID must be positive", scope, gid)
		}
	}
	return nil
}

func isReservedMetadataKey(key string) bool {
	if key == constants.ManagedByLabel || key == constants.AutoscalerSafeToEvictAnnotation {
		return true
	}
	for _, prefix := range reservedMetadataPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func compileTemplateMap(field string, entries map[string]string, isLabel bool) (map[string]*template.Template, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	compiled := make(map[string]*template.Template, len(entries))
	probeValues := sampleTemplateValues()
	for _, key := range keys {
		if errs := validation.IsQualifiedName(key); len(errs) > 0 {
			return nil, fmt.Errorf("invalid key %q in %s: %s", key, field, strings.Join(errs, "; "))
		}
		if isReservedMetadataKey(key) {
			return nil, fmt.Errorf("reserved system metadata key %q cannot be configured in %s", key, field)
		}
		tmpl, err := parseMetadataTemplate(field, key, entries[key])
		if err != nil {
			return nil, err
		}
		if _, err := executeTemplate(tmpl, probeValues, isLabel, field, key); err != nil {
			return nil, err
		}
		compiled[key] = tmpl
	}
	return compiled, nil
}

func parseMetadataTemplate(field, key, raw string) (*template.Template, error) {
	tmpl, err := template.New(field + ":" + key).
		Option("missingkey=error").
		Funcs(template.FuncMap{"truncHash": truncHash}).
		Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template for %s[%q]: %w", field, key, err)
	}
	return tmpl, nil
}

func sampleTemplateValues() TemplateValues {
	return TemplateValues{
		JobSetName:      "xmc-operation-1-1",
		Namespace:       "default",
		JobName:         "worker",
		XID:             "1",
		WID:             "1",
		EID:             "1",
		AcceleratorType: "sample-type",
		Topology:        "2x2",
		Count:           1,
	}
}

// truncHash bounds value to maxLen characters. Values exceeding maxLen are
// truncated on a UTF-8 rune boundary and suffixed with a deterministic
// 8-character SHA-256 hex digest.
func truncHash(maxLen int, value string) (string, error) {
	if maxLen < minTruncHashLength {
		return "", fmt.Errorf("truncHash maxLen must be at least %d, got %d", minTruncHashLength, maxLen)
	}
	runes := []rune(value)
	if len(runes) <= maxLen {
		return value, nil
	}
	sum := sha256.Sum256([]byte(value))
	suffix := hex.EncodeToString(sum[:])[:8]
	prefixLen := maxLen - len(suffix) - 1
	return strings.TrimRight(string(runes[:prefixLen]), "-") + "-" + suffix, nil
}

func renderTemplateMap(templates map[string]*template.Template, vals TemplateValues, isLabel bool, field string) (map[string]string, error) {
	if len(templates) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(templates))
	for k := range templates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	rendered := make(map[string]string, len(templates))
	for _, key := range keys {
		out, err := executeTemplate(templates[key], vals, isLabel, field, key)
		if err != nil {
			return nil, err
		}
		rendered[key] = out
	}
	return rendered, nil
}

func executeTemplate(tmpl *template.Template, vals TemplateValues, isLabel bool, field, key string) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vals); err != nil {
		return "", fmt.Errorf("failed to render template for %s[%q]: %w", field, key, err)
	}
	out := buf.String()
	if isLabel {
		if errs := validation.IsValidLabelValue(out); len(errs) > 0 {
			return "", fmt.Errorf("rendered value %q for %s[%q] is not a valid Kubernetes label value: %s", out, field, key, strings.Join(errs, "; "))
		}
	}
	return out, nil
}
