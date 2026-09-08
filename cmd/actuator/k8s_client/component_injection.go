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

	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/unstructured/unstructured"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/schema/schema"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/types/types"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
)

// evaluateComponentSnapshots discovers required component snapshots and evaluates the placement gate and drift warnings.
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
	if isComponentDisabled(comp) {
		return apierrors.NewServiceUnavailable(fmt.Sprintf("required component %q is disabled", compName))
	}

	if healthy, reason := evaluateComponentHealth(comp); !healthy {
		return apierrors.NewServiceUnavailable(fmt.Sprintf("required component %q is unhealthy: %s", compName, reason))
	}

	checkAndRecordComponentDrift(ctx, compName, lockedGen, comp, jobSet)
	return nil
}

// checkAndRecordComponentDrift records a warning annotation if observed generation does not match locked generation.
func checkAndRecordComponentDrift(ctx context.Context, compName string, lockedGen int64, comp *unstructured.Unstructured, jobSet *jobsetv1alpha2.JobSet) {
	if lockedGen <= 0 {
		return
	}
	observedGen := extractObservedGeneration(comp)
	if observedGen == lockedGen {
		return
	}
	logging.Warn(ctx, "Component generation drift detected", "component", compName, "observedGeneration", observedGen, "lockedGeneration", lockedGen)
	addDriftWarningAnnotation(jobSet, fmt.Sprintf("component %q generation mismatch: observed %d, locked %d", compName, observedGen, lockedGen))
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
		gvr := schema.GroupVersionResource{
			Group:    constants.ComponentCRDGroup,
			Version:  constants.ComponentCRDVersion,
			Resource: constants.ComponentCRDResource,
		}
		return c.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	}
	return nil, apierrors.NewServiceUnavailable("no kubernetes client available to fetch component")
}

// isComponentDisabled checks whether a component is explicitly disabled via spec.enabled or status.state.
func isComponentDisabled(comp *unstructured.Unstructured) bool {
	if comp == nil || comp.Object == nil {
		return false
	}
	if enabled, ok, err := unstructured.NestedBool(comp.Object, "spec", "enabled"); err == nil && ok && !enabled {
		return true
	}
	if state, ok, err := unstructured.NestedString(comp.Object, "status", "state"); err == nil && ok {
		return state == "COMPONENT_STATE_DISABLED" || state == "COMPONENT_STATE_DISABLING" || state == "COMPONENT_STATE_DELETED"
	}
	return false
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

// extractObservedGeneration retrieves observedGeneration from status, supporting multiple numeric formats including json.Number.
func extractObservedGeneration(comp *unstructured.Unstructured) int64 {
	if comp == nil || comp.Object == nil {
		return 0
	}
	val, ok, err := unstructured.NestedFieldNoCopy(comp.Object, "status", "observedGeneration")
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
