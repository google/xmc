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
// Package jobset provides a reconciler for JobSets.
package jobset

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	k8slabels "golang/k8s_io/apimachinery/v/v0_23/pkg/labels/labels"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	"golang/prometheus/client/prometheus/promauto/promauto"
	"golang/prometheus/client/prometheus/prometheus"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/controller/controller"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/controller/controllerutil/controllerutil"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/event/event"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/handler/handler"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/manager/manager"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/predicate/predicate"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/source/source"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/common/labels"
	"xmanager_cloud/kubernetes/reconciler/hypercomputeclient/hypercomputeclient"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
)

var (
	reconcileTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobset_reconcile_total",
		Help: "Total number of JobSet reconciliations started",
	}, []string{})

	reconcileErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobset_reconcile_errors_total",
		Help: "Total number of JobSet reconciliations that returned an error",
	}, []string{})

	reconcileDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "jobset_reconcile_duration_milliseconds",
		Help:    "Duration of JobSet Reconcile calls in milliseconds",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000},
	}, []string{})

	updatesSentTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobset_updates_sent_to_batcher_total",
		Help: "Total number of updates sent to the batcher from the JobSet reconciler",
	}, []string{})
)

// Reconciler implements reconcile.Reconciler for JobSets.
type Reconciler struct {
	client.Client               // Embed the controller-runtime client
	Scheme                      *runtime.Scheme
	Batcher                     *shared.UpdateBatcher
	Project                     string
	Location                    string
	ClusterName                 string
	MaxConcurrentReconciles     int
	StartupPodErrorCheckTime    time.Duration
	StartupPodErrorCheckEnabled bool
	Storage                     WorkUnitExecutionReader
	MLRunClient                 MLRunCreator
}

// WorkUnitExecutionReader is an interface for reading WorkUnitExecutions from ESS.
type WorkUnitExecutionReader interface {
	FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*spb.WorkUnitExecution, error)
}

// MLRunCreator defines the interface required for creating, looking up, and
// updating the single MLRun tracking record allowed per jobset.
type MLRunCreator interface {
	CreateMLRun(ctx context.Context, name, displayName, runPhase string, opts *hypercomputeclient.CreateMLRunOptions) (map[string]any, error)
	GetMLRun(ctx context.Context, name string) (map[string]any, error)
	UpdateMLRun(ctx context.Context, name string, opts *hypercomputeclient.UpdateMLRunOptions) (map[string]any, error)
}

func hasValidXMCLabels(ctx context.Context, jobSet *v1alpha2.JobSet) bool {
	ctx = logging.WithJobSetName(ctx, jobSet.Name, nil)

	_, _, _, err := labels.ParseXMCLabels(jobSet.Labels)
	if err != nil {
		if errors.Is(err, labels.ErrMissingXMCLabels) {
			logging.Warn(ctx, "JobSet is missing XMC labels and will be skipped", "error", err)
		} else {
			logging.Error(ctx, "JobSet has invalid XMC labels and will be skipped", "error", err)
		}
		return false
	}
	return true
}

