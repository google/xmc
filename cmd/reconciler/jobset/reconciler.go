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
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/cmd/reconciler/shared"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/kubernetes/common/labels"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
)

var (
	reconcileTotal = shared.MetricsFactory.NewCounterVec(prometheus.CounterOpts{
		Name: "jobset_reconcile_total",
		Help: "Total number of JobSet reconciliations started",
	}, []string{})

	reconcileErrorsTotal = shared.MetricsFactory.NewCounterVec(prometheus.CounterOpts{
		Name: "jobset_reconcile_errors_total",
		Help: "Total number of JobSet reconciliations that returned an error",
	}, []string{})

	reconcileDuration = shared.MetricsFactory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "jobset_reconcile_duration_milliseconds",
		Help:    "Duration of JobSet Reconcile calls in milliseconds",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000},
	}, []string{})

	updatesSentTotal = shared.MetricsFactory.NewCounterVec(prometheus.CounterOpts{
		Name: "jobset_updates_sent_to_batcher_total",
		Help: "Total number of updates sent to the batcher from the JobSet reconciler",
	}, []string{})
)

// Params holds dependencies and configuration for constructing a JobSet Reconciler.
type Params struct {
	Client                   client.Client
	Scheme                   *runtime.Scheme
	Batcher                  *shared.UpdateBatcher
	Project                  string
	Location                 string
	ClusterName              string
	MaxConcurrentReconciles  int
	StartupPodErrorCheckTime time.Duration
	Storage                  ReconcilerStorage
	MLRunClient              MLRunCreator
}

