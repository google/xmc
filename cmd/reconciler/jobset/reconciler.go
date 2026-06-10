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
	"time"

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
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
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/api/v1alpha2/v1alpha2"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/common/labels"
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
	var podErrorMsg string
	state := ToWorkUnitState(jobSet)

	if state == spb.WorkUnitStatus_STARTING && r.StartupPodErrorCheckEnabled {
		result, podErrorMsg = r.handleStartupPodChecks(ctx, jobSet)
	}

	update, err := ConvertJobSetToWorkUnitExecutionUpdate(ctx, jobSet, wid, eid, r.Project, r.Location, r.ClusterName, podErrorMsg)
	if err != nil {
		logging.Error(ctx, "Failed to convert JobSet to WorkUnitExecutionUpdate", "namespace", jobSet.Namespace, "error", err)
		// we do not want to requeue as fields used in the update are monitored by predicate
		return ctrl.Result{}, nil
	}
	logging.Debug(ctx, "Successfully converted JobSet", "namespace", jobSet.Namespace)

	updatesSentTotal.WithLabelValues().Inc()
	// This function CANNOT BE CALLED WITHOUT A STRIPPED CONTEXT. If it gets it's hand on an slog,
	// it blocks the entire logging system for some reason.
	r.Batcher.Add(logging.Strip(ctx), xid, *update, shared.JobsetSource)

	return result, nil
}

func (r *Reconciler) handleStartupPodChecks(ctx context.Context, jobSet *v1alpha2.JobSet) (ctrl.Result, string) {
	age := calculateJobsetStartAge(jobSet)
	if age < r.StartupPodErrorCheckTime {
		logging.Debug(ctx, "Skipping startup pod error check for JobSet, age is less than StartupPodErrorCheckTime", "age", age)
		return ctrl.Result{RequeueAfter: r.StartupPodErrorCheckTime - age}, ""
	}

	podErrorMsg, err := r.checkPodsForStartupErrors(ctx, jobSet)
	if err != nil {
		logging.Error(ctx, "Failed to check for stuck starting job", "namespace", jobSet.Namespace, "error", err)
		return ctrl.Result{}, ""
	}
	return ctrl.Result{}, podErrorMsg
}

func (r *Reconciler) checkPodsForStartupErrors(ctx context.Context, jobSet *v1alpha2.JobSet) (string, error) {
	logging.Debug(ctx, "Checking pods to detect startup errors", "namespace", jobSet.Namespace, "jobSet", jobSet.Name)
	matchFields := client.MatchingFields{
		v1alpha2.JobSetNameKey: jobSet.Name,
	}
	var podList corev1.PodList
	if err := r.List(logging.Strip(ctx), &podList, client.InNamespace(jobSet.Namespace), matchFields); err != nil {
		return "", fmt.Errorf("failed to list pods: %w", err)
	}

	for _, pod := range podList.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Waiting != nil && (containerStatus.State.Waiting.Reason == "ImagePullBackOff" || containerStatus.State.Waiting.Reason == "ErrImagePull") {
				return fmt.Sprintf("Failed to pull the container image (%s): %s", containerStatus.State.Waiting.Reason, containerStatus.State.Waiting.Message), nil
			}
		}
	}
	logging.Debug(ctx, "No startup errors found in pods")
	return "", nil
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
