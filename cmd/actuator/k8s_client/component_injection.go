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

package k8sclient

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/unstructured/unstructured"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/schema/schema"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/types/types"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/logging"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/pkg/components/crreader"
)

// componentAccess encapsulates resolved access configurations from a component custom resource.
type componentAccess struct {
	envVars            map[string]string
	secretEnvVars      map[string]string
	secretVolumeMounts map[string]string
}

// evaluateComponentSnapshots discovers required component snapshots, evaluates placement/drift, and injects environment variables.
func (c *Client) evaluateComponentSnapshots(ctx context.Context, workUnit *esspb.WorkUnit, namespace string, jobSet *jobsetv1alpha2.JobSet) error {
	snapshots, err := parseComponentSnapshotsAnnotation(workUnit, jobSet)
	if err != nil || len(snapshots) == 0 {
		return err
	}

	if !c.RuntimeConfig.EnableComponents {
		return fmt.Errorf("components feature is disabled on this cluster (enable_components=false), cannot satisfy component-snapshots annotation")
	}

	compNames := make([]string, 0, len(snapshots))
	for name := range snapshots {
		compNames = append(compNames, name)
	}
	sort.Strings(compNames)

	logging.Debug(ctx, "Evaluating component snapshots for JobSet", "components", compNames)

	for _, compName := range compNames {
		lockedGen := snapshots[compName]
		comp, err := c.getComponent(ctx, compName, namespace)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get component %q: %w", compName, err)
			}
			return apierrors.NewServiceUnavailable(fmt.Sprintf("required component %q not found: hydration lag or missing CR: %v", compName, err))
		}

		if err := evaluateComponentPlacement(ctx, compName, lockedGen, comp, jobSet); err != nil {
			return err
		}
		access, err := extractComponentAccess(comp)
		if err != nil {
			return fmt.Errorf("failed to extract access configuration for component %q: %w", compName, err)
		}
		injectAccessIntoJobSet(jobSet, access)
		logging.Debug(ctx, "Injected component access configurations into JobSet",
			"component", compName,
			"lockedGeneration", lockedGen,
			"plainEnvVars", len(access.envVars),
			"secretEnvVars", len(access.secretEnvVars),
			"secretVolumes", len(access.secretVolumeMounts),
		)
	}
	return nil
}

// parseComponentSnapshotsAnnotation extracts and unmarshals component snapshots from JobSet annotations.
func parseComponentSnapshotsAnnotation(workUnit *esspb.WorkUnit, jobSet *jobsetv1alpha2.JobSet) (map[string]int64, error) {
	ann := ""
	if jobset := workUnit.GetExecutable().GetJobset(); jobset != nil {
		ann = jobset.GetAnnotations()[constants.ComponentSnapshotsAnnotation]
	}
	if ann == "" && jobSet != nil {
		ann = jobSet.Annotations[constants.ComponentSnapshotsAnnotation]
	}
	if ann == "" {
		return nil, nil
	}
	var snapshots map[string]int64
	if err := json.Unmarshal([]byte(ann), &snapshots); err != nil {
		return nil, fmt.Errorf("failed to parse %s annotation %q: %w", constants.ComponentSnapshotsAnnotation, ann, err)
	}
	return snapshots, nil
}

// evaluateComponentPlacement validates enablement, health, and drift for a required component.
func evaluateComponentPlacement(ctx context.Context, compName string, lockedGen int64, comp *unstructured.Unstructured, jobSet *jobsetv1alpha2.JobSet) error {
	if crreader.IsDisabled(comp) {
		return apierrors.NewServiceUnavailable(fmt.Sprintf("required component %q is disabled", compName))
	}

	if healthy, reason := evaluateComponentHealth(comp); !healthy {
		return apierrors.NewServiceUnavailable(fmt.Sprintf("required component %q is unhealthy: %s", compName, reason))
	}

	checkAndRecordComponentDrift(ctx, compName, lockedGen, comp, jobSet)
	return nil
}

