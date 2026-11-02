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
	"maps"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/cmd/reconciler/hypercomputeclient"
	"xmanager_cloud/cmd/reconciler/shared"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
)

// mlRunSyncDuration measures the wall-clock the reconcile loop spends in the
// MLRun path, so we can tell how much MLRun creation adds to a reconcile
// (compare against jobset_reconcile_duration_milliseconds).
var mlRunSyncDuration = shared.MetricsFactory.NewHistogram(prometheus.HistogramOpts{
	Name:    "jobset_reconciler_mlrun_sync_duration_seconds",
	Help:    "Wall-clock time the reconciler spends in the MLRun path per reconcile.",
	Buckets: mlRunLatencyBuckets,
})

// MLRunCreator defines the interface required for creating, looking up, and
// updating the single MLRun tracking record allowed per jobset.
type MLRunCreator interface {
	CreateMLRun(ctx context.Context, name, displayName, runPhase string, opts *hypercomputeclient.CreateMLRunOptions) (map[string]any, error)
	GetMLRun(ctx context.Context, name string) (map[string]any, error)
	UpdateMLRun(ctx context.Context, name string, opts *hypercomputeclient.UpdateMLRunOptions) (map[string]any, error)
}

type mlRunCacheKey struct {
	xid int64
	wid int64
	eid int64
}

// maxMLRunURLCacheEntries caps the in-memory MLRun URL cache so memory stays
// bounded even if JobSets disappear without going through finalizer eviction.
const maxMLRunURLCacheEntries = 10000

func (r *Reconciler) getCachedMLRunURL(xid, wid, eid int64) string {
	r.mlRunCacheMu.Lock()
	defer r.mlRunCacheMu.Unlock()
	return r.mlRunURLCache[mlRunCacheKey{xid: xid, wid: wid, eid: eid}]
}

func (r *Reconciler) cacheMLRunURL(xid, wid, eid int64, url string) {
	if url == "" {
		return
	}
	r.mlRunCacheMu.Lock()
	defer r.mlRunCacheMu.Unlock()
	if r.mlRunURLCache == nil {
		r.mlRunURLCache = make(map[mlRunCacheKey]string)
	} else if len(r.mlRunURLCache) >= maxMLRunURLCacheEntries {
		clear(r.mlRunURLCache)
	}
	r.mlRunURLCache[mlRunCacheKey{xid: xid, wid: wid, eid: eid}] = url
}

func (r *Reconciler) evictCachedMLRunURLs(xid, wid, eid int64) {
	r.mlRunCacheMu.Lock()
	defer r.mlRunCacheMu.Unlock()
	for k := range r.mlRunURLCache {
		if k.xid == xid && k.wid == wid && k.eid <= eid {
			delete(r.mlRunURLCache, k)
		}
	}
}

// syncMLRun resolves ManagedXprof metadata for a JobSet and ensures its MLRun
// tracking record is created or reused when ManagedXprof is enabled.
func (r *Reconciler) syncMLRun(ctx context.Context, jobSet *v1alpha2.JobSet, xid, wid, eid int64) string {
	mlRunStart := time.Now()
	mxEnvs := getManagedXprofEnvs(jobSet)
	displayName, runGroup, gcsPath := mxEnvs.DisplayName, mxEnvs.RunGroup, mxEnvs.GCSPath
	if gcsPath != "" {
		if cached := r.getCachedMLRunURL(xid, wid, eid); cached != "" {
			mlRunSyncDuration.Observe(time.Since(mlRunStart).Seconds())
			return cached
		}
	}
	if gcsPath != "" && (displayName == "" || runGroup == "") {
		displayName, runGroup = r.lookupManagedXprofArtifact(ctx, xid, wid)
	}
	warnOnMissingManagedXprofEnvs(ctx, jobSet, displayName, runGroup, gcsPath, xid, wid)
	if gcsPath == "" || displayName == "" || runGroup == "" {
		return ""
	}
	mlRunURL, skipMLRunCreation := r.checkWorkUnitStatusAndMLRun(ctx, xid, wid, eid)
	if !skipMLRunCreation && mlRunURL == "" {
		logging.Info(ctx, "Begin to create MLRun URL.")
		var confirmed bool
		mlRunURL, confirmed = r.ensureMLRunCreated(ctx, jobSet, xid, wid, eid, displayName, runGroup, gcsPath)
		if confirmed {
			r.cacheMLRunURL(xid, wid, eid, mlRunURL)
		}
	}
	mlRunSyncDuration.Observe(time.Since(mlRunStart).Seconds())
	return mlRunURL
}