// Reconciler implements reconcile.Reconciler for JobSets.
type Reconciler struct {
	client.Client            // Embed the controller-runtime client
	Scheme                   *runtime.Scheme
	Batcher                  *shared.UpdateBatcher
	Project                  string
	Location                 string
	ClusterName              string
	MaxConcurrentReconciles  int
	StartupPodErrorCheckTime time.Duration
	Storage                  ReconcilerStorage
	MLRunClient              MLRunCreator
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
		Client:                   p.Client,
		Scheme:                   p.Scheme,
		Batcher:                  p.Batcher,
		Project:                  p.Project,
		Location:                 p.Location,
		ClusterName:              p.ClusterName,
		MaxConcurrentReconciles:  p.MaxConcurrentReconciles,
		StartupPodErrorCheckTime: p.StartupPodErrorCheckTime,
		Storage:                  p.Storage,
		MLRunClient:              p.MLRunClient,
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

func observeReconcileMetrics(startTime time.Time, recErr error) {
	duration := time.Since(startTime)
	reconcileDuration.WithLabelValues().Observe(float64(duration.Nanoseconds()) / 1e6)
	if recErr != nil {
		reconcileErrorsTotal.WithLabelValues().Inc()
	}
}

// Reconcile fetches the JobSet and logs its name and a summary of its ReplicatedJobsStatus.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, recErr error) {
	ctx = logging.WithLogger(ctx, logging.NewGCPLogger(slog.LevelInfo, ""))
	reconcileTotal.WithLabelValues().Inc()
	startTime := time.Now()
	defer func() { observeReconcileMetrics(startTime, recErr) }()

	ctx = logging.WithJobSetName(ctx, req.NamespacedName.Name, nil)
	logging.Info(ctx, "Reconcile JobSet", "namespacedName", req.NamespacedName)

	var jobSet v1alpha2.JobSet
	// This function CANNOT BE CALLED WITHOUT A STRIPPED CONTEXT. If it gets it's hand on an slog,
	// it blocks the entire logging system for some reason.
	if err := r.Get(logging.Strip(ctx), req.NamespacedName, &jobSet); err != nil {
		if apierrors.IsNotFound(err) {
			logging.Debug(ctx, "JobSet not found, no action needed", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		logging.Error(ctx, "Error fetching JobSet", "namespace", req.Namespace, "name", req.Name, "error", err)
		return ctrl.Result{}, err
	}

	if !jobSet.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleJobSetFinalizerRemoval(ctx, &jobSet)
	}

	result, err := r.syncJobSetStatus(ctx, &jobSet)
	logging.Info(ctx, "Reconciled JobSet", "namespacedName", req.NamespacedName)
	return result, err
}

// removeFinalizerPatch builds an RFC 6902 JSON Patch that tests and removes the
// finalizer at idx, guarding against concurrent metadata.finalizers mutations
// without pinning metadata.resourceVersion (which changes on status updates).
func removeFinalizerPatch(idx int, finalizer string) client.Patch {
	path := fmt.Sprintf("/metadata/finalizers/%d", idx)
	payload := fmt.Sprintf(`[{"op":"test","path":%q,"value":%q},{"op":"remove","path":%q}]`, path, finalizer, path)
	return client.RawPatch(types.JSONPatchType, []byte(payload))
}

func (r *Reconciler) enqueueStoppedUpdateOnDelete(ctx context.Context, jobSet *v1alpha2.JobSet, xid, wid, eid int64, labelsErr error) {
	if labelsErr != nil {
		logging.Warn(ctx, "JobSet is being deleted with finalizer but has invalid labels", "namespace", jobSet.Namespace, "error", labelsErr)
		return
	}
	update := createStoppedUpdate(wid, eid)
	updatesSentTotal.WithLabelValues().Inc()
	r.Batcher.Add(logging.Strip(ctx), xid, *update, shared.JobsetSource)
	logging.Debug(ctx, "Added STOPPED update to batch for JobSet", "namespace", jobSet.Namespace)
}

// handleJobSetFinalizerRemoval processes a JobSet that is being deleted.
func (r *Reconciler) handleJobSetFinalizerRemoval(ctx context.Context, jobSet *v1alpha2.JobSet) (ctrl.Result, error) {
	ctx = logging.WithJobSetName(ctx, jobSet.Name, jobSet.Labels)

	finalizerIdx := slices.Index(jobSet.Finalizers, constants.JobSetFinalizer)
	if finalizerIdx < 0 {
		logging.Debug(ctx, "JobSet is being deleted but finalizer is already gone.", "namespace", jobSet.Namespace)
		return ctrl.Result{}, nil
	}

	logging.Debug(ctx, "JobSet is being deleted, processing finalizer", "namespace", jobSet.Namespace)

	parsedLabels, labelsErr := labels.ParseXMCLabels(jobSet.Labels)
	xid, wid, eid := parsedLabels.XID, parsedLabels.WID, parsedLabels.EID
	ctx = logging.WithWorkUnitExecutionName(ctx, validator.ConstructWorkUnitExecutionName(xid, wid, eid))

	// This function CANNOT BE CALLED WITHOUT A STRIPPED CONTEXT.
	if err := r.Patch(logging.Strip(ctx), jobSet, removeFinalizerPatch(finalizerIdx, constants.JobSetFinalizer)); err != nil {
		return r.handlePatchError(ctx, jobSet, err)
	}

	r.enqueueStoppedUpdateOnDelete(ctx, jobSet, xid, wid, eid, labelsErr)
	logging.Debug(ctx, "Successfully patched finalizer from JobSet", "namespace", jobSet.Namespace)
	return ctrl.Result{}, nil
}

// finalizerPatchRetryDelay is the delay before retrying finalizer removal when
// the RFC 6902 JSON Patch test precondition fails (HTTP 422 Invalid) due to a
// concurrent metadata.finalizers edit.
const finalizerPatchRetryDelay = 100 * time.Millisecond

func (r *Reconciler) handlePatchError(ctx context.Context, jobSet *v1alpha2.JobSet, err error) (ctrl.Result, error) {
	ctx = logging.WithJobSetName(ctx, jobSet.Name, jobSet.Labels)

	if apierrors.IsNotFound(err) {
		logging.Info(ctx, "JobSet not found while trying to patch finalizer, assuming already deleted.", "namespace", jobSet.Namespace)
		return ctrl.Result{}, nil
	}
	if apierrors.IsInvalid(err) {
		logging.Debug(ctx, "Finalizer patch precondition failed for JobSet, requeuing.", "namespace", jobSet.Namespace)
		return ctrl.Result{RequeueAfter: finalizerPatchRetryDelay}, nil
	}
	logging.Error(ctx, "Failed to patch finalizer for JobSet", "namespace", jobSet.Namespace, "error", err)
	return ctrl.Result{}, fmt.Errorf("failed to patch finalizer for JobSet %s/%s: %w", jobSet.Namespace, jobSet.Name, err)
}

// syncJobSetStatus converts the JobSet to a WorkUnitExecutionUpdate and adds it to the batcher.
func (r *Reconciler) syncJobSetStatus(ctx context.Context, jobSet *v1alpha2.JobSet) (ctrl.Result, error) {
	ctx = logging.WithJobSetName(ctx, jobSet.Name, jobSet.Labels)

	parsedLabels, err := labels.ParseXMCLabels(jobSet.Labels)
	if err != nil {
		return ctrl.Result{}, reconcile.TerminalError(fmt.Errorf("failed to parse XMC labels for JobSet %s/%s: %w", jobSet.Namespace, jobSet.Name, err))
	}
	xid, wid, eid := parsedLabels.XID, parsedLabels.WID, parsedLabels.EID
	ctx = logging.WithWorkUnitExecutionName(ctx, validator.ConstructWorkUnitExecutionName(xid, wid, eid))

	var result ctrl.Result
	var podErrorMsg, startupWarningMsg string
	if ToWorkUnitState(jobSet) == spb.WorkUnitStatus_STARTING {
		checkRes := r.handleStartupPodChecks(ctx, jobSet)
		result, podErrorMsg, startupWarningMsg = checkRes.Result, checkRes.PodErrorMsg, checkRes.StartupWarningMsg
	}

	mlRunURL := r.syncMLRun(ctx, jobSet, xid, wid, eid)

	update := ConvertJobSetToWorkUnitExecutionUpdate(ctx, jobSet, wid, eid, r.Project, r.Location, r.ClusterName, podErrorMsg, startupWarningMsg)
	setMLRunURL(update, mlRunURL)
	logging.Debug(ctx, "Successfully converted JobSet", "namespace", jobSet.Namespace)

	updatesSentTotal.WithLabelValues().Inc()
	// This function CANNOT BE CALLED WITHOUT A STRIPPED CONTEXT. If it gets it's hand on an slog,
	// it blocks the entire logging system for some reason.
	r.Batcher.Add(logging.Strip(ctx), xid, *update, shared.JobsetSource)

	return result, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if r == nil {
		return errors.New("reconciler cannot be nil")
	}
	logging.Debug(ctx, "Setting up jobset reconciler controller with MaxConcurrentReconciles", "MaxConcurrentReconciles", r.MaxConcurrentReconciles)
	err := ctrl.NewControllerManagedBy(mgr).
		Named("jobset").
		WatchesRawSource(source.Kind(
			mgr.GetCache(),
			&v1alpha2.JobSet{},
			&handler.TypedEnqueueRequestForObject[*v1alpha2.JobSet]{},
			jobSetPredicate(ctx),
		)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create jobset controller: %w", err)
	}
	return nil
}
