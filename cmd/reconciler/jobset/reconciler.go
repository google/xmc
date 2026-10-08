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
	"strings"
	"time"

	"golang/prometheus/client/prometheus/prometheus"
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
	"xmanager_cloud/cmd/reconciler/hypercomputeclient"
	"xmanager_cloud/cmd/reconciler/shared"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/kubernetes/common/labels"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/validator/validator"
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

	// mlRunSyncDuration measures the wall-clock the reconcile loop spends in the
	// MLRun path, so we can tell how much MLRun creation adds to a reconcile
	// (compare against jobset_reconcile_duration_milliseconds).
	mlRunSyncDuration = shared.MetricsFactory.NewHistogram(prometheus.HistogramOpts{
		Name:    "jobset_reconciler_mlrun_sync_duration_seconds",
		Help:    "Wall-clock time the reconciler spends in the MLRun path per reconcile.",
		Buckets: mlRunLatencyBuckets,
	})
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

// MLRunCreator defines the interface required for creating, looking up, and
// updating the single MLRun tracking record allowed per jobset.
type MLRunCreator interface {
	CreateMLRun(ctx context.Context, name, displayName, runPhase string, opts *hypercomputeclient.CreateMLRunOptions) (map[string]any, error)
	GetMLRun(ctx context.Context, name string) (map[string]any, error)
	UpdateMLRun(ctx context.Context, name string, opts *hypercomputeclient.UpdateMLRunOptions) (map[string]any, error)
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

// removeFinalizerPatch builds an RFC 6902 JSON Patch that tests and removes the
// finalizer at idx, guarding against concurrent metadata.finalizers mutations
// without pinning metadata.resourceVersion (which changes on status updates).
func removeFinalizerPatch(idx int, finalizer string) client.Patch {
	path := fmt.Sprintf("/metadata/finalizers/%d", idx)
	payload := fmt.Sprintf(`[{"op":"test","path":%q,"value":%q},{"op":"remove","path":%q}]`, path, finalizer, path)
	return client.RawPatch(types.JSONPatchType, []byte(payload))
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

	// Patch the object in the API server using an RFC 6902 JSON Patch that tests
	// and removes the finalizer at its index without pinning metadata.resourceVersion
	// (so concurrent .status updates by the JobSet controller do not cause conflicts).
	// This function CANNOT BE CALLED WITHOUT A STRIPPED CONTEXT. If it gets it's hand on an slog,
	// it blocks the entire logging system for some reason.
	if err := r.Patch(logging.Strip(ctx), jobSet, removeFinalizerPatch(finalizerIdx, constants.JobSetFinalizer)); err != nil {
		return r.handlePatchError(ctx, jobSet, err)
	}

	if labelsErr == nil {
		update := createStoppedUpdate(wid, eid)
		updatesSentTotal.WithLabelValues().Inc()
		r.Batcher.Add(logging.Strip(ctx), xid, *update, shared.JobsetSource)
		logging.Debug(ctx, "Added STOPPED update to batch for JobSet", "namespace", jobSet.Namespace)
	} else {
		logging.Warn(ctx, "JobSet is being deleted with finalizer but has invalid labels", "namespace", jobSet.Namespace, "error", labelsErr)
	}

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
		// For JSON Patch, a failed `test` precondition (concurrent finalizer list
		// edit or stale cache index) is rejected by the API server with 422 Invalid.
		// Requeue after a short delay without returning an error so it is not counted
		// in reconcileErrorsTotal or logged at ERROR by controller-runtime.
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
	state := ToWorkUnitState(jobSet)

	if state == spb.WorkUnitStatus_STARTING {
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

// ensureMLRunCreated creates (or, on conflict, upserts) the single MLRun tracking
// record for a ManagedXprof execution and returns its URL.
//
// Extracted to separate MLRun control-plane invocation and error handling from the
// core JobSet status synchronization workflow.
func (r *Reconciler) ensureMLRunCreated(ctx context.Context, jobSet *v1alpha2.JobSet, xid, wid, eid int64, displayName, runGroup, gcsPath string) string {
	if r.MLRunClient == nil || jobSet == nil {
		return ""
	}

	if jobSet.CreationTimestamp.IsZero() {
		logging.Warn(ctx, "JobSet creation timestamp is uninitialized when constructing MLRun ID", "jobset", jobSet.Name)
	}

	mlRunID := ConstructMLRunID(r.ClusterName, jobSet.Namespace, "JobSet", jobSet.Name, jobSet.CreationTimestamp.Time)
	clusterPath := r.ClusterName
	if !strings.HasPrefix(clusterPath, "projects/") && r.Project != "" && r.Location != "" {
		clusterPath = fmt.Sprintf("projects/%s/locations/%s/clusters/%s", r.Project, r.Location, r.ClusterName)
	}

	createTime := formatCreationTime(jobSet.CreationTimestamp)

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
			Kind:       "JobSet",
			Cluster:    clusterPath,
			Namespace:  jobSet.Namespace,
			ID:         jobSet.Name,
			CreateTime: createTime,
		},
	}

	logging.Info(ctx, "Creating MLRun for ManagedXprof execution", "mlRunID", mlRunID, "displayName", displayName, "runGroup", runGroup)
	_, err := r.MLRunClient.CreateMLRun(ctx, mlRunID, displayName, "ACTIVE", opts)
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

	// Create-then-update: stamp list_existing_sessions_only post-creation to preserve
	// the ACTIVE phase (creating with it forces COMPLETED, which blocks on-demand profiling).
	res, err := r.MLRunClient.UpdateMLRun(ctx, mlRunID, &hypercomputeclient.UpdateMLRunOptions{
		Labels: map[string]string{listExistingSessionsOnlyLabel: "true"},
	})
	if err != nil {
		logging.Error(ctx, "Failed to stamp list_existing_sessions_only on MLRun", "mlRunID", mlRunID, "error", err)
		return ""
	}
	return r.mlRunURL(res, mlRunID)
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
	if mlRunOwnedByXManager(existing) && mlRunHasLabel(existing, listExistingSessionsOnlyLabel, "true") {
		logging.Info(ctx, "MLRun already exists and is owned by XManager; treating as success", "mlRunID", existingID)
		return r.mlRunURL(existing, existingID)
	}

	logging.Info(ctx, "Updating existing MLRun with ManagedXprof fields", "mlRunID", existingID)
	updateLabels := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		updateLabels[k] = v
	}
	updateLabels[listExistingSessionsOnlyLabel] = "true"

	res, err := r.MLRunClient.UpdateMLRun(ctx, existingID, &hypercomputeclient.UpdateMLRunOptions{
		DisplayName: displayName,
		RunGroup:    runGroup,
		Labels:      updateLabels,
		Artifacts:   artifacts,
	})
	if err != nil {
		logging.Error(ctx, "Failed to update existing MLRun for ManagedXprof execution", "mlRunID", existingID, "error", err)
		return constructDiagnosticsConsoleURL(r.Location, existingID, r.Project)
	}
	return r.mlRunURL(res, existingID)
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
