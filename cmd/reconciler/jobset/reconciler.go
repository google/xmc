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

	// mlRunSyncDuration measures the wall-clock the reconcile loop spends in the
	// MLRun path, so we can tell how much MLRun creation adds to a reconcile
	// (compare against jobset_reconcile_duration_milliseconds).
	mlRunSyncDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "jobset_reconciler_mlrun_sync_duration_seconds",
		Help:    "Wall-clock time the reconciler spends in the MLRun path per reconcile.",
		Buckets: mlRunLatencyBuckets,
	})
)

// Params holds dependencies and configuration for constructing a JobSet Reconciler.
type Params struct {
	Client                      client.Client
	Scheme                      *runtime.Scheme
	Batcher                     *shared.UpdateBatcher
	Project                     string
	Location                    string
	ClusterName                 string
	MaxConcurrentReconciles     int
	StartupPodErrorCheckTime    time.Duration
	StartupPodErrorCheckEnabled bool
	Storage                     ReconcilerStorage
	MLRunClient                 MLRunCreator
}

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
	Storage                     ReconcilerStorage
	MLRunClient                 MLRunCreator
}

// NewReconciler creates and validates a new JobSet Reconciler.
func NewReconciler(p Params) (*Reconciler, error) {
	if p.Client == nil {
		return nil, errors.New("client is required")
	}
	if p.Scheme == nil {
		return nil, errors.New("scheme is required")
	}
	if p.Batcher == nil {
		return nil, errors.New("batcher is required")
	}
	return &Reconciler{
		Client:                      p.Client,
		Scheme:                      p.Scheme,
		Batcher:                     p.Batcher,
		Project:                     p.Project,
		Location:                    p.Location,
		ClusterName:                 p.ClusterName,
		MaxConcurrentReconciles:     p.MaxConcurrentReconciles,
		StartupPodErrorCheckTime:    p.StartupPodErrorCheckTime,
		StartupPodErrorCheckEnabled: p.StartupPodErrorCheckEnabled,
		Storage:                     p.Storage,
		MLRunClient:                 p.MLRunClient,
	}, nil
}

// WorkUnitExecutionReader is an interface for reading WorkUnitExecutions from ESS.
type WorkUnitExecutionReader interface {
	FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*spb.WorkUnitExecution, error)
}

// ManagedXprofArtifactReader lists the artifacts attached to a resource so the
// reconciler can read the ManagedXprof artifact directly from storage.
type ManagedXprofArtifactReader interface {
	ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*spb.Artifact, string, error)
}