// checkAndRecordComponentDrift records a warning annotation if the CMS component generation in the
// Component status does not match the locked generation.
//
// TODO(components): also compare the ComponentId, so a snapshot locked on an earlier incarnation
// at the same generation is reported as drift (needs the bridge to persist status.componentId).
func checkAndRecordComponentDrift(ctx context.Context, compName string, lockedGen int64, comp *unstructured.Unstructured, jobSet *jobsetv1alpha2.JobSet) {
	if lockedGen <= 0 {
		return
	}
	componentGen := extractComponentGeneration(comp)
	if componentGen == lockedGen {
		return
	}
	logging.Warn(ctx, "Component generation drift detected", "component", compName, "componentGeneration", componentGen, "lockedGeneration", lockedGen)
	addDriftWarningAnnotation(jobSet, fmt.Sprintf("component %q generation mismatch: component generation %d, locked %d", compName, componentGen, lockedGen))
}

// addDriftWarningAnnotation appends or sets the component drift warning annotation on the JobSet.
func addDriftWarningAnnotation(jobSet *jobsetv1alpha2.JobSet, details string) {
	if jobSet == nil {
		return
	}
	if jobSet.Annotations == nil {
		jobSet.Annotations = make(map[string]string)
	}
	if existing := jobSet.Annotations[constants.ComponentDriftWarningAnnotation]; existing != "" {
		details = existing + "; " + details
	}
	jobSet.Annotations[constants.ComponentDriftWarningAnnotation] = details
}

// getComponent retrieves the Component custom resource from the target namespace or falls back to "xmc".
func (c *Client) getComponent(ctx context.Context, name, targetNamespace string) (*unstructured.Unstructured, error) {
	comp, err := c.fetchComponentResource(ctx, name, targetNamespace)
	if err == nil || !apierrors.IsNotFound(err) || targetNamespace == constants.DefaultComponentsNamespace {
		return comp, err
	}
	logging.Debug(ctx, "Component not found in target namespace, falling back to default components namespace",
		"component", name,
		"targetNamespace", targetNamespace,
		"fallbackNamespace", constants.DefaultComponentsNamespace,
	)
	return c.fetchComponentResource(ctx, name, constants.DefaultComponentsNamespace)
}

// fetchComponentResource fetches a single Component custom resource by namespace and name.
func (c *Client) fetchComponentResource(ctx context.Context, name, namespace string) (*unstructured.Unstructured, error) {
	if c.ManagerClient != nil {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   constants.ComponentCRDGroup,
			Version: constants.ComponentCRDVersion,
			Kind:    constants.ComponentCRDKind,
		})
		if err := c.ManagerClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, u); err != nil {
			return nil, err
		}
		return u, nil
	}
	if c.dynamicClient != nil {
		return c.dynamicClient.Resource(crreader.ComponentGVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	}
	return nil, apierrors.NewServiceUnavailable("no kubernetes client available to fetch component")
}

// evaluateComponentHealth checks whether the component is fully initialized and healthy (Ready=True, Degraded!=True).
func evaluateComponentHealth(comp *unstructured.Unstructured) (bool, string) {
	if comp == nil || comp.Object == nil {
		return true, ""
	}
	conditions, ok, err := unstructured.NestedSlice(comp.Object, "status", "conditions")
	if err != nil || !ok || len(conditions) == 0 {
		return false, "component conditions are uninitialized"
	}
	hasReady := false
	for _, c := range conditions {
		if cMap, ok := c.(map[string]any); ok && cMap["type"] == "Ready" {
			hasReady = true
		}
		if healthy, reason := checkConditionHealth(c); !healthy {
			return false, reason
		}
	}
	if !hasReady {
		return false, "Ready condition is missing"
	}
	return true, ""
}

