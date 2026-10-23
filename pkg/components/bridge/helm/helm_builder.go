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

package helm

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

const (
	// terminationGracePeriodBufferSeconds provides a 5s buffer beyond drain timeout before force killing.
	terminationGracePeriodBufferSeconds = 5
)

// ComponentRef uniquely identifies a component within a Kubernetes cluster by its namespace, name, and optional CR UID.
type ComponentRef struct {
	// Namespace is the Kubernetes namespace containing the component.
	Namespace string `json:"namespace,omitempty"`
	// Name is the unique name of the component within its namespace.
	Name string `json:"name"`
	// UID is the Kubernetes metadata.uid of the Component custom resource incarnation, when known.
	UID string `json:"uid,omitempty"`
}

// String returns the canonical "namespace/name" (or "name" if namespace is omitted) string representation,
// suffixed with "@<uid>" when UID is set.
func (c ComponentRef) String() string {
	base := c.Name
	if c.Namespace != "" {
		base = c.Namespace + "/" + c.Name
	}
	if uid := strings.TrimSpace(c.UID); uid != "" {
		return base + "@" + uid
	}
	return base
}

// HelmComponentOptions defines the configuration values for managing a 1P component via Helm releases.
type HelmComponentOptions struct {
	// Ref uniquely identifies the component (namespace and name).
	Ref ComponentRef
	// ReleaseName is the Helm release name (defaults to "xmanager-cloud").
	ReleaseName string
	// ChartPath specifies an optional explicit filesystem path to the component's Helm chart.
	ChartPath string
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

// BuildHelmValues converts HelmComponentOptions into a structured values map suitable for Helm upgrade.
func BuildHelmValues(opts HelmComponentOptions) map[string]any {
	values := make(map[string]any)
	deploymentKey := ComponentNameToHelmKey(opts.Ref.Name)

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

	defaultEnvImg := os.Getenv("DEFAULT_" + strings.ToUpper(strings.ReplaceAll(opts.Ref.Name, "-", "_")) + "_IMAGE")
	if opts.ImageTag != "" || opts.Parameters["image"] != "" || opts.Parameters["imageRepository"] != "" || defaultEnvImg != "" {
		compValues["image"] = ResolveImage(opts.Ref.Name, opts.ImageTag, opts.Parameters)
	}
	if projID := os.Getenv("PROJECT_ID"); projID != "" {
		compValues["projectId"] = projID
	}
	if opts.DrainTimeoutSeconds > 0 {
		compValues["terminationGracePeriodSeconds"] = int64(opts.DrainTimeoutSeconds) + terminationGracePeriodBufferSeconds
	}

	for k, v := range opts.Parameters {
		if k == "image" || k == "imageRepository" || k == "chartPath" {
			continue
		}
		compValues[k] = parseParamValue(v)
	}

	for k, v := range opts.RawValues {
		compValues[k] = v
	}

	values[deploymentKey] = compValues
	return values
}

// DefaultComponentRepository returns the standard default Google Artifact Registry repository
// for first-party (1P) components in production XMC deployments (e.g. us-docker.pkg.dev/xmc-prod/xmc/<componentName>).
// When deploying to non-prod environments or custom registries, callers supply explicit repository overrides
// via parameters["imageRepository"] or parameters["image"].
func DefaultComponentRepository(componentName string) string {
	return fmt.Sprintf("us-docker.pkg.dev/xmc-prod/xmc/%s", componentName)
}

// ResolveImage resolves the container image following 4-tier precedence:
// 1. Full image override (parameters["image"])
// 2. Custom repository (parameters["imageRepository"]) + tag
// 3. Default repository (DefaultComponentRepository) + tag override (imageTag)
// 4. Default repository + ":latest"
func ResolveImage(componentName string, imageTag string, parameters map[string]string) string {
	if img, ok := parameters["image"]; ok && img != "" {
		return img
	}
	repo := parameters["imageRepository"]
	if repo == "" && (imageTag == "" || imageTag == "0.1.0") {
		if envImg := os.Getenv("DEFAULT_" + strings.ToUpper(strings.ReplaceAll(componentName, "-", "_")) + "_IMAGE"); envImg != "" {
			return envImg
		}
	}
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
	// Standard 1P components like "ingestion-gateway" and "health-service" are intentionally
	// omitted from the known map because toCamelCase(name) + "Deployment" computes
	// "ingestionGatewayDeployment" and "healthServiceDeployment" identically.
	// Only components requiring non-standard key transformations (e.g. "rod-data-provider" -> "rodDeployment",
	// "xmc-bridge" -> "componentsDeployment") need explicit entries in this lookup table.
	known := map[string]string{
		"rod-data-provider": "rodDeployment",
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
		Ref: ComponentRef{
			Namespace: namespace,
			Name:      name,
		},
		ReleaseName: DefaultHelmReleaseName,
		Enabled:     true,
		Parameters:  make(map[string]string),
		RawValues:   make(map[string]any),
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
	for k, v := range spec.GetParameters() {
		opts.Parameters[k] = v
	}
	if cp := opts.Parameters["chartPath"]; cp != "" {
		opts.ChartPath = cp
	}
	if lc := spec.GetLifecycleControl(); lc != nil && lc.GetDrainingTimeout() != nil {
		opts.DrainTimeoutSeconds = int32(lc.GetDrainingTimeout().GetSeconds())
	}
}

func applyUnstructuredSpec(opts *HelmComponentOptions, specMap map[string]any) {
	if en, ok := specMap["enabled"].(bool); ok {
		opts.Enabled = en
	}
	if dt := parseDrainingTimeout(specMap["lifecycleControl"]); dt > 0 {
		opts.DrainTimeoutSeconds = dt
	}
	if rel, ok := specMap["releaseName"].(string); ok && rel != "" {
		opts.ReleaseName = rel
	}
	if tag, ok := specMap["imageTag"].(string); ok && tag != "" {
		opts.ImageTag = tag
	}
	extractParams(specMap["parameters"], opts.Parameters)
	if opts.Parameters["chartPath"] != "" {
		opts.ChartPath = opts.Parameters["chartPath"]
	}
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