// ReconcilerStorage is the storage surface the JobSet reconciler depends on. It is
// composed from focused interfaces so callers and tests only implement what they use.
type ReconcilerStorage interface {
	WorkUnitExecutionReader
	ManagedXprofArtifactReader
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

	_, err := labels.ParseXMCLabels(jobSet.Labels)
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
			oldJobSet, okOld := e.ObjectOld.(*v1alpha2.JobSet)
			newJobSet, okNew := e.ObjectNew.(*v1alpha2.JobSet)
			if !okOld || !okNew {
				return true
			}
			return shouldProcessJobSetUpdate(ctx, oldJobSet, newJobSet)
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

func shouldProcessJobSetUpdate(ctx context.Context, oldJobSet, newJobSet *v1alpha2.JobSet) bool {
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

	ctx = logging.Strip(ctx)
	if !hasValidXMCLabels(ctx, newJobSet) || !hasValidXMCLabels(ctx, oldJobSet) {
		return false
	}

	if oldJobSet.GetDeletionTimestamp() == nil && newJobSet.GetDeletionTimestamp() != nil {
		return true
	}

	return newJobSet.GetDeletionTimestamp() == nil
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

	parsedLabels, labelsErr := labels.ParseXMCLabels(jobSet.Labels)
	xid, wid, eid := parsedLabels.XID, parsedLabels.WID, parsedLabels.EID
	ctx = logging.WithWorkUnitExecutionName(ctx, validator.ConstructWorkUnitExecutionName(xid, wid, eid))
	if labelsErr == nil {
		update := createStoppedUpdate(wid, eid)
		updatesSentTotal.WithLabelValues().Inc()
		r.Batcher.Add(logging.Strip(ctx), xid, *update, shared.JobsetSource)
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

	parsedLabels, err := labels.ParseXMCLabels(jobSet.Labels)
	xid, wid, eid := parsedLabels.XID, parsedLabels.WID, parsedLabels.EID
	ctx = logging.WithWorkUnitExecutionName(ctx, validator.ConstructWorkUnitExecutionName(xid, wid, eid))
	if err != nil {
		logging.Error(ctx, "Failed to parse labels for JobSet", "namespace", jobSet.Namespace, "error", err)
		return ctrl.Result{}, nil
	}

	var result ctrl.Result
	var podErrorMsg, startupWarningMsg string
	state := ToWorkUnitState(jobSet)

	if state == spb.WorkUnitStatus_STARTING && r.StartupPodErrorCheckEnabled {
		checkRes := r.handleStartupPodChecks(ctx, jobSet)
		result, podErrorMsg, startupWarningMsg = checkRes.Result, checkRes.PodErrorMsg, checkRes.StartupWarningMsg
	}

	// The actuator injects the ManagedXprof GCS destination, display name, and run
	// group into the JobSet env. The display name and run group can still be missing
	// when the ManagedXprof artifact is attached after the JobSet is created (racing
	// actuation), so fall back to reading them from the WorkUnit-level artifact. When
	// they remain absent we skip this round and a later reconcile retries once the
	// artifact is present.
	// Time the whole ManagedXprof/MLRun path (env extraction, the optional artifact
	// lookup, and MLRun creation) so its cost is attributable independently of the
	// rest of the reconcile loop. The histogram is only observed when the MLRun path
	// is taken, so its _count doubles as the number of reconciles that entered it.
	mlRunStart := time.Now()
	mxEnvs := getManagedXprofEnvs(jobSet)
	displayName, runGroup, gcsPath := mxEnvs.DisplayName, mxEnvs.RunGroup, mxEnvs.GCSPath
	// Only query storage when ManagedXprof is enabled (gcsPath set) but the display
	// metadata is missing. Standard JobSets leave gcsPath empty and skip MLRun
	// creation anyway, so the lookup would be wasted work on the hot reconcile path.
	if gcsPath != "" && (displayName == "" || runGroup == "") {
		displayName, runGroup = r.lookupManagedXprofArtifact(ctx, xid, wid)
	}
	warnOnMissingManagedXprofEnvs(ctx, jobSet, displayName, runGroup, gcsPath, xid, wid)
	var mlRunURL string
	if gcsPath != "" && displayName != "" && runGroup != "" {
		var skipMLRunCreation bool
		mlRunURL, skipMLRunCreation = r.checkWorkUnitStatusAndMLRun(ctx, xid, wid, eid)
		if !skipMLRunCreation && mlRunURL == "" {
			logging.Info(ctx, "Begin to create MLRun URL.")
			mlRunURL = r.ensureMLRunCreated(ctx, jobSet, xid, wid, eid, displayName, runGroup, gcsPath)
		}
		mlRunSyncDuration.Observe(time.Since(mlRunStart).Seconds())
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

// StartupPodCheckResult contains the status result and error/warning messages for startup pod checks.
type StartupPodCheckResult struct {
	Result            ctrl.Result
	PodErrorMsg       string
	StartupWarningMsg string
}

func (r *Reconciler) handleStartupPodChecks(ctx context.Context, jobSet *v1alpha2.JobSet) StartupPodCheckResult {
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
	if r == nil {
		return errors.New("reconciler cannot be nil")
	}
	logging.Debug(ctx, "Setting up jobset reconciler")
	if r.Batcher == nil {
		return errors.New("batcher is not initialized")
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
	if !isJobSetConditionsEqual(old.Conditions, new.Conditions) {
		return false
	}
	return isReplicatedJobsStatusEqual(old.ReplicatedJobsStatus, new.ReplicatedJobsStatus)
}

func isJobSetConditionsEqual(old, new []metav1.Condition) bool {
	if len(old) != len(new) {
		return false
	}
	for i := range old {
		if old[i].Type != new[i].Type ||
			old[i].Status != new[i].Status ||
			old[i].Reason != new[i].Reason ||
			old[i].Message != new[i].Message {
			return false
		}
	}
	return true
}

func isReplicatedJobsStatusEqual(old, new []v1alpha2.ReplicatedJobStatus) bool {
	if len(old) != len(new) {
		return false
	}
	for i := range old {
		if old[i].Name != new[i].Name ||
			old[i].Ready != new[i].Ready ||
			old[i].Succeeded != new[i].Succeeded ||
			old[i].Failed != new[i].Failed ||
			old[i].Active != new[i].Active ||
			old[i].Suspended != new[i].Suspended {
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

// listExistingSessionsOnlyLabel tells the Diagon profiler-session backend to
// discover profiles by recursively scanning the run's GCS folder (<gcs_path>/**)
// rather than assuming its own <gcs_path>/<run_id>/plugins/profile/ layout. XMC
// workloads (e.g. the SDK-less programmatic Xprof example) write the profile
// themselves to <gcs_path>/plugins/profile/ and cannot know the
// control-plane-minted run id, so without this label the backend never scans the
// bucket and the profile is never surfaced.
//
// Setting it unconditionally is safe: XMC assigns a unique per-work-unit gcs_path
// (so distinct runs never see each other's profiles even with a recursive scan),
// and on-demand capture still works with the label set.
const listExistingSessionsOnlyLabel = "list_existing_sessions_only"

// mlRunResourceNameRE captures the MLRun id embedded in an AlreadyExists error's
// resource name, e.g. "projects/p/locations/l/machineLearningRuns/some-id".
var mlRunResourceNameRE = regexp.MustCompile(`machineLearningRuns/([a-zA-Z0-9_-]+)`)

// buildMLRunLabels returns the labels stamped on every MLRun the reconciler owns.
// Extracted so create and upsert apply an identical label set (including the
// created-by marker that lets us detect ownership on a create conflict).
func buildMLRunLabels(xid, wid int64) map[string]string {
	return map[string]string{
		"experiment-id":               fmt.Sprintf("%d", xid),
		"workunit-id":                 fmt.Sprintf("%d", wid),
		"created-by":                  createdByXManagerLabelValue,
		listExistingSessionsOnlyLabel: "true",
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
	// workloadDetails set at creation are preserved as-is. They are mutable, but we
	// intentionally do not set them here: this upsert only updates the ManagedXprof
	// fields, and UpdateMLRun drops orchestrator/workloadDetails from the patch so
	// they are left untouched.
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

// artifactListPageSize bounds the single ListArtifacts page read by
// lookupManagedXprofArtifact. At most one ManagedXprof artifact is expected per
// work unit, so a small page is sufficient.
const artifactListPageSize = 10

// managedXprofArtifactFilter restricts ListArtifacts to ManagedXprof artifacts
// server-side (via the payload_type schema filter field), so the reconciler no
// longer fetches and discards unrelated artifact types while searching.
const managedXprofArtifactFilter = `payload_type = "managed_xprof"`

// lookupManagedXprofArtifact reads the ManagedXprof artifact attached to the work
// unit and returns its display name and run group.
//
// The artifact is read directly from storage (rather than from JobSet env) because
// clients may attach it after the JobSet is created. An absent artifact yields
// empty strings, so the caller skips MLRun creation this round and retries on a
// later reconcile.
func (r *Reconciler) lookupManagedXprofArtifact(ctx context.Context, xid, wid int64) (displayName, runGroup string) {
	// Storage is optional in some reconciler configurations; without it there is
	// no artifact source, so the fallback is a no-op (mirrors the nil-MLRunClient
	// guard in ensureMLRunCreated).
	if r.Storage == nil {
		return "", ""
	}
	parent := fmt.Sprintf("experiments/%d/workUnits/%d", xid, wid)

	// A work unit has at most one ManagedXprof artifact and the server filters by
	// payload_type, so a single small page suffices; use the first match.
	artifacts, _, err := r.Storage.ListArtifacts(ctx, parent, artifactListPageSize, "", managedXprofArtifactFilter, "", 0)
	if err != nil {
		logging.Warn(ctx, "Failed to list artifacts for WorkUnit", "xid", xid, "wid", wid, "error", err)
		return "", ""
	}
	managed := collectManagedXprofArtifacts(artifacts)
	if len(managed) == 0 {
		return "", ""
	}
	if len(managed) > 1 {
		// At most one ManagedXprof artifact is expected per work unit; surface the
		// anomaly and deterministically use the first so behavior stays predictable.
		logging.Warn(ctx, "Multiple ManagedXprof artifacts found for WorkUnit; using the first", "xid", xid, "wid", wid, "count", len(managed))
	}
	return managed[0].GetDisplayName(), managed[0].GetRunGroup()
}

// collectManagedXprofArtifacts returns every ManagedXprof artifact in the list,
// in order. Extracted as a pure function so artifact selection is independently
// testable.
func collectManagedXprofArtifacts(artifacts []*spb.Artifact) []*spb.ManagedXprofArtifact {
	var managed []*spb.ManagedXprofArtifact
	for _, art := range artifacts {
		if mx := art.GetPayload().GetManagedXprof(); mx != nil {
			managed = append(managed, mx)
		}
	}
	return managed
}

// ManagedXprofEnvs holds environmental configurations for Managed Xprof.
type ManagedXprofEnvs struct {
	DisplayName string
	RunGroup    string
	GCSPath     string
}

func getManagedXprofEnvs(jobSet *v1alpha2.JobSet) ManagedXprofEnvs {
	var envs ManagedXprofEnvs
	for _, rj := range jobSet.Spec.ReplicatedJobs {
		for _, c := range rj.Template.Spec.Template.Spec.Containers {
			for _, env := range c.Env {
				switch env.Name {
				case constants.XmcEnvVarManagedXprofDisplayName:
					envs.DisplayName = env.Value
				case constants.XmcEnvVarManagedXprofRunGroup:
					envs.RunGroup = env.Value
				case constants.XmcEnvVarManagedXprofGCSObjectFolder:
					envs.GCSPath = env.Value
				}
			}
			// These environment variables are same on all containers, so we can return early.
			if envs.DisplayName != "" || envs.RunGroup != "" || envs.GCSPath != "" {
				return envs
			}
		}
	}
	return envs
}

// managedXprofMisconfigured reports the one actionable Managed Xprof
// misconfiguration: a JobSet that carries display metadata (both display name and
// run group) but no GCS path, so an MLRun can never be created for it. A missing
// display name or run group while gcsPath is set is an expected transient (the
// WorkUnit artifact may attach after the JobSet is created and is recovered by the
// fallback lookup), so it is deliberately not flagged. Extracted as a pure
// function so the warn policy is testable without a logging sink.
func managedXprofMisconfigured(displayName, runGroup, gcsPath string) bool {
	return displayName != "" && runGroup != "" && gcsPath == ""
}

// warnOnMissingManagedXprofEnvs warns when a JobSet is misconfigured for Managed
// Xprof (see managedXprofMisconfigured). Standard JobSets and JobSets still waiting
// on their WorkUnit artifact are intentionally silent to avoid flooding the
// reconcile loop's logs.
func warnOnMissingManagedXprofEnvs(ctx context.Context, jobSet *v1alpha2.JobSet, displayName, runGroup, gcsPath string, xid, wid int64) {
	if managedXprofMisconfigured(displayName, runGroup, gcsPath) {
		logging.Warn(ctx, "No GCS path found for Managed Xprof, skipping MLRun URL creation.", "namespace", jobSet.Namespace, "xid", xid, "wid", wid)
	}
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
//   - bool: True if the WorkUnit is in a terminal state and MLRun creation should be skipped.
func (r *Reconciler) checkWorkUnitStatusAndMLRun(ctx context.Context, xid, wid, eid int64) (string, bool) {
	mlRunURL := ""
	skipMLRunCreation := false
	if r.Storage == nil {
		return mlRunURL, skipMLRunCreation
	}
	execution, fetchErr := r.Storage.FetchCurrentWorkUnitExecution(ctx, xid, wid)
	if fetchErr != nil {
		// TODO: Only transient (e.g. network) errors should be retried here,
		// bounded by a time budget, so a fetch failure does not silently drop the
		// MLRun URL. Reconciliation must stay fast, so the retry is deferred until
		// we observe the issue in practice. Until then a failed fetch is treated
		// as "no URL"; a later reconciliation (on the next JobSet status change)
		// can recreate it.
		logging.Error(ctx, "Failed to fetch current WorkUnitExecution from ESS", "error", fetchErr)
		return mlRunURL, skipMLRunCreation
	}

	mlRunURL = getMLRunURL(execution)

	// The execution is fetched by (xid, wid); if it belongs to a different execution
	// than the one being reconciled (a re-execution race where ESS has not advanced
	// its current execution), reusing its MLRun URL would misattribute it. We only
	// warn for now to avoid premature optimization.
	if mlRunURL != "" && execution != nil && isStaleExecutionForMLRun(execution.GetName(), eid) {
		logging.Warn(ctx, "Reusing MLRun URL from a different current execution than the one being reconciled; possible re-execution race.", "reconciledEID", eid, "xid", xid, "wid", wid, "executionName", execution.GetName())
	}

	if execution != nil && isTerminalState(execution.GetStatus().GetState()) {
		currentState := execution.GetStatus().GetState()
		logging.Info(ctx, "WorkUnit execution is in terminal/stopping state in ESS, skipping MLRun creation", "currentState", currentState.String())
		skipMLRunCreation = true
	}

	return mlRunURL, skipMLRunCreation
}

// isStaleExecutionForMLRun reports whether executionName refers to a different
// execution than the one currently being reconciled (reconciledEID). Extracted as a
// pure function so the re-execution race detection is independently testable.
// Returns false when the name cannot be parsed, since a mismatch cannot be proven.
func isStaleExecutionForMLRun(executionName string, reconciledEID int64) bool {
	_, _, currentEID, err := validator.ValidateWorkUnitExecutionName(executionName)
	if err != nil {
		return false
	}
	return currentEID != reconciledEID
}
