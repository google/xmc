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

package artifactlifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/hypercomputeclient"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

const (
	runPhaseActive    = "ACTIVE"
	runPhaseCompleted = "COMPLETED"
	runPhaseFailed    = "FAILED"
)

// MLRunClient defines the external Hypercompute operations required by ManagedXprofRule.
type MLRunClient interface {
	CreateMLRun(ctx context.Context, name, displayName, runPhase string, opts *hypercomputeclient.CreateMLRunOptions) (map[string]any, error)
	GetMLRun(ctx context.Context, name string) (map[string]any, error)
	UpdateMLRun(ctx context.Context, name string, opts *hypercomputeclient.UpdateMLRunOptions) (map[string]any, error)
}

// ManagedXprofRule governs ManagedXprof artifacts and manages backing Hypercompute MLRun resources.
type ManagedXprofRule struct {
	mlRunClient MLRunClient
	projectID   string
	location    string
}

// NewManagedXprofRule constructs a declarative LifecycleRule for ManagedXprof artifacts.
//
// projectID and location must be the resolved MLRun project and location (the same
// values the MLRun client was built with); they are used to construct console URLs.
func NewManagedXprofRule(mlRunClient MLRunClient, projectID, location string) LifecycleRule {
	r := &ManagedXprofRule{
		mlRunClient: mlRunClient,
		projectID:   projectID,
		location:    location,
	}
	return LifecycleRule{
		Name:    "ManagedXprofRule",
		Matches: r.Matches,
		TargetExecutionStates: []essmsgpb.WorkUnitStatus_WorkUnitState{
			essmsgpb.WorkUnitStatus_STARTING,
			essmsgpb.WorkUnitStatus_RUNNING,
			essmsgpb.WorkUnitStatus_COMPLETED,
			essmsgpb.WorkUnitStatus_FAILED,
			essmsgpb.WorkUnitStatus_STOPPED,
		},
		OnExecutionStateChanged: r.OnExecutionStateChanged,
		OnExecutionSuperseded:   r.OnExecutionSuperseded,
	}
}

// Matches returns true if the artifact contains a ManagedXprof payload.
func (r *ManagedXprofRule) Matches(artifact *essmsgpb.Artifact) bool {
	if artifact == nil {
		return false
	}
	return artifact.GetPayload().GetManagedXprof() != nil
}

// ErrMLRunClientUnavailable is returned when the rule is registered (lifecycle flag
// on) but the MLRun client failed to initialize. The event is retried and eventually
// dead-lettered instead of being acknowledged with a fabricated, unbacked URL.
var ErrMLRunClientUnavailable = errors.New("mlrun client is not configured")

// OnExecutionStateChanged handles state transitions for ManagedXprof artifacts.
// Hypercompute and storage failures are returned so the lifecycle worker retries the
// event; every step is idempotent, so a retry safely re-runs the whole handler.
func (r *ManagedXprofRule) OnExecutionStateChanged(
	ctx context.Context,
	store storage.Storage,
	event *taskqueuepb.ArtifactLifecycleEvent,
	artifact *essmsgpb.Artifact,
) error {
	if event == nil || artifact == nil {
		return nil
	}
	if r.mlRunClient == nil {
		return ErrMLRunClientUnavailable
	}

	switch event.GetNewState() {
	case essmsgpb.WorkUnitStatus_STARTING, essmsgpb.WorkUnitStatus_RUNNING:
		return r.handleActiveState(ctx, store, event, artifact)
	case essmsgpb.WorkUnitStatus_COMPLETED, essmsgpb.WorkUnitStatus_FAILED, essmsgpb.WorkUnitStatus_STOPPED:
		return r.handleTerminalState(ctx, store, event, artifact)
	default:
		return nil
	}
}

type mlRunContext struct {
	mlRunID     string
	displayName string
	runGroup    string
	createOpts  *hypercomputeclient.CreateMLRunOptions
}

// handleActiveState ensures the execution's MLRun exists and records its URL. When the
// event's execution is already superseded, the MLRun is closed immediately instead.
func (r *ManagedXprofRule) handleActiveState(
	ctx context.Context,
	store storage.Storage,
	event *taskqueuepb.ArtifactLifecycleEvent,
	artifact *essmsgpb.Artifact,
) error {
	mlCtx, ok := buildMLRunContext(event)
	if !ok {
		logging.Warn(ctx, "Skipping MLRun creation; lifecycle event lacks JobSet runtime context", "xid", event.GetExperimentId(), "wid", event.GetWorkUnitId(), "eid", event.GetWorkUnitExecutionId())
		return nil
	}
	xprofURL, err := r.getOrCreateMLRun(ctx, mlCtx)
	if err != nil {
		return err
	}
	superseded, err := r.persistMLRunURL(ctx, store, event, artifact, xprofURL)
	if err != nil || !superseded {
		return err
	}
	logging.Info(ctx, "Closing MLRun of superseded execution", "xid", event.GetExperimentId(), "wid", event.GetWorkUnitId(), "eid", event.GetWorkUnitExecutionId(), "mlRunID", mlCtx.mlRunID)
	return r.updateRunPhase(ctx, mlCtx.mlRunID, runPhaseCompleted)
}