func checkConditionHealth(c any) (bool, string) {
	cMap, ok := c.(map[string]any)
	if !ok {
		return true, ""
	}
	cType, _ := cMap["type"].(string)
	cStatus, _ := cMap["status"].(string)
	reason, _ := cMap["reason"].(string)
	if reason == "" {
		reason, _ = cMap["message"].(string)
	}
	if cType == "Ready" && cStatus != "True" {
		if reason == "" {
			reason = fmt.Sprintf("Ready condition is %s", cStatus)
		}
		return false, reason
	}
	if cType == "Degraded" && cStatus == "True" {
		if reason == "" {
			reason = "Degraded condition is True"
		}
		return false, reason
	}
	return true, ""
}

// extractComponentGeneration returns the CMS component generation the Component status describes:
// status.componentGeneration, or status.observedGeneration when a bridge that predates
// componentGeneration wrote the status.
func extractComponentGeneration(comp *unstructured.Unstructured) int64 {
	if gen := extractStatusInt(comp, "componentGeneration"); gen > 0 {
		return gen
	}
	return extractStatusInt(comp, "observedGeneration")
}

// extractStatusInt retrieves the integer status field, supporting multiple numeric formats
// including json.Number, or returns 0 when it is absent or not a number.
func extractStatusInt(comp *unstructured.Unstructured, field string) int64 {
	if comp == nil || comp.Object == nil {
		return 0
	}
	val, ok, err := unstructured.NestedFieldNoCopy(comp.Object, "status", field)
	if err != nil || !ok {
		return 0
	}
	switch val := val.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return i
		}
	case string:
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
	}
	return 0
}

// extractComponentAccess parses access metadata from status.access of the Component CR.
func extractComponentAccess(comp *unstructured.Unstructured) (componentAccess, error) {
	if comp == nil || comp.Object == nil {
		return componentAccess{}, nil
	}
	access, ok, err := unstructured.NestedMap(comp.Object, "status", "access")
	if err != nil {
		return componentAccess{}, fmt.Errorf("failed to parse status.access: %w", err)
	}
	if !ok || access == nil {
		return componentAccess{}, nil
	}
	return componentAccess{
		envVars:            extractStringMap(access["envVars"]),
		secretEnvVars:      extractStringMap(access["secretEnvVars"]),
		secretVolumeMounts: extractStringMap(access["secretVolumeMounts"]),
	}, nil
}

// extractStringMap converts an unstructured map[string]any to map[string]string, converting scalar types to strings.
func extractStringMap(raw any) map[string]string {
	return crreader.CoerceStringMap(raw)
}

// injectAccessIntoJobSet injects environment variables and volume mounts across all replicated jobs.
func injectAccessIntoJobSet(jobSet *jobsetv1alpha2.JobSet, access componentAccess) {
	if jobSet == nil || (len(access.envVars) == 0 && len(access.secretEnvVars) == 0 && len(access.secretVolumeMounts) == 0) {
		return
	}
	envVars := buildComponentEnvVars(access.envVars, access.secretEnvVars)
	for i := range jobSet.Spec.ReplicatedJobs {
		podSpec := &jobSet.Spec.ReplicatedJobs[i].Template.Spec.Template.Spec
		mounts := buildSecretVolumeMounts(podSpec, access.secretVolumeMounts)
		injectIntoContainers(podSpec.Containers, envVars, mounts)
		injectIntoContainers(podSpec.InitContainers, envVars, mounts)
	}
}

// buildComponentEnvVars constructs plain and secret environment variable definitions.
func buildComponentEnvVars(plainEnv, secretEnv map[string]string) []corev1.EnvVar {
	envVars := buildPlainEnvVars(plainEnv)
	return append(envVars, buildSecretEnvVars(secretEnv)...)
}

func buildPlainEnvVars(plainEnv map[string]string) []corev1.EnvVar {
	if len(plainEnv) == 0 {
		return nil
	}
	keys := make([]string, 0, len(plainEnv))
	for k := range plainEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	envVars := make([]corev1.EnvVar, 0, len(keys))
	for _, k := range keys {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: plainEnv[k]})
	}
	return envVars
}

