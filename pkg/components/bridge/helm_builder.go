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

package bridge

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// HelmComponentOptions defines the configuration values for managing a 1P component via Helm releases.
type HelmComponentOptions struct {
	// ComponentName is the name of the component (e.g., "ingestion-gateway").
	ComponentName string
	// Namespace is the target release namespace.
	Namespace string
	// ReleaseName is the Helm release name (defaults to "xmanager-cloud").
	ReleaseName string
	// Enabled indicates whether the component should be enabled in the Helm release.
	Enabled bool
	// ImageTag specifies an optional container image tag override.
	ImageTag string
	// DrainTimeoutSeconds specifies graceful termination timeout in seconds.
	DrainTimeoutSeconds int32
	// Parameters contains key-value string configuration parameters.
	Parameters map[string]string
	// RawValues contains structured Helm values overrides if provided.
	RawValues map[string]any
}

// HelmManager defines the contract for provisioning and managing 1P component workloads via Helm releases.
type HelmManager interface {
	// Ensure1PComponent sets the component's enablement flag and values in the Helm release and triggers an upgrade.
	Ensure1PComponent(ctx context.Context, namespace string, componentName string, opts HelmComponentOptions) error
	// Delete1PComponent sets the component's enablement flag to false in the Helm release and triggers an upgrade.
	Delete1PComponent(ctx context.Context, namespace string, componentName string) error
	// Orphan1PComponent retains the component values while detaching bridge ownership.
	Orphan1PComponent(ctx context.Context, namespace string, componentName string) error
	// IsComponentActive checks if the component is currently marked enabled in the Helm release.
	IsComponentActive(componentName string) bool
}

// BuildHelmValues converts HelmComponentOptions into a structured values map suitable for Helm upgrade.
func BuildHelmValues(opts HelmComponentOptions) map[string]any {
	values := make(map[string]any)
	deploymentKey := ComponentNameToHelmKey(opts.ComponentName)

	compValues := make(map[string]any)
	compValues["enabled"] = opts.Enabled

	if opts.DrainTimeoutSeconds > 0 {
		compValues["drainTimeoutSeconds"] = opts.DrainTimeoutSeconds
	}

	if opts.ImageTag != "" {
		values["global"] = map[string]any{
			"imageTag": opts.ImageTag,
		}
	}

	if opts.ImageTag != "" || len(opts.Parameters) > 0 {
		compValues["image"] = ResolveImage(opts.ComponentName, opts.ImageTag, opts.Parameters)
	}
	if opts.DrainTimeoutSeconds > 0 {
		compValues["terminationGracePeriodSeconds"] = int64(opts.DrainTimeoutSeconds) + 5
	}

	for k, v := range opts.Parameters {
		compValues[k] = parseParamValue(v)
	}

	for k, v := range opts.RawValues {
		compValues[k] = v
	}

	values[deploymentKey] = compValues
	return values
}

// DefaultComponentRepository returns standard default image repository for known components.
func DefaultComponentRepository(componentName string) string {
	switch componentName {
	case "ingestion-gateway":
		return "us-docker.pkg.dev/xmc-prod/xmc/ingestion-gateway"
	case "rod":
		return "us-docker.pkg.dev/xmc-prod/xmc/rod-data-provider"
	case "health-service":
		return "us-docker.pkg.dev/xmc-prod/xmc/health-service"
	default:
		return fmt.Sprintf("us-docker.pkg.dev/xmc-prod/xmc/%s", componentName)
	}
}

// ResolveImage resolves the container image following 4-tier precedence:
// 1. Full image override (parameters["image"])
// 2. Custom repository (parameters["imageRepository"]) + tag
// 3. Default repository + tag override (imageTag)
// 4. Default repository + ":latest"
func ResolveImage(componentName string, imageTag string, parameters map[string]string) string {
	if img, ok := parameters["image"]; ok && img != "" {
		return img
	}
	repo := parameters["imageRepository"]
	if repo == "" {
		repo = DefaultComponentRepository(componentName)
	}
	tag := imageTag
	if tag == "" {
		tag = "latest"
	}
	return fmt.Sprintf("%s:%s", repo, tag)
}

// ComponentNameToHelmKey maps a component name to its standard Helm values deployment section key.
func ComponentNameToHelmKey(name string) string {
	known := map[string]string{
		"ingestion-gateway": "ingestionGatewayDeployment",
		"rod":               "rodDeployment",
		"health-service":    "healthServiceDeployment",
		"metrics":           "metrics",
		"xmc-bridge":        "componentsDeployment",
	}
	if k, ok := known[name]; ok {
		return k
	}
	return toCamelCase(name) + "Deployment"
}

