// Copyright 2025 Google LLC
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

package jobset

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/kubernetes/common/constants"
)

// unschedulablePodsCheckTime bounds how long handleStartupPodChecks keeps
// re-queuing a STARTING JobSet to re-check for an unschedulable verdict, so a
// pod stuck Pending does not trigger the reconcile loop indefinitely.
const unschedulablePodsCheckTime = 5 * time.Minute

// tpuNodeListTimeout bounds the cached Node list call in listAvailableTPUTopologies
// so an unsynced Node informer (e.g. on missing RBAC or API server latency) fails
// fast and falls back to the generic unschedulable warning instead of blocking the
// reconcile worker.
const tpuNodeListTimeout = 5 * time.Second

// StartupPodCheckResult contains the status result and error/warning messages for startup pod checks.
type StartupPodCheckResult struct {
	Result            ctrl.Result
	PodErrorMsg       string
	StartupWarningMsg string
}

func isMultiKueueManaged(jobSet *v1alpha2.JobSet) bool {
	return jobSet != nil && jobSet.Spec.ManagedBy != nil && *jobSet.Spec.ManagedBy == constants.KueueMultiKueueControllerName
}

func (r *Reconciler) handleStartupPodChecks(ctx context.Context, jobSet *v1alpha2.JobSet) StartupPodCheckResult {
	if isMultiKueueManaged(jobSet) {
		logging.Debug(ctx, "Skipping local startup pod check for MultiKueue-managed JobSet")
		return StartupPodCheckResult{}
	}
	age := calculateJobsetStartAge(jobSet)
	if age < r.StartupPodErrorCheckTime {
		logging.Debug(ctx, "Skipping startup pod error check for JobSet, age is less than StartupPodErrorCheckTime", "age", age)
		return StartupPodCheckResult{Result: ctrl.Result{RequeueAfter: r.StartupPodErrorCheckTime - age}}
	}

	// Fetch the JobSet's pods once and share the list between both checks (avoids
	// a redundant API List call).
	pods, err := r.listJobSetPods(ctx, jobSet)
	if err != nil {
		logging.Error(ctx, "Failed to list pods for startup checks", "namespace", jobSet.Namespace, "error", err)
		return StartupPodCheckResult{}
	}

	// A hard error (e.g. ImagePullBackOff) fails the work unit immediately.
	if podErrorMsg := checkPodsForStartupErrors(ctx, pods); podErrorMsg != "" {
		return StartupPodCheckResult{PodErrorMsg: podErrorMsg}
	}

	// No hard error: the pods may simply be unschedulable (exhausted quota, no
	// matching nodes). Surface the scheduler's verdict as a warning while keeping
	// the work unit in STARTING. Bound the extra re-checks with
	// unschedulablePodsCheckTime so we don't requeue indefinitely; after that we
	// rely on event-driven reconciles (the warning still clears once pods schedule).
	warningMsg := r.checkPodsForUnschedulable(ctx, pods)
	result := ctrl.Result{}
	if warningMsg != "" && age < unschedulablePodsCheckTime {
		result.RequeueAfter = unschedulablePodsCheckTime - age
	}
	return StartupPodCheckResult{Result: result, StartupWarningMsg: warningMsg}
}

// listJobSetPods returns all pods belonging to the given JobSet. JobSet stamps
// v1alpha2.JobSetNameKey as a label on every pod it creates.
func (r *Reconciler) listJobSetPods(ctx context.Context, jobSet *v1alpha2.JobSet) ([]corev1.Pod, error) {
	var podList corev1.PodList
	if err := r.List(logging.Strip(ctx), &podList, client.InNamespace(jobSet.Namespace),
		client.MatchingLabels{v1alpha2.JobSetNameKey: jobSet.Name}); err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}
	return podList.Items, nil
}

// checkPodsForUnschedulable returns a human-readable warning if any pod of the
// JobSet is Pending with a scheduler verdict of Unschedulable (PodScheduled=False).
// It reads the scheduler's actual condition rather than guessing from elapsed
// time, so a pod that is merely waiting for a node to be provisioned is not
// misreported once it schedules.
func (r *Reconciler) checkPodsForUnschedulable(ctx context.Context, pods []corev1.Pod) string {
	for i := range pods {
		pod := &pods[i]
		if pod.Status.Phase != corev1.PodPending {
			continue
		}
		for _, cond := range pod.Status.Conditions {
			if !isPodUnschedulableCondition(cond) {
				continue
			}
			if tpuWarning := r.checkTPUTopologyMismatch(ctx, pod, cond.Message); tpuWarning != "" {
				return tpuWarning
			}
			return fmt.Sprintf(
				"Work unit may be unschedulable: %s. Check cluster quota and node availability (e.g. kubectl describe pod / clusterqueue).",
				cond.Message)
		}
	}
	logging.Debug(ctx, "No unschedulable pods found")
	return ""
}