// handleTerminalState transitions the MLRun to COMPLETED or FAILED. When the event
// carries the JobSet runtime context it also ensures the MLRun exists and records its
// URL; otherwise it finalizes the MLRun recorded on the event's execution, if any.
func (r *ManagedXprofRule) handleTerminalState(
	ctx context.Context,
	store storage.Storage,
	event *taskqueuepb.ArtifactLifecycleEvent,
	artifact *essmsgpb.Artifact,
) error {
	phase := terminalRunPhase(event.GetNewState())
	mlCtx, ok := buildMLRunContext(event)
	if !ok {
		return r.finalizeRecordedMLRun(ctx, store, event, phase)
	}
	xprofURL, err := r.getOrCreateMLRun(ctx, mlCtx)
	if err != nil {
		return err
	}
	if _, err := r.persistMLRunURL(ctx, store, event, artifact, xprofURL); err != nil {
		return err
	}
	return r.updateRunPhase(ctx, mlCtx.mlRunID, phase)
}

// finalizeRecordedMLRun transitions the MLRun recorded on the event execution's
// metadata.mlrun_url to phase. Executions without a recorded MLRun are skipped.
func (r *ManagedXprofRule) finalizeRecordedMLRun(ctx context.Context, store storage.Storage, event *taskqueuepb.ArtifactLifecycleEvent, phase string) error {
	xid, wid, eid := event.GetExperimentId(), event.GetWorkUnitId(), event.GetWorkUnitExecutionId()
	mlRunID, err := executionMLRunID(ctx, store, xid, wid, eid)
	if err != nil {
		return err
	}
	if mlRunID == "" {
		logging.Warn(ctx, "Skipping MLRun finalization; no runtime context and no recorded MLRun URL", "xid", xid, "wid", wid, "eid", eid)
		return nil
	}
	return r.updateRunPhase(ctx, mlRunID, phase)
}

func terminalRunPhase(state essmsgpb.WorkUnitStatus_WorkUnitState) string {
	if state == essmsgpb.WorkUnitStatus_COMPLETED || state == essmsgpb.WorkUnitStatus_STOPPED {
		return runPhaseCompleted
	}
	return runPhaseFailed
}

// updateRunPhase sets the MLRun phase. Re-applying the current phase and the client's
// terminal-to-ACTIVE downgrade guard are no-ops that succeed, so retries are safe.
func (r *ManagedXprofRule) updateRunPhase(ctx context.Context, mlRunID, phase string) error {
	if _, err := r.mlRunClient.UpdateMLRun(ctx, mlRunID, &hypercomputeclient.UpdateMLRunOptions{RunPhase: phase}); err != nil {
		return fmt.Errorf("update MLRun %q to phase %s: %w", mlRunID, phase, err)
	}
	return nil
}

// OnExecutionSuperseded closes exactly the MLRun recorded on the superseded
// execution's metadata.mlrun_url. Executions without a recorded MLRun are skipped.
// The caller (the synchronous restart path) logs a returned error and never fails
// the restart on it.
func (r *ManagedXprofRule) OnExecutionSuperseded(
	ctx context.Context,
	store storage.Storage,
	xid, wid, oldEID int64,
	_ *essmsgpb.Artifact,
) error {
	if r.mlRunClient == nil {
		return ErrMLRunClientUnavailable
	}
	mlRunID, err := executionMLRunID(ctx, store, xid, wid, oldEID)
	if err != nil {
		return err
	}
	if mlRunID == "" {
		logging.Info(ctx, "Skipping superseded execution MLRun finalization; execution has no recorded MLRun URL", "xid", xid, "wid", wid, "oldEID", oldEID)
		return nil
	}
	logging.Info(ctx, "Finalizing superseded execution MLRun", "xid", xid, "wid", wid, "oldEID", oldEID, "mlRunID", mlRunID)
	if err := r.updateRunPhase(ctx, mlRunID, runPhaseCompleted); err != nil {
		logging.Warn(ctx, "Failed to close superseded execution MLRun", "xid", xid, "wid", wid, "oldEID", oldEID, "mlRunID", mlRunID, "error", err)
		return err
	}
	return nil
}