func parseParamValue(v string) any {
	if i, err := strconv.ParseInt(v, 10, 64); err == nil {
		return i
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return v
}

func toCamelCase(s string) string {
	var b strings.Builder
	capitalize := false
	for i, r := range s {
		if r == '-' || r == '_' {
			capitalize = true
			continue
		}
		if capitalize {
			b.WriteString(strings.ToUpper(string(r)))
			capitalize = false
		} else if i == 0 {
			b.WriteString(strings.ToLower(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ParseHelmComponentOptions extracts HelmComponentOptions from an unstructured spec map and ComponentSpec.
func ParseHelmComponentOptions(name, namespace string, specObj any, spec *componentspb.ComponentSpec) HelmComponentOptions {
	opts := HelmComponentOptions{
		ComponentName: name,
		Namespace:     namespace,
		Enabled:       true,
		Parameters:    make(map[string]string),
		RawValues:     make(map[string]any),
	}
	applyProtoSpec(&opts, spec)
	if specMap, ok := specObj.(map[string]any); ok && specMap != nil {
		applyUnstructuredSpec(&opts, specMap)
	}
	return opts
}

func applyProtoSpec(opts *HelmComponentOptions, spec *componentspb.ComponentSpec) {
	if spec == nil {
		return
	}
	opts.ImageTag = spec.GetVersion()
	if lc := spec.GetLifecycleControl(); lc != nil && lc.GetDrainingTimeout() != nil {
		opts.DrainTimeoutSeconds = int32(lc.GetDrainingTimeout().GetSeconds())
	}
}

func applyUnstructuredSpec(opts *HelmComponentOptions, specMap map[string]any) {
	if dt := parseDrainingTimeout(specMap["lifecycleControl"]); dt > 0 {
		opts.DrainTimeoutSeconds = dt
	}
	if rel := extractStr(specMap, "releaseName"); rel != "" {
		opts.ReleaseName = rel
	}
	if tag := extractStr(specMap, "imageTag"); tag != "" {
		opts.ImageTag = tag
	}
	extractParams(specMap["parameters"], opts.Parameters)
	extractRawValues(specMap["values"], opts.RawValues)
}

func extractParams(raw any, target map[string]string) {
	paramsMap, ok := raw.(map[string]any)
	if !ok || paramsMap == nil {
		return
	}
	for k, v := range paramsMap {
		if s, ok := v.(string); ok {
			target[k] = s
		} else if v != nil {
			target[k] = fmt.Sprintf("%v", v)
		}
	}
}

func extractRawValues(raw any, target map[string]any) {
	valuesMap, ok := raw.(map[string]any)
	if !ok || valuesMap == nil {
		return
	}
	for k, v := range valuesMap {
		target[k] = v
	}
}

func parseDrainingTimeout(lcObj any) int32 {
	lc, ok := lcObj.(map[string]any)
	if !ok || lc == nil {
		return 0
	}
	switch v := lc["drainingTimeout"].(type) {
	case string:
		if d, err := time.ParseDuration(v); err == nil {
			return int32(d.Seconds())
		}
	case float64:
		return int32(v)
	case int64:
		return int32(v)
	case int:
		return int32(v)
	}
	return 0
}

// FakeHelmManager provides an in-memory double of HelmManager for unit tests.
type FakeHelmManager struct {
	mu               sync.Mutex
	ActiveComponents map[string]bool
	AppliedValues    map[string]map[string]any
	EnsureErr        error
	DeleteErr        error
}

// NewFakeHelmManager creates an initialized FakeHelmManager.
func NewFakeHelmManager() *FakeHelmManager {
	return &FakeHelmManager{
		ActiveComponents: make(map[string]bool),
		AppliedValues:    make(map[string]map[string]any),
	}
}

// Ensure1PComponent records component enablement and synthesized Helm values.
func (f *FakeHelmManager) Ensure1PComponent(ctx context.Context, namespace string, componentName string, opts HelmComponentOptions) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.EnsureErr != nil {
		return f.EnsureErr
	}
	if f.ActiveComponents == nil {
		f.ActiveComponents = make(map[string]bool)
	}
	if f.AppliedValues == nil {
		f.AppliedValues = make(map[string]map[string]any)
	}
	f.ActiveComponents[componentName] = opts.Enabled
	f.AppliedValues[componentName] = BuildHelmValues(opts)
	return nil
}

// Delete1PComponent records component disabling in memory.
func (f *FakeHelmManager) Delete1PComponent(ctx context.Context, namespace string, componentName string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	if f.ActiveComponents != nil {
		delete(f.ActiveComponents, componentName)
	}
	if f.AppliedValues != nil {
		delete(f.AppliedValues, componentName)
	}
	return nil
}

// Orphan1PComponent detaches tracking without removing values.
func (f *FakeHelmManager) Orphan1PComponent(ctx context.Context, namespace string, componentName string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	if f.ActiveComponents != nil {
		delete(f.ActiveComponents, componentName)
	}
	return nil
}

// IsComponentActive checks whether the component is recorded as active.
func (f *FakeHelmManager) IsComponentActive(componentName string) bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ActiveComponents[componentName]
}