func jobSetPredicate(ctx context.Context) predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			ctx := logging.Strip(ctx)

			jobSet, ok := e.Object.(*v1alpha2.JobSet)
			if !ok {
				logging.Error(ctx, "Predicate received unexpected object type in CreateEvent", "type", fmt.Sprintf("%T", e.Object))
				return true
			}
			return hasValidXMCLabels(ctx, jobSet)
		},

		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},

		UpdateFunc: func(e event.UpdateEvent) bool {
			oldJobSet, ok := e.ObjectOld.(*v1alpha2.JobSet)
			if !ok {
				return true
			}
			newJobSet, ok := e.ObjectNew.(*v1alpha2.JobSet)
			if !ok {
				return true
			}

			if oldJobSet.GetResourceVersion() == newJobSet.GetResourceVersion() {
				return false
			}

			generationSame := oldJobSet.Generation == newJobSet.Generation
			labelsSame := oldJobSet.Labels[constants.WorkUnitIDLabel] == newJobSet.Labels[constants.WorkUnitIDLabel] &&
				oldJobSet.Labels[constants.ExperimentIDLabel] == newJobSet.Labels[constants.ExperimentIDLabel]
			statusSame := isJobSetStatusEqual(&oldJobSet.Status, &newJobSet.Status)
			deletionSame := (oldJobSet.GetDeletionTimestamp() == nil) == (newJobSet.GetDeletionTimestamp() == nil)

			oldSus := oldJobSet.Spec.Suspend != nil && *oldJobSet.Spec.Suspend
			newSus := newJobSet.Spec.Suspend != nil && *newJobSet.Spec.Suspend
			specSame := generationSame && oldSus == newSus && calculateTotalReplicas(oldJobSet) == calculateTotalReplicas(newJobSet)

			if specSame && labelsSame && statusSame && deletionSame {
				return false
			}

			ctx := logging.Strip(ctx)
			newManaged := hasValidXMCLabels(ctx, newJobSet)
			oldManaged := hasValidXMCLabels(ctx, oldJobSet)

			// Both must be managed to trigger on updates (unless it's a creation/deletion which is handled elsewhere).
			// If it wasn't managed before, we don't want to trigger on status updates.
			// If it's not managed now, we don't care.
			if !newManaged || !oldManaged {
				return false
			}

			// Trigger on the start of deletion.
			if oldJobSet.GetDeletionTimestamp() == nil && newJobSet.GetDeletionTimestamp() != nil {
				return true
			}

			// If already deleting, do not trigger on subsequent updates.
			if newJobSet.GetDeletionTimestamp() != nil {
				return false
			}

			return true
		},

		GenericFunc: func(e event.GenericEvent) bool {
			ctx := logging.Strip(ctx)

			// Reconcile existing resources on controller startup.
			jobSet, ok := e.Object.(*v1alpha2.JobSet)
			if !ok {
				logging.Error(ctx, "Predicate received unexpected object type in GenericEvent", "type", fmt.Sprintf("%T", e.Object))
				return true
			}
			return hasValidXMCLabels(ctx, jobSet)
		},
	}
}

// Reconcile fetches the JobSet and logs its name and a summary of its ReplicatedJobsStatus.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, recErr error) {
	ctx = logging.WithLogger(ctx, logging.NewGCPLogger(slog.LevelInfo, ""))

	reconcileTotal.WithLabelValues().Inc()
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		reconcileDuration.WithLabelValues().Observe(float64(duration.Nanoseconds()) / 1e6)
		if recErr != nil {
			reconcileErrorsTotal.WithLabelValues().Inc()
		}
	}()
	ctx = logging.WithJobSetName(ctx, req.NamespacedName.Name, nil)

	logging.Info(ctx, "Reconcile JobSet", "namespacedName", req.NamespacedName)

	var jobSet v1alpha2.JobSet
	// This function CANNOT BE CALLED WITHOUT A STRIPPED CONTEXT. If it gets it's hand on an slog,
	// it blocks the entire logging system for some reason.
	if err := r.Get(logging.Strip(ctx), req.NamespacedName, &jobSet); err != nil {
		if apierrors.IsNotFound(err) {
			logging.Debug(ctx, "JobSet not found, no action needed", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil // Successfully do nothing
		}
		logging.Error(ctx, "Error fetching JobSet", "namespace", req.Namespace, "name", req.Name, "error", err)
		recErr = err
		return ctrl.Result{}, err // Return error for other issues
	}

	if !jobSet.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleJobSetFinalizerRemoval(ctx, &jobSet)
	}

	result, err := r.syncJobSetStatus(ctx, &jobSet)

	logging.Info(ctx, "Reconciled JobSet", "namespacedName", req.NamespacedName)
	return result, err
}