func isPodUnschedulableCondition(cond corev1.PodCondition) bool {
	return cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == corev1.PodReasonUnschedulable
}

// checkTPUTopologyMismatch checks if an unschedulable pod is waiting on an unsatisfiable TPU topology selector.
func (r *Reconciler) checkTPUTopologyMismatch(ctx context.Context, pod *corev1.Pod, condMsg string) string {
	if pod.Spec.NodeSelector == nil {
		return ""
	}
	reqTopology, hasTopology := pod.Spec.NodeSelector[constants.TPUTopologySelectorKey]
	if !hasTopology || !isNodeSelectorMismatchMessage(condMsg) {
		return ""
	}

	availTopologies, err := r.listAvailableTPUTopologies(ctx)
	if err != nil {
		logging.Warn(ctx, "Failed to discover available TPU topologies in cluster", "error", err)
		return ""
	}
	if len(availTopologies) == 0 {
		return "" // Fall back to generic warning to avoid false positives during autoscaling from zero.
	}

	for _, avail := range availTopologies {
		if avail == reqTopology {
			return "" // Topology exists in cluster; pod is pending for quota or another reason.
		}
	}

	reqType := pod.Spec.NodeSelector[constants.TPUAcceleratorSelectorKey]
	return fmt.Sprintf(
		"Work unit is unschedulable: requested TPU topology %q (type %q) does not match any node pool's advertised topology in the cluster. Available TPU topologies: %v. Please request a topology supported by the cluster's TPU node pools.",
		reqTopology, reqType, availTopologies)
}

func isNodeSelectorMismatchMessage(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "node affinity/selector") || strings.Contains(lower, "nodeselector") || strings.Contains(lower, "didn't match")
}

// listAvailableTPUTopologies returns a sorted list of unique TPU topologies advertised by nodes in the cluster.
func (r *Reconciler) listAvailableTPUTopologies(ctx context.Context) ([]string, error) {
	var nodeList corev1.NodeList
	selector, err := k8slabels.Parse(constants.TPUTopologySelectorKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse TPU topology label selector: %w", err)
	}
	listCtx, cancel := context.WithTimeout(logging.Strip(ctx), tpuNodeListTimeout)
	defer cancel()
	if err := r.List(listCtx, &nodeList, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("failed to list nodes with TPU topology label: %w", err)
	}

	topologySet := make(map[string]struct{})
	for i := range nodeList.Items {
		if topVal, exists := nodeList.Items[i].Labels[constants.TPUTopologySelectorKey]; exists && topVal != "" {
			topologySet[topVal] = struct{}{}
		}
	}

	topologies := make([]string, 0, len(topologySet))
	for top := range topologySet {
		topologies = append(topologies, top)
	}
	sort.Strings(topologies)
	return topologies, nil
}

// TODO(vluk): Check InitContainerStatuses and EphemeralContainerStatuses for image pull errors in addition to ContainerStatuses.
func checkPodsForStartupErrors(ctx context.Context, pods []corev1.Pod) string {
	for _, pod := range pods {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Waiting != nil && (containerStatus.State.Waiting.Reason == "ImagePullBackOff" || containerStatus.State.Waiting.Reason == "ErrImagePull") {
				return fmt.Sprintf("Failed to pull the container image (%s): %s", containerStatus.State.Waiting.Reason, containerStatus.State.Waiting.Message)
			}
		}
	}
	logging.Debug(ctx, "No startup errors found in pods")
	return ""
}

func calculateJobsetStartAge(jobSet *v1alpha2.JobSet) time.Duration {
	// We are looking for the last transition time to determine how much time has passed since jobset was admitted to the kueue.
	for _, condition := range jobSet.Status.Conditions {
		if condition.Type == string(v1alpha2.JobSetSuspended) && condition.Status == metav1.ConditionFalse {
			return time.Since(condition.LastTransitionTime.Time)
		}
	}
	// if LastTransitionTime is not found, we fall back to the creation time
	return time.Since(jobSet.CreationTimestamp.Time)
}