func buildSecretEnvVars(secretEnv map[string]string) []corev1.EnvVar {
	if len(secretEnv) == 0 {
		return nil
	}
	keys := make([]string, 0, len(secretEnv))
	for k := range secretEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	envVars := make([]corev1.EnvVar, 0, len(keys))
	for _, k := range keys {
		val := secretEnv[k]
		secretName, key, ok := strings.Cut(val, ":")
		if !ok || secretName == "" || key == "" {
			continue
		}
		envVars = append(envVars, corev1.EnvVar{
			Name: k,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secretName,
					},
					Key: key,
				},
			},
		})
	}
	return envVars
}

// buildSecretVolumeMounts ensures volumes exist in PodSpec and returns VolumeMount slices for containers.
func buildSecretVolumeMounts(podSpec *corev1.PodSpec, secretVolumeMounts map[string]string) []corev1.VolumeMount {
	if podSpec == nil || len(secretVolumeMounts) == 0 {
		return nil
	}
	keys := make([]string, 0, len(secretVolumeMounts))
	for k := range secretVolumeMounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	mounts := make([]corev1.VolumeMount, 0, len(keys))
	for _, k := range keys {
		val := secretVolumeMounts[k]
		secretName, mountPath, ok := strings.Cut(val, ":")
		if !ok || secretName == "" || mountPath == "" || !strings.HasPrefix(mountPath, "/") {
			continue
		}
		volName := getOrAddSecretVolume(podSpec, secretName)
		mounts = append(mounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: mountPath,
			ReadOnly:  true,
		})
	}
	return mounts
}

// getOrAddSecretVolume finds or appends a secret volume in PodSpec and returns its volume name.
func getOrAddSecretVolume(podSpec *corev1.PodSpec, secretName string) string {
	for _, v := range podSpec.Volumes {
		if v.Secret != nil && v.Secret.SecretName == secretName {
			return v.Name
		}
	}
	existingNames := make(map[string]bool, len(podSpec.Volumes))
	for _, v := range podSpec.Volumes {
		existingNames[v.Name] = true
	}
	baseName := sanitizeVolumeName(secretName)
	volName := baseName
	for i := 1; existingNames[volName]; i++ {
		volName = fmt.Sprintf("%s-%d", baseName, i)
	}
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: volName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secretName,
			},
		},
	})
	return volName
}

// sanitizeVolumeName converts Kubernetes secret names into RFC 1123 compliant volume names.
func sanitizeVolumeName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	cleaned := strings.Trim(b.String(), "-")
	if len(cleaned) > 50 {
		cleaned = strings.TrimRight(cleaned[:50], "-")
	}
	if cleaned == "" {
		cleaned = "secret-vol"
	}
	return cleaned
}

// injectIntoContainers appends environment variables and volume mounts to a container slice.
func injectIntoContainers(containers []corev1.Container, envVars []corev1.EnvVar, mounts []corev1.VolumeMount) {
	for i := range containers {
		c := &containers[i]
		for _, ev := range envVars {
			appendOrUpdateEnvVar(c, ev)
		}
		for _, m := range mounts {
			appendVolumeMountIfNotPresent(c, m)
		}
	}
}

func appendOrUpdateEnvVar(c *corev1.Container, ev corev1.EnvVar) {
	for i, existing := range c.Env {
		if existing.Name == ev.Name {
			c.Env[i] = ev
			return
		}
	}
	c.Env = append(c.Env, ev)
}

// appendVolumeMountIfNotPresent appends a volume mount to container if the mount path is not already present.
func appendVolumeMountIfNotPresent(c *corev1.Container, mount corev1.VolumeMount) {
	for _, existing := range c.VolumeMounts {
		if existing.MountPath == mount.MountPath {
			return
		}
	}
	c.VolumeMounts = append(c.VolumeMounts, mount)
}