// handleJobSetFinalizerRemoval processes a JobSet that is being deleted.
func (r *Reconciler) handleJobSetFinalizerRemoval(ctx context.Context, jobSet *v1alpha2.JobSet) (ctrl.Result, error) {
	ctx = logging.WithJobSetName(ctx, jobSet.Name, jobSet.Labels)

	if !controllerutil.ContainsFinalizer(jobSet, constants.JobSetFinalizer) {
		logging.Debug(ctx, "JobSet is being deleted but finalizer is already gone.", "namespace", jobSet.Namespace)
		return ctrl.Result{}, nil
	}

	logging.Debug(ctx, "JobSet is being deleted, processing finalizer", "namespace", jobSet.Namespace)

	xid, wid, eid, labelsErr := labels.ParseXMCLabels(jobSet.Labels)
	ctx = logging.WithWorkUnitExecutionName(ctx, validator.ConstructWorkUnitExecutionName(xid, wid, eid))
	if labelsErr == nil {
		update := createStoppedUpdate(wid, eid)
		updatesSentTotal.WithLabelValues().Inc()
		r.Batcher.Add(ctx, xid, *update, shared.JobsetSource)
		logging.Debug(ctx, "Added STOPPED update to batch for JobSet", "namespace", jobSet.Namespace)
	} else {
		logging.Warn(ctx, "JobSet is being deleted with finalizer but has invalid labels", "namespace", jobSet.Namespace, "error", labelsErr)
	}

	// Create a deep copy to use as the base for the patch
	patchBase := jobSet.DeepCopy()

	// Modify the jobSet object in-place to remove the finalizer
	controllerutil.RemoveFinalizer(jobSet, constants.JobSetFinalizer)

	// Patch the object in the API server
	// This function CANNOT BE CALLED WITHOUT A STRIPPED CONTEXT. If it gets it's hand on an slog,
	// it blocks the entire logging system for some reason.
	if err := r.Patch(logging.Strip(ctx), jobSet, client.MergeFrom(patchBase)); err != nil {
		return r.handlePatchError(ctx, jobSet, err)
	}

	logging.Debug(ctx, "Successfully patched finalizer from JobSet", "namespace", jobSet.Namespace)
	return ctrl.Result{}, nil
}

func (r *Reconciler) handlePatchError(ctx context.Context, jobSet *v1alpha2.JobSet, err error) (ctrl.Result, error) {
	ctx = logging.WithJobSetName(ctx, jobSet.Name, jobSet.Labels)

	if apierrors.IsNotFound(err) {
		logging.Info(ctx, "JobSet not found while trying to patch finalizer, assuming already deleted.", "namespace", jobSet.Namespace)
		return ctrl.Result{}, nil
	}
	if apierrors.IsConflict(err) {
		logging.Debug(ctx, "Conflict patching finalizer for JobSet, requeuing for retry.", "namespace", jobSet.Namespace)
		return ctrl.Result{Requeue: true}, nil
	}
	logging.Error(ctx, "Failed to patch finalizer for JobSet", "namespace", jobSet.Namespace, "error", err)
	return ctrl.Result{}, fmt.Errorf("failed to patch finalizer for JobSet %s/%s: %w", jobSet.Namespace, jobSet.Name, err)
}

// syncJobSetStatus converts the JobSet to a WorkUnitExecutionUpdate and adds it to the batcher.
func (r *Reconciler) syncJobSetStatus(ctx context.Context, jobSet *v1alpha2.JobSet) (ctrl.Result, error) {
	ctx = logging.WithJobSetName(ctx, jobSet.Name, jobSet.Labels)

	xid, wid, eid, err := labels.ParseXMCLabels(jobSet.Labels)
	ctx = logging.WithWorkUnitExecutionName(ctx, validator.ConstructWorkUnitExecutionName(xid, wid, eid))
	if err != nil {
		logging.Error(ctx, "Failed to parse labels for JobSet", "namespace", jobSet.Namespace, "error", err)
		return ctrl.Result{}, nil
	}

	var result ctrl.Result
	var podErrorMsg, startupWarningMsg string
	state := ToWorkUnitState(jobSet)

	if state == spb.WorkUnitStatus_STARTING && r.StartupPodErrorCheckEnabled {
		result, podErrorMsg, startupWarningMsg = r.handleStartupPodChecks(ctx, jobSet)
	}

	displayName, runGroup, gcsPath := getManagedXprofEnvs(jobSet)
	// Only warn about missing Managed Xprof config for jobs that actually opted
	// into it (at least one env var set). Standard JobSets leave all three empty
	// and would otherwise flood the logs with warnings every reconciliation.
	if gcsPath != "" || displayName != "" || runGroup != "" {
		if gcsPath == "" {
			logging.Warn(ctx, "No GCS path found for Managed Xprof, skipping MLRun URL creation.", "namespace", jobSet.Namespace, "xid", xid, "wid", wid)
		}
		if displayName == "" {
			logging.Warn(ctx, "No display name found for Managed Xprof, skipping MLRun URL creation.", "namespace", jobSet.Namespace, "xid", xid, "wid", wid)
		}
		if runGroup == "" {
			logging.Warn(ctx, "No run group found for Managed Xprof, skipping MLRun URL creation.", "namespace", jobSet.Namespace, "xid", xid, "wid", wid)
		}
	}
	var mlRunURL string
	if gcsPath != "" && displayName != "" && runGroup != "" {
		var skip bool
		mlRunURL, skip = r.checkWorkUnitStatusAndMLRun(ctx, xid, wid)
		if !skip && mlRunURL == "" {
			logging.Info(ctx, "Begin to create MLRun URL.")
			mlRunURL = r.ensureMLRunCreated(ctx, jobSet, xid, wid, eid, displayName, runGroup, gcsPath)
		}
	}

	update, err := ConvertJobSetToWorkUnitExecutionUpdate(ctx, jobSet, wid, eid, r.Project, r.Location, r.ClusterName, podErrorMsg, startupWarningMsg)
	if err != nil {
		logging.Error(ctx, "Failed to convert JobSet to WorkUnitExecutionUpdate", "namespace", jobSet.Namespace, "error", err)
		// we do not want to requeue as fields used in the update are monitored by predicate
		return ctrl.Result{}, nil
	}
	setMLRunURL(update, mlRunURL)
	logging.Debug(ctx, "Successfully converted JobSet", "namespace", jobSet.Namespace)

	updatesSentTotal.WithLabelValues().Inc()
	// This function CANNOT BE CALLED WITHOUT A STRIPPED CONTEXT. If it gets it's hand on an slog,
	// it blocks the entire logging system for some reason.
	r.Batcher.Add(logging.Strip(ctx), xid, *update, shared.JobsetSource)

	return result, nil
}