func resolveClusterPath(clusterName, project, location string) string {
	if !strings.HasPrefix(clusterName, "projects/") && project != "" && location != "" {
		return fmt.Sprintf("projects/%s/locations/%s/clusters/%s", project, location, clusterName)
	}
	return clusterName
}

func buildCreateMLRunOptions(jobSet *v1alpha2.JobSet, clusterPath, runGroup string, labels, artifacts map[string]string) *hypercomputeclient.CreateMLRunOptions {
	return &hypercomputeclient.CreateMLRunOptions{
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
			CreateTime: formatCreationTime(jobSet.CreationTimestamp),
		},
	}
}

// ensureMLRunCreated creates (or, on conflict, upserts) the single MLRun tracking
// record for a ManagedXprof execution and returns its URL and whether the
// creation or upsert succeeded.
//
// Extracted to separate MLRun control-plane invocation and error handling from the
// core JobSet status synchronization workflow.
func (r *Reconciler) ensureMLRunCreated(ctx context.Context, jobSet *v1alpha2.JobSet, xid, wid, eid int64, displayName, runGroup, gcsPath string) (string, bool) {
	if r.MLRunClient == nil || jobSet == nil {
		return "", false
	}
	if jobSet.CreationTimestamp.IsZero() {
		logging.Warn(ctx, "JobSet creation timestamp is uninitialized when constructing MLRun ID", "jobset", jobSet.Name)
	}

	mlRunID := ConstructMLRunID(r.ClusterName, jobSet.Namespace, "JobSet", jobSet.Name, jobSet.CreationTimestamp.Time)
	clusterPath := resolveClusterPath(r.ClusterName, r.Project, r.Location)
	labels := buildMLRunLabels(xid, wid)
	artifacts := map[string]string{"gcs_path": gcsPath}
	opts := buildCreateMLRunOptions(jobSet, clusterPath, runGroup, labels, artifacts)

	logging.Info(ctx, "Creating MLRun for ManagedXprof execution", "mlRunID", mlRunID, "displayName", displayName, "runGroup", runGroup)
	if _, err := r.MLRunClient.CreateMLRun(ctx, mlRunID, displayName, "ACTIVE", opts); err != nil {
		if isMLRunAlreadyExistsError(err) {
			return r.upsertExistingMLRun(ctx, err, mlRunID, displayName, runGroup, labels, artifacts)
		}
		logging.Error(ctx, "Failed to create MLRun for ManagedXprof execution", "mlRunID", mlRunID, "error", err)
		return "", false
	}
	url := r.stampExistingSessionsOnlyLabel(ctx, mlRunID)
	return url, url != ""
}

// stampExistingSessionsOnlyLabel stamps list_existing_sessions_only post-creation
// to preserve the ACTIVE phase (creating with it forces COMPLETED, blocking on-demand profiling).
func (r *Reconciler) stampExistingSessionsOnlyLabel(ctx context.Context, mlRunID string) string {
	res, err := r.MLRunClient.UpdateMLRun(ctx, mlRunID, &hypercomputeclient.UpdateMLRunOptions{
		Labels: map[string]string{listExistingSessionsOnlyLabel: "true"},
	})
	if err != nil {
		logging.Error(ctx, "Failed to stamp list_existing_sessions_only on MLRun", "mlRunID", mlRunID, "error", err)
		return ""
	}
	return r.mlRunURL(res, mlRunID)
}