// persistMLRunURL records xprofURL inside one Spanner RW transaction. It reports
// superseded=true, writing nothing, when the event's execution is no longer current
// (Shared-Artifact Execution Fencing). Otherwise it records the URL on the event
// execution's metadata.mlrun_url and on the shared Artifact row.
func (r *ManagedXprofRule) persistMLRunURL(
	ctx context.Context,
	store storage.Storage,
	event *taskqueuepb.ArtifactLifecycleEvent,
	artifact *essmsgpb.Artifact,
	xprofURL string,
) (bool, error) {
	aid, err := validator.ParseArtifactIDFromName(artifact.GetName())
	if err != nil {
		return false, fmt.Errorf("invalid artifact name %q: %w", artifact.GetName(), err)
	}
	xid, wid, eid := event.GetExperimentId(), event.GetWorkUnitId(), event.GetWorkUnitExecutionId()

	var superseded bool
	err = store.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		wu, err := tx.WorkUnits.FetchWorkUnit(ctx, xid, wid)
		if err != nil {
			return fmt.Errorf("failed to fetch work unit %d: %w", wid, err)
		}
		superseded = isExecutionSuperseded(ctx, wu, eid)
		if superseded {
			return nil
		}
		if err := recordExecutionMLRunURLInTx(ctx, tx, xid, wid, eid, wu.GetCurrentWorkUnitExecution(), xprofURL); err != nil {
			return err
		}
		return writeArtifactXprofURLInTx(ctx, tx, aid, xprofURL)
	})
	return superseded, err
}

// writeArtifactXprofURLInTx sets the shared Artifact row's url and xprof_url,
// skipping the write when both already hold xprofURL.
func writeArtifactXprofURLInTx(ctx context.Context, tx *storage.Transaction, aid int64, xprofURL string) error {
	currentArt, err := tx.Artifacts.FetchArtifact(ctx, aid)
	if err != nil {
		return fmt.Errorf("failed to fetch artifact %d in transaction: %w", aid, err)
	}
	if currentArt.GetUrl().GetUrl() == xprofURL &&
		currentArt.GetPayload().GetManagedXprof().GetXprof().GetXprofUrl() == xprofURL {
		return nil
	}
	_, err = tx.Artifacts.UpdateArtifact(ctx, &storage.ArtifactUpdate{
		AID:        aid,
		Artifact:   buildArtifactWithXprofURL(currentArt, xprofURL),
		UpdateMask: &fmpb.FieldMask{Paths: []string{"url", "payload"}},
	})
	return err
}

func buildArtifactWithXprofURL(currentArt *essmsgpb.Artifact, xprofURL string) *essmsgpb.Artifact {
	var updatedURL *essmsgpb.Url
	if currentArt.GetUrl() != nil {
		updatedURL = proto.Clone(currentArt.GetUrl()).(*essmsgpb.Url)
	} else {
		updatedURL = &essmsgpb.Url{}
	}
	updatedURL.Url = xprofURL

	var updatedPayload *essmsgpb.ArtifactPayload
	if currentArt.GetPayload() != nil {
		updatedPayload = proto.Clone(currentArt.GetPayload()).(*essmsgpb.ArtifactPayload)
	} else {
		updatedPayload = &essmsgpb.ArtifactPayload{}
	}
	if mx := updatedPayload.GetManagedXprof(); mx != nil {
		mx.Xprof = &essmsgpb.XProfUIInstance{XprofUrl: xprofURL}
	} else {
		updatedPayload.Type = &essmsgpb.ArtifactPayload_ManagedXprof{
			ManagedXprof: &essmsgpb.ManagedXprofArtifact{
				Xprof: &essmsgpb.XProfUIInstance{XprofUrl: xprofURL},
			},
		}
	}

	return &essmsgpb.Artifact{
		Url:     updatedURL,
		Payload: updatedPayload,
	}
}

// getOrCreateMLRun creates the MLRun (or, on AlreadyExists, adopts the existing one)
// and returns the URL to persist. It returns an error, and never a URL, when the
// MLRun could not be created or adopted.
func (r *ManagedXprofRule) getOrCreateMLRun(ctx context.Context, mlCtx mlRunContext) (string, error) {
	_, err := r.mlRunClient.CreateMLRun(ctx, mlCtx.mlRunID, mlCtx.displayName, runPhaseActive, mlCtx.createOpts)
	if err == nil {
		return r.stampExistingSessionsOnlyLabel(ctx, mlCtx.mlRunID)
	}
	if hypercomputeclient.IsAlreadyExists(err) {
		return r.adoptExistingMLRun(ctx, err, mlCtx)
	}
	return "", fmt.Errorf("create MLRun %q: %w", mlCtx.mlRunID, err)
}