// unschedulablePodsCheckTime bounds how long handleStartupPodChecks keeps
// re-queuing a STARTING JobSet to re-check for an unschedulable verdict, so a
// pod stuck Pending does not trigger the reconcile loop indefinitely.
const unschedulablePodsCheckTime = 5 * time.Minute

func (r *Reconciler) handleStartupPodChecks(ctx context.Context, jobSet *v1alpha2.JobSet) (ctrl.Result, string, string) {
	age := calculateJobsetStartAge(jobSet)
	if age < r.StartupPodErrorCheckTime {
		logging.Debug(ctx, "Skipping startup pod error check for JobSet, age is less than StartupPodErrorCheckTime", "age", age)
		return ctrl.Result{RequeueAfter: r.StartupPodErrorCheckTime - age}, "", ""
	}

	// Fetch the JobSet's pods once and share the list between both checks (avoids
	// a redundant API List call).
	pods, err := r.listJobSetPods(ctx, jobSet)
	if err != nil {
		logging.Error(ctx, "Failed to list pods for startup checks", "namespace", jobSet.Namespace, "error", err)
		return ctrl.Result{}, "", ""
	}

	// A hard error (e.g. ImagePullBackOff) fails the work unit immediately.
	if podErrorMsg := checkPodsForStartupErrors(ctx, pods); podErrorMsg != "" {
		return ctrl.Result{}, podErrorMsg, ""
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
	return result, "", warningMsg
}

// listJobSetPods returns all pods belonging to the given JobSet.
func (r *Reconciler) listJobSetPods(ctx context.Context, jobSet *v1alpha2.JobSet) ([]corev1.Pod, error) {
	var podList corev1.PodList
	if err := r.List(logging.Strip(ctx), &podList, client.InNamespace(jobSet.Namespace),
		client.MatchingFields{v1alpha2.JobSetNameKey: jobSet.Name}); err != nil {
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
	if err := r.List(logging.Strip(ctx), &nodeList, client.MatchingLabelsSelector{Selector: selector}); err != nil {
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

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logging.Debug(ctx, "Setting up jobset reconciler")
	if r.Batcher == nil {
		return fmt.Errorf("batcher is not initialized")
	}

	logging.Debug(ctx, "Starting jobset reconciler controller with MaxConcurrentReconciles", "MaxConcurrentReconciles", r.MaxConcurrentReconciles)
	c, err := controller.New("jobset", mgr, controller.Options{
		Reconciler:              r,
		MaxConcurrentReconciles: r.MaxConcurrentReconciles,
	})
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	err = c.Watch(&source.Kind{Type: &v1alpha2.JobSet{}}, &handler.EnqueueRequestForObject{}, jobSetPredicate(ctx))
	if err != nil {
		return fmt.Errorf("failed to watch JobSet: %w", err)
	}
	logging.Debug(ctx, "Jobset reconciler controller started")

	logging.Debug(ctx, "Adding batch processor runnable")
	if err := mgr.Add(manager.RunnableFunc(r.Batcher.BatchProcessor)); err != nil {
		return fmt.Errorf("failed to add batch processor runnable: %w", err)
	}
	logging.Debug(ctx, "Batch processor runnable added")

	return nil
}

func isJobSetStatusEqual(old, new *v1alpha2.JobSetStatus) bool {
	if old.Restarts != new.Restarts {
		return false
	}
	if len(old.Conditions) != len(new.Conditions) {
		return false
	}
	for i := range old.Conditions {
		if old.Conditions[i].Type != new.Conditions[i].Type ||
			old.Conditions[i].Status != new.Conditions[i].Status ||
			old.Conditions[i].Reason != new.Conditions[i].Reason ||
			old.Conditions[i].Message != new.Conditions[i].Message {
			return false
		}
	}
	if len(old.ReplicatedJobsStatus) != len(new.ReplicatedJobsStatus) {
		return false
	}
	for i := range old.ReplicatedJobsStatus {
		if old.ReplicatedJobsStatus[i].Name != new.ReplicatedJobsStatus[i].Name ||
			old.ReplicatedJobsStatus[i].Ready != new.ReplicatedJobsStatus[i].Ready ||
			old.ReplicatedJobsStatus[i].Succeeded != new.ReplicatedJobsStatus[i].Succeeded ||
			old.ReplicatedJobsStatus[i].Failed != new.ReplicatedJobsStatus[i].Failed ||
			old.ReplicatedJobsStatus[i].Active != new.ReplicatedJobsStatus[i].Active ||
			old.ReplicatedJobsStatus[i].Suspended != new.ReplicatedJobsStatus[i].Suspended {
			return false
		}
	}
	return true
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

// createdByXManagerLabelValue marks an MLRun as owned by the XManager reconciler.
// On a create conflict, a run carrying created-by equal to this value is treated as
// our own (idempotent success); a run without it is adopted (updated) with our fields.
const createdByXManagerLabelValue = "xmanager"

// mlRunResourceNameRE captures the MLRun id embedded in an AlreadyExists error's
// resource name, e.g. "projects/p/locations/l/machineLearningRuns/some-id".
var mlRunResourceNameRE = regexp.MustCompile(`machineLearningRuns/([a-zA-Z0-9_-]+)`)

// buildMLRunLabels returns the labels stamped on every MLRun the reconciler owns.
// Extracted so create and upsert apply an identical label set (including the
// created-by marker that lets us detect ownership on a create conflict).
func buildMLRunLabels(xid, wid int64) map[string]string {
	return map[string]string{
		"experiment-id": fmt.Sprintf("%d", xid),
		"workunit-id":   fmt.Sprintf("%d", wid),
		"created-by":    createdByXManagerLabelValue,
	}
}

// ConstructMLRunID deterministically constructs and sanitizes a unique MLRun identifier for an execution.
//
// Extracted into its own pure function so that ID formatting and sanitization are independently testable and
// guaranteed idempotent when reconciliations are retried.
func ConstructMLRunID(project, location, clusterName string, xid, wid, eid int64, creationTime time.Time) string {
	rawID := fmt.Sprintf("xmc-%s-%s-%s-%d-%d-%d-%d", project, location, clusterName, xid, wid, eid, creationTime.UnixMilli())
	return hypercomputeclient.SanitizeIdentifier(rawID)
}

// ensureMLRunCreated creates (or, on conflict, upserts) the single MLRun tracking
// record for a ManagedXprof execution and returns its URL.
//
// Extracted to separate MLRun control-plane invocation and error handling from the
// core JobSet status synchronization workflow.
func (r *Reconciler) ensureMLRunCreated(ctx context.Context, jobSet *v1alpha2.JobSet, xid, wid, eid int64, displayName, runGroup, gcsPath string) string {
	if r.MLRunClient == nil || jobSet == nil {
		return ""
	}

	mlRunID := ConstructMLRunID(r.Project, r.Location, r.ClusterName, xid, wid, eid, jobSet.CreationTimestamp.Time)
	clusterPath := r.ClusterName
	if !strings.HasPrefix(clusterPath, "projects/") && r.Project != "" && r.Location != "" {
		clusterPath = fmt.Sprintf("projects/%s/locations/%s/clusters/%s", r.Project, r.Location, r.ClusterName)
	}

	// Build labels and artifacts once so an AlreadyExists upsert can apply the exact
	// same ManagedXprof fields (and ownership marker) the create would have.
	labels := buildMLRunLabels(xid, wid)
	artifacts := map[string]string{"gcs_path": gcsPath}
	opts := &hypercomputeclient.CreateMLRunOptions{
		RunGroup:     runGroup,
		Tools:        []map[string]any{{"xprof": map[string]any{}}},
		Labels:       labels,
		Artifacts:    artifacts,
		Orchestrator: "GKE",
		WorkloadDetails: &hypercomputeclient.GKEWorkloadDetails{
			Kind:      "JobSet",
			Cluster:   clusterPath,
			Namespace: jobSet.Namespace,
			ID:        jobSet.Name,
		},
	}

	logging.Info(ctx, "Creating MLRun for ManagedXprof execution", "mlRunID", mlRunID, "displayName", displayName, "runGroup", runGroup)
	res, err := r.MLRunClient.CreateMLRun(ctx, mlRunID, displayName, "ACTIVE", opts)
	if err != nil {
		// At most one MLRun may exist per jobset, so a conflict means one already
		// exists (possibly created by another producer); adopt or refresh it instead
		// of returning empty and looping forever on the 409.
		if isMLRunAlreadyExistsError(err) {
			return r.upsertExistingMLRun(ctx, err, mlRunID, displayName, runGroup, labels, artifacts)
		}
		logging.Error(ctx, "Failed to create MLRun for ManagedXprof execution", "mlRunID", mlRunID, "error", err)
		return ""
	}
	return r.mlRunURL(res, mlRunID)
}

// isMLRunAlreadyExistsError reports whether err is the control plane's AlreadyExists
// response. The control plane surfaces this two ways: an HTTP 409 Conflict from the
// create call, and a long-running-operation failure carrying google.rpc.Code
// AlreadyExists (6). We branch on the typed errors' codes rather than matching error
// strings, which is brittle across message-wording changes.
func isMLRunAlreadyExistsError(err error) bool {
	var apiErr *hypercomputeclient.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusConflict
	}
	var opErr *hypercomputeclient.OperationError
	if errors.As(err, &opErr) {
		return opErr.Code == hypercomputeclient.CodeAlreadyExists
	}
	return false
}

// parseExistingMLRunID extracts the conflicting run's id from an AlreadyExists
// error. Because only one MLRun may exist per jobset, the control plane embeds the
// existing run's resource name in the error, letting us locate a run created with a
// different id than ours without a separate List call. Returns "" if not found.
func parseExistingMLRunID(errText string) string {
	m := mlRunResourceNameRE.FindStringSubmatch(errText)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// mlRunOwnedByXManager reports whether an MLRun resource carries our
// created-by=xmanager label. A GET response decodes labels as map[string]any, so we
// type-assert defensively.
func mlRunOwnedByXManager(mlRun map[string]any) bool {
	labels, ok := mlRun["labels"].(map[string]any)
	if !ok {
		return false
	}
	owner, _ := labels["created-by"].(string)
	return owner == createdByXManagerLabelValue
}

// mlRunURL returns the URL to surface for an MLRun: the API-provided url when
// present, otherwise the deterministic Cloud Console diagnostics URL for the id.
func (r *Reconciler) mlRunURL(res map[string]any, mlRunID string) string {
	if res != nil {
		if urlVal, ok := res["url"].(string); ok && urlVal != "" {
			return urlVal
		}
	}
	return constructDiagnosticsConsoleURL(r.Location, mlRunID, r.Project)
}

// upsertExistingMLRun handles an MLRun create conflict. It locates the existing run
// (its id is carried in the conflict error, falling back to our deterministic id)
// and, if the run is not one we own, updates it with our ManagedXprof fields so
// enrichment is applied even to runs created by other producers. A run we already
// own is returned as an idempotent success without an update.
func (r *Reconciler) upsertExistingMLRun(ctx context.Context, createErr error, mlRunID, displayName, runGroup string, labels map[string]string, artifacts map[string]string) string {
	existingID := parseExistingMLRunID(createErr.Error())
	if existingID == "" {
		// The conflict did not name the run; the common case is our own
		// deterministic id, so fall back to it.
		existingID = mlRunID
	}

	existing, err := r.MLRunClient.GetMLRun(ctx, existingID)
	if err != nil {
		logging.Warn(ctx, "MLRun already exists but could not be fetched; treating as success", "mlRunID", existingID, "error", err)
		return constructDiagnosticsConsoleURL(r.Location, existingID, r.Project)
	}
	if mlRunOwnedByXManager(existing) {
		logging.Info(ctx, "MLRun already exists and is owned by XManager; treating as success", "mlRunID", existingID)
		return constructDiagnosticsConsoleURL(r.Location, existingID, r.Project)
	}

	logging.Info(ctx, "MLRun exists but is not owned by XManager; updating with ManagedXprof fields", "mlRunID", existingID)
	// UpdateMLRun re-sends the run fetched via GET, so the orchestrator and
	// workloadDetails set at creation are preserved as-is. We intentionally do not
	// override them here: the control plane rejects an orchestrator *change* (equal
	// values are accepted), and in XMC they are fixed at creation.
	res, err := r.MLRunClient.UpdateMLRun(ctx, existingID, &hypercomputeclient.UpdateMLRunOptions{
		DisplayName: displayName,
		RunGroup:    runGroup,
		Labels:      labels,
		Artifacts:   artifacts,
	})
	if err != nil {
		logging.Error(ctx, "Failed to update existing MLRun for ManagedXprof execution", "mlRunID", existingID, "error", err)
		return constructDiagnosticsConsoleURL(r.Location, existingID, r.Project)
	}
	return r.mlRunURL(res, existingID)
}

// constructDiagnosticsConsoleURL constructs the standard ML Diagnostics detail view
// URL in Google Cloud Console.
func constructDiagnosticsConsoleURL(location, mlRunID, project string) string {
	if location == "" || mlRunID == "" || project == "" {
		return ""
	}
	return fmt.Sprintf("https://console.cloud.google.com/kubernetes/aiml/run/%s/%s?project=%s", location, mlRunID, project)
}

func getManagedXprofEnvs(jobSet *v1alpha2.JobSet) (displayName, runGroup, gcsPath string) {
	for _, rj := range jobSet.Spec.ReplicatedJobs {
		for _, c := range rj.Template.Spec.Template.Spec.Containers {
			for _, env := range c.Env {
				switch env.Name {
				case constants.XmcEnvVarManagedXprofDisplayName:
					displayName = env.Value
				case constants.XmcEnvVarManagedXprofRunGroup:
					runGroup = env.Value
				case constants.XmcEnvVarManagedXprofGCSObjectFolder:
					gcsPath = env.Value
				}
			}
			// These environment variables are same on all containers, so we can return early.
			if displayName != "" || runGroup != "" || gcsPath != "" {
				return
			}
		}
	}
	return
}

// getMLRunURL extracts the MLRun URL from the WorkUnitExecution metadata safely.
func getMLRunURL(execution *spb.WorkUnitExecution) string {
	if execution != nil && execution.GetMetadata() != nil {
		return execution.GetMetadata().GetMlrunUrl()
	}
	return ""
}

// isTerminalState returns true if the work unit state in ESS indicates it has stopped or is stopping.
func isTerminalState(state spb.WorkUnitStatus_WorkUnitState) bool {
	switch state {
	case spb.WorkUnitStatus_COMPLETED, spb.WorkUnitStatus_FAILED, spb.WorkUnitStatus_STOPPED, spb.WorkUnitStatus_STOPPING:
		return true
	default:
		return false
	}
}

// checkWorkUnitStatusAndMLRun queries ESS to prevent redundant actions and retrieves the existing MLRun URL to maintain state.
//
// Returns:
//   - string: The existing MLRun URL from ESS metadata.
//   - bool: True if the WorkUnit is in a terminal state and status synchronization should be skipped.
func (r *Reconciler) checkWorkUnitStatusAndMLRun(ctx context.Context, xid, wid int64) (string, bool) {
	mlRunURL := ""
	skipMLRunStatusSync := false
	if r.Storage == nil {
		return mlRunURL, skipMLRunStatusSync
	}
	execution, fetchErr := r.Storage.FetchCurrentWorkUnitExecution(ctx, xid, wid)
	if fetchErr != nil {
		logging.Error(ctx, "Failed to fetch current WorkUnitExecution from ESS", "error", fetchErr)
		return mlRunURL, skipMLRunStatusSync
	}

	mlRunURL = getMLRunURL(execution)

	if execution != nil && isTerminalState(execution.GetStatus().GetState()) {
		currentState := execution.GetStatus().GetState()
		logging.Info(ctx, "WorkUnit execution is in terminal/stopping state in ESS, skipping status synchronization", "currentState", currentState.String())
		skipMLRunStatusSync = true
	}

	return mlRunURL, skipMLRunStatusSync
}