func resolveExistingMLRunID(createErr error, fallbackID string) string {
	if id := parseExistingMLRunID(createErr.Error()); id != "" {
		return id
	}
	return fallbackID
}

func withExistingSessionsOnlyLabel(labels map[string]string) map[string]string {
	updateLabels := maps.Clone(labels)
	if updateLabels == nil {
		updateLabels = make(map[string]string, 1)
	}
	updateLabels[listExistingSessionsOnlyLabel] = "true"
	return updateLabels
}

// upsertExistingMLRun handles an MLRun create conflict. It locates the existing run
// (its id is carried in the conflict error, falling back to our deterministic id)
// and, if the run is not one we own, updates it with our ManagedXprof fields so
// enrichment is applied even to runs created by other producers. A run we already
// own is returned as an idempotent success without an update. It returns the run
// URL and whether the run was confirmed; on GetMLRun/UpdateMLRun failure it returns
// a best-effort console URL with false so callers do not cache it.
func (r *Reconciler) upsertExistingMLRun(ctx context.Context, createErr error, mlRunID, displayName, runGroup string, labels map[string]string, artifacts map[string]string) (string, bool) {
	existingID := resolveExistingMLRunID(createErr, mlRunID)
	existing, err := r.MLRunClient.GetMLRun(ctx, existingID)
	if err != nil {
		logging.Warn(ctx, "MLRun already exists but could not be fetched; returning unconfirmed console URL fallback", "mlRunID", existingID, "error", err)
		return constructDiagnosticsConsoleURL(r.consoleLocation(), existingID, r.consoleProject()), false
	}
	if mlRunOwnedByXManager(existing) && mlRunHasLabel(existing, listExistingSessionsOnlyLabel, "true") {
		logging.Info(ctx, "MLRun already exists and is owned by XManager; treating as success", "mlRunID", existingID)
		return r.mlRunURL(existing, existingID), true
	}

	logging.Info(ctx, "Updating existing MLRun with ManagedXprof fields", "mlRunID", existingID)
	res, err := r.MLRunClient.UpdateMLRun(ctx, existingID, &hypercomputeclient.UpdateMLRunOptions{
		DisplayName: displayName,
		RunGroup:    runGroup,
		Labels:      withExistingSessionsOnlyLabel(labels),
		Artifacts:   artifacts,
	})
	if err != nil {
		logging.Error(ctx, "Failed to update existing MLRun for ManagedXprof execution", "mlRunID", existingID, "error", err)
		return constructDiagnosticsConsoleURL(r.consoleLocation(), existingID, r.consoleProject()), false
	}
	return r.mlRunURL(res, existingID), true
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
	if r.Storage == nil {
		return "", false
	}
	execution, fetchErr := r.Storage.FetchCurrentWorkUnitExecution(ctx, xid, wid)
	if fetchErr != nil {
		logging.Error(ctx, "Failed to fetch current WorkUnitExecution from ESS", "error", fetchErr)
		return "", false
	}

	mlRunURL := getMLRunURL(execution)
	if execution != nil && mlRunURL != "" && isStaleExecutionForMLRun(execution.GetName(), eid) {
		logging.Warn(ctx, "Reusing MLRun URL from a different current execution than the one being reconciled; possible re-execution race.", "reconciledEID", eid, "xid", xid, "wid", wid, "executionName", execution.GetName())
	} else if mlRunURL != "" {
		r.cacheMLRunURL(xid, wid, eid, mlRunURL)
	}

	if execution != nil && isTerminalState(execution.GetStatus().GetState()) {
		currentState := execution.GetStatus().GetState()
		logging.Info(ctx, "WorkUnit execution is in terminal/stopping state in ESS, skipping MLRun creation", "currentState", currentState.String())
		return mlRunURL, true
	}
	return mlRunURL, false
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