// stampExistingSessionsOnlyLabel stamps list_existing_sessions_only post-creation to
// preserve the ACTIVE phase (creating with it forces COMPLETED, blocking on-demand
// profiling). On failure the retried event re-enters through CreateMLRun ->
// AlreadyExists -> adoptExistingMLRun, which re-stamps the label because adoption is
// incomplete until the label is present.
func (r *ManagedXprofRule) stampExistingSessionsOnlyLabel(ctx context.Context, mlRunID string) (string, error) {
	res, err := r.mlRunClient.UpdateMLRun(ctx, mlRunID, &hypercomputeclient.UpdateMLRunOptions{
		Labels: map[string]string{hypercomputeclient.ListExistingSessionsOnlyLabel: "true"},
	})
	if err != nil {
		return "", fmt.Errorf("stamp %s label on MLRun %q: %w", hypercomputeclient.ListExistingSessionsOnlyLabel, mlRunID, err)
	}
	return r.mlRunURL(res, mlRunID), nil
}

// adoptExistingMLRun handles an MLRun create conflict: a run already owned by
// XManager is returned as-is; any other run is updated with our ManagedXprof fields.
func (r *ManagedXprofRule) adoptExistingMLRun(ctx context.Context, createErr error, mlCtx mlRunContext) (string, error) {
	existingID := hypercomputeclient.ResolveExistingMLRunID(createErr, mlCtx.mlRunID)
	existing, err := r.mlRunClient.GetMLRun(ctx, existingID)
	if err != nil {
		return "", fmt.Errorf("get existing MLRun %q: %w", existingID, err)
	}
	if hypercomputeclient.MLRunAdoptionComplete(existing) {
		return r.mlRunURL(existing, existingID), nil
	}

	res, err := r.mlRunClient.UpdateMLRun(ctx, existingID, &hypercomputeclient.UpdateMLRunOptions{
		DisplayName: mlCtx.displayName,
		RunGroup:    mlCtx.runGroup,
		Labels:      hypercomputeclient.WithExistingSessionsOnlyLabel(mlCtx.createOpts.Labels),
		Artifacts:   mlCtx.createOpts.Artifacts,
	})
	if err != nil {
		return "", fmt.Errorf("adopt existing MLRun %q: %w", existingID, err)
	}
	return r.mlRunURL(res, existingID), nil
}

// buildMLRunContext derives the MLRun identity and create options from the event's
// JobSet runtime context. It reports false when the context (or its JobSet name) is
// missing, because the MLRun id cannot then be derived consistently with the reconciler.
func buildMLRunContext(event *taskqueuepb.ArtifactLifecycleEvent) (mlRunContext, bool) {
	rt := event.GetJobSetRuntimeContext()
	jobSetName := rt.GetJobSetName()
	if jobSetName == "" {
		return mlRunContext{}, false
	}
	var creationTime time.Time
	if rt.GetCreationTime() != nil {
		creationTime = rt.GetCreationTime().AsTime()
	}

	envVars := rt.GetEnvironmentVariables()
	displayName := envVars[constants.XmcEnvVarManagedXprofDisplayName]
	if displayName == "" {
		displayName = jobSetName
	}
	runGroup := envVars[constants.XmcEnvVarManagedXprofRunGroup]
	artifacts := make(map[string]string)
	if folder := envVars[constants.XmcEnvVarManagedXprofGCSObjectFolder]; folder != "" {
		artifacts["xprof_gcs_path"] = folder
	}

	return mlRunContext{
		mlRunID:     hypercomputeclient.ConstructMLRunID(rt.GetCluster(), rt.GetNamespace(), "JobSet", jobSetName, creationTime),
		displayName: displayName,
		runGroup:    runGroup,
		createOpts: &hypercomputeclient.CreateMLRunOptions{
			RunGroup:     runGroup,
			Labels:       hypercomputeclient.BuildMLRunLabels(event.GetExperimentId(), event.GetWorkUnitId()),
			Artifacts:    artifacts,
			Orchestrator: "GKE",
			WorkloadDetails: &hypercomputeclient.GKEWorkloadDetails{
				ID:        jobSetName,
				Kind:      "JobSet",
				Cluster:   rt.GetCluster(),
				Namespace: rt.GetNamespace(),
			},
		},
	}, true
}

func (r *ManagedXprofRule) mlRunURL(mlRun map[string]any, mlRunID string) string {
	return hypercomputeclient.MLRunURL(mlRun, r.location, mlRunID, r.projectID)
}
