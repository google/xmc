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
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
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
	// ImageTagFromChartVersion indicates ImageTag was populated from the Component chart version
	// (spec.version) rather than an explicit spec.imageTag override.
	ImageTagFromChartVersion bool
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
	if ns := strings.TrimSpace(opts.Ref.Namespace); ns != "" {
		values["namespace"] = ns
	}

	defaultEnvImg := defaultComponentEnvImage(opts.Ref.Name)
	effectiveTag := strings.TrimSpace(opts.ImageTag)
	if opts.ImageTagFromChartVersion {
		effectiveTag = ""
	}
	if effectiveTag != "" {
		values["global"] = map[string]any{"imageTag": effectiveTag}
	}

	compValues := buildComponentDeploymentValues(opts, effectiveTag, defaultEnvImg)
	values[ComponentNameToHelmKey(opts.Ref.Name)] = compValues
	return values
}

func buildComponentDeploymentValues(opts HelmComponentOptions, effectiveTag, defaultEnvImg string) map[string]any {
	compValues := map[string]any{"enabled": opts.Enabled}
	if opts.DrainTimeoutSeconds > 0 {
		compValues["drainTimeoutSeconds"] = opts.DrainTimeoutSeconds
		compValues["terminationGracePeriodSeconds"] = int64(opts.DrainTimeoutSeconds) + terminationGracePeriodBufferSeconds
	}
	// parameters["image"] is a full image ref (repo:tag or repo@digest) used verbatim;
	// parameters["imageRepository"] is a tagless registry/repo path combined with effectiveTag.
	hasImgOverride := strings.TrimSpace(opts.Parameters["image"]) != "" || strings.TrimSpace(opts.Parameters["imageRepository"]) != ""
	if effectiveTag != "" || hasImgOverride || defaultEnvImg != "" {
		compValues["image"] = ResolveImage(opts.Ref.Name, effectiveTag, opts.Parameters)
	}
	if projID := strings.TrimSpace(os.Getenv("PROJECT_ID")); projID != "" {
		compValues["projectId"] = projID
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
	return compValues
}

func defaultComponentEnvImageVar(componentName string) string {
	return "DEFAULT_" + strings.ToUpper(strings.ReplaceAll(componentName, "-", "_")) + "_IMAGE"
}

func defaultComponentEnvImage(componentName string) string {
	return strings.TrimSpace(os.Getenv(defaultComponentEnvImageVar(componentName)))
}

// warnMissingEnvFallbacks logs structured warnings during 1P component actuation when bridge environment
// defaults (PROJECT_ID or DEFAULT_<COMPONENT>_IMAGE) are unset and the Component spec provides no override.
func warnMissingEnvFallbacks(ctx context.Context, ref ComponentRef, opts HelmComponentOptions, values map[string]any) {
	if !opts.Enabled {
		return
	}
	compValues, _ := values[ComponentNameToHelmKey(ref.Name)].(map[string]any)
	projVal, hasProj := compValues["projectId"]
	if !hasProj || projVal == nil || strings.TrimSpace(fmt.Sprint(projVal)) == "" {
		logging.Warn(ctx, "PROJECT_ID is not set and component has no projectId override; deploying with empty projectId",
			"name", ref.Name, "namespace", ref.Namespace)
	}
	hasParamImg := strings.TrimSpace(opts.Parameters["image"]) != "" || strings.TrimSpace(opts.Parameters["imageRepository"]) != ""
	if defaultComponentEnvImage(ref.Name) == "" && !hasParamImg && !hasNonEmptyRawValue(opts.RawValues, "image", "imageRepository") {
		logging.Warn(ctx, "Default component image env var is not set and component has no image override; falling back to default chart repository",
			"name", ref.Name, "namespace", ref.Namespace, "envVar", defaultComponentEnvImageVar(ref.Name), "defaultRepository", DefaultComponentRepository(ref.Name))
	}
}

func hasNonEmptyRawValue(raw map[string]any, keys ...string) bool {
	for _, k := range keys {
		if s, ok := raw[k].(string); ok && strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

// repositoryFromImageRef strips any @digest or :tag suffix from a container image reference.
func repositoryFromImageRef(imageRef string) string {
	ref := strings.TrimSpace(imageRef)
	if idx := strings.Index(ref, "@"); idx >= 0 {
		ref = ref[:idx]
	}
	lastSlash := strings.LastIndex(ref, "/")
	if lastColon := strings.LastIndex(ref, ":"); lastColon > lastSlash {
		ref = ref[:lastColon]
	}
	return ref
}

// DefaultComponentRepository returns the standard default Google Artifact Registry repository
// for first-party (1P) components in production XMC deployments (e.g. us-docker.pkg.dev/xmc-prod/xmc/<componentName>).
// When deploying to non-prod environments or custom registries, callers supply explicit repository overrides
// via parameters["imageRepository"] or parameters["image"].
func DefaultComponentRepository(componentName string) string {
	return fmt.Sprintf("us-docker.pkg.dev/xmc-prod/xmc/%s", componentName)
}

// ResolveImage resolves the container image following precedence:
//  1. Full image override (parameters["image"])
//  2. Custom repository (parameters["imageRepository"]) + tag
//  3. Environment-configured default image (DEFAULT_<COMPONENT>_IMAGE) verbatim when imageTag is empty,
//     or its repository + explicit imageTag override when imageTag is set
//  4. Default repository (DefaultComponentRepository) + imageTag (or ":latest")
func ResolveImage(componentName string, imageTag string, parameters map[string]string) string {
	if img := strings.TrimSpace(parameters["image"]); img != "" {
		return img
	}
	repo := strings.TrimSpace(parameters["imageRepository"])
	tag := strings.TrimSpace(imageTag)
	envImg := defaultComponentEnvImage(componentName)
	if repo == "" && tag == "" && envImg != "" {
		return envImg
	}
	if repo == "" && envImg != "" {
		repo = repositoryFromImageRef(envImg)
	}
	if repo == "" {
		repo = DefaultComponentRepository(componentName)
	}
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
			Namespace: strings.TrimSpace(namespace),
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
	if ver := strings.TrimSpace(spec.GetVersion()); ver != "" {
		opts.ImageTag = ver
	}
	for k, v := range spec.GetParameters() {
		opts.Parameters[k] = v
	}
	if cp := strings.TrimSpace(opts.Parameters["chartPath"]); cp != "" {
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
	if rel, ok := specMap["releaseName"].(string); ok && strings.TrimSpace(rel) != "" {
		opts.ReleaseName = strings.TrimSpace(rel)
	}
	applyVersionAndImageTag(opts, specMap)
	extractParams(specMap["parameters"], opts.Parameters)
	if cp := strings.TrimSpace(opts.Parameters["chartPath"]); cp != "" {
		opts.ChartPath = cp
	}
	extractRawValues(specMap["values"], opts.RawValues)
}

func applyVersionAndImageTag(opts *HelmComponentOptions, specMap map[string]any) {
	chartVer, _ := specMap["chartVersion"].(string)
	chartVer = strings.TrimSpace(chartVer)
	if ver, ok := specMap["version"].(string); ok && strings.TrimSpace(ver) != "" && opts.ImageTag == "" {
		opts.ImageTag = strings.TrimSpace(ver)
	}
	if chartVer != "" && opts.ImageTag == chartVer {
		opts.ImageTagFromChartVersion = true
	}
	if tag, ok := specMap["imageTag"].(string); ok && strings.TrimSpace(tag) != "" {
		opts.ImageTag = strings.TrimSpace(tag)
		opts.ImageTagFromChartVersion = false
	}
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
