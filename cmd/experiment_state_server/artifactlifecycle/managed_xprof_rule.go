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

// storedMLRunID returns the MLRun id recorded on the artifact's xprof_url (or
// url), or "" when no MLRun URL has been persisted yet.
func storedMLRunID(artifact *essmsgpb.Artifact) string {
	rawURL := artifact.GetPayload().GetManagedXprof().GetXprof().GetXprofUrl()
	if rawURL == "" {
		rawURL = artifact.GetUrl().GetUrl()
	}
	return hypercomputeclient.ParseMLRunIDFromURL(rawURL)
}

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

// OnExecutionStateChanged handles state transitions for ManagedXprof artifacts.
func (r *ManagedXprofRule) OnExecutionStateChanged(
	ctx context.Context,
	store storage.Storage,
	event *taskqueuepb.ArtifactLifecycleEvent,
	artifact *essmsgpb.Artifact,
) error {
	if event == nil || artifact == nil {
		return nil
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

// handleActiveState initializes the MLRun and persists xprof_url onto the Artifact row.
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
	xprofURL := r.getOrCreateMLRun(ctx, mlCtx)
	if xprofURL == "" {
		return nil
	}
	return r.persistArtifactURL(ctx, store, event, artifact, xprofURL)
}

// handleTerminalState transitions the MLRun to COMPLETED or FAILED. When the event
// carries the JobSet runtime context it also ensures the MLRun exists and backfills
// xprof_url; otherwise it finalizes the MLRun recorded on the artifact, if any.
func (r *ManagedXprofRule) handleTerminalState(
	ctx context.Context,
	store storage.Storage,
	event *taskqueuepb.ArtifactLifecycleEvent,
	artifact *essmsgpb.Artifact,
) error {
	mlRunID := storedMLRunID(artifact)
	if mlCtx, ok := buildMLRunContext(event); ok {
		mlRunID = mlCtx.mlRunID
		r.backfillArtifactURL(ctx, store, event, artifact, mlCtx)
	}
	if mlRunID == "" {
		logging.Warn(ctx, "Skipping MLRun finalization; no runtime context and no stored MLRun URL", "xid", event.GetExperimentId(), "wid", event.GetWorkUnitId(), "eid", event.GetWorkUnitExecutionId())
		return nil
	}
	r.updateRunPhase(ctx, mlRunID, terminalRunPhase(event.GetNewState()))
	return nil
}

// backfillArtifactURL ensures the MLRun exists and persists its URL, logging failures
// so terminal-phase finalization still proceeds.
func (r *ManagedXprofRule) backfillArtifactURL(
	ctx context.Context,
	store storage.Storage,
	event *taskqueuepb.ArtifactLifecycleEvent,
	artifact *essmsgpb.Artifact,
	mlCtx mlRunContext,
) {
	xprofURL := r.getOrCreateMLRun(ctx, mlCtx)
	if xprofURL == "" {
		return
	}
	if err := r.persistArtifactURL(ctx, store, event, artifact, xprofURL); err != nil {
		logging.Warn(ctx, "Failed to backfill xprof_url on terminal state", "artifact", artifact.GetName(), "error", err)
	}
}

func terminalRunPhase(state essmsgpb.WorkUnitStatus_WorkUnitState) string {
	if state == essmsgpb.WorkUnitStatus_COMPLETED || state == essmsgpb.WorkUnitStatus_STOPPED {
		return runPhaseCompleted
	}
	return runPhaseFailed
}

func (r *ManagedXprofRule) updateRunPhase(ctx context.Context, mlRunID, phase string) {
	if r.mlRunClient == nil {
		return
	}
	_, err := r.mlRunClient.UpdateMLRun(ctx, mlRunID, &hypercomputeclient.UpdateMLRunOptions{RunPhase: phase})
	if err != nil {
		logging.Warn(ctx, "Failed to update MLRun phase", "mlRunID", mlRunID, "phase", phase, "error", err)
	}
}

// OnExecutionSuperseded closes out the external MLRun recorded on the artifact for a
// superseded execution. Artifacts without a stored MLRun URL are skipped: the MLRun
// id cannot be derived without the superseded JobSet's runtime context.
func (r *ManagedXprofRule) OnExecutionSuperseded(
	ctx context.Context,
	_ storage.Storage,
	xid, wid, oldEID int64,
	artifact *essmsgpb.Artifact,
) error {
	if r.mlRunClient == nil {
		return nil
	}
	mlRunID := storedMLRunID(artifact)
	if mlRunID == "" {
		logging.Info(ctx, "Skipping superseded execution MLRun finalization; artifact has no stored MLRun URL", "xid", xid, "wid", wid, "oldEID", oldEID, "artifact", artifact.GetName())
		return nil
	}
	logging.Info(ctx, "Finalizing superseded execution MLRun", "xid", xid, "wid", wid, "oldEID", oldEID, "mlRunID", mlRunID)
	r.updateRunPhase(ctx, mlRunID, runPhaseCompleted)
	return nil
}

// persistArtifactURL updates the shared Artifact row inside a Spanner RW transaction,
// enforcing Shared-Artifact Execution Fencing (event.EID == CurrentWorkUnitExecution.EID).
func (r *ManagedXprofRule) persistArtifactURL(
	ctx context.Context,
	store storage.Storage,
	event *taskqueuepb.ArtifactLifecycleEvent,
	artifact *essmsgpb.Artifact,
	xprofURL string,
) error {
	aid, err := validator.ParseArtifactIDFromName(artifact.GetName())
	if err != nil {
		return fmt.Errorf("invalid artifact name %q: %w", artifact.GetName(), err)
	}

	return store.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		fenced, err := isExecutionSupersededInTx(ctx, tx, event.GetExperimentId(), event.GetWorkUnitId(), event.GetWorkUnitExecutionId())
		if err != nil || fenced {
			return err
		}

		currentArt, err := tx.Artifacts.FetchArtifact(ctx, aid)
		if err != nil {
			return fmt.Errorf("failed to fetch artifact %d in transaction: %w", aid, err)
		}
		if currentArt.GetUrl().GetUrl() == xprofURL &&
			currentArt.GetPayload().GetManagedXprof().GetXprof().GetXprofUrl() == xprofURL {
			return nil
		}

		update := &storage.ArtifactUpdate{
			AID:        aid,
			Artifact:   buildArtifactWithXprofURL(currentArt, xprofURL),
			UpdateMask: &fmpb.FieldMask{Paths: []string{"url", "payload"}},
		}
		_, err = tx.Artifacts.UpdateArtifact(ctx, update)
		return err
	})
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

// getOrCreateMLRun creates the MLRun (or, on conflict, adopts the existing one) and
// returns the URL to persist on the artifact.
func (r *ManagedXprofRule) getOrCreateMLRun(ctx context.Context, mlCtx mlRunContext) string {
	if r.mlRunClient == nil {
		return r.mlRunURL(nil, mlCtx.mlRunID)
	}

	_, err := r.mlRunClient.CreateMLRun(ctx, mlCtx.mlRunID, mlCtx.displayName, runPhaseActive, mlCtx.createOpts)
	if err == nil {
		return r.stampExistingSessionsOnlyLabel(ctx, mlCtx.mlRunID)
	}
	if hypercomputeclient.IsAlreadyExists(err) {
		return r.adoptExistingMLRun(ctx, err, mlCtx)
	}
	logging.Error(ctx, "CreateMLRun failed", "mlRunID", mlCtx.mlRunID, "error", err)
	return r.mlRunURL(nil, mlCtx.mlRunID)
}

// stampExistingSessionsOnlyLabel stamps list_existing_sessions_only post-creation to
// preserve the ACTIVE phase (creating with it forces COMPLETED, blocking on-demand profiling).
func (r *ManagedXprofRule) stampExistingSessionsOnlyLabel(ctx context.Context, mlRunID string) string {
	res, err := r.mlRunClient.UpdateMLRun(ctx, mlRunID, &hypercomputeclient.UpdateMLRunOptions{
		Labels: map[string]string{hypercomputeclient.ListExistingSessionsOnlyLabel: "true"},
	})
	if err != nil {
		logging.Warn(ctx, "Failed to stamp list_existing_sessions_only label on MLRun", "mlRunID", mlRunID, "error", err)
	}
	return r.mlRunURL(res, mlRunID)
}

// adoptExistingMLRun handles an MLRun create conflict: a run already owned by
// XManager is returned as-is; any other run is updated with our ManagedXprof fields.
func (r *ManagedXprofRule) adoptExistingMLRun(ctx context.Context, createErr error, mlCtx mlRunContext) string {
	existingID := hypercomputeclient.ResolveExistingMLRunID(createErr, mlCtx.mlRunID)
	existing, err := r.mlRunClient.GetMLRun(ctx, existingID)
	if err != nil {
		logging.Warn(ctx, "MLRun already exists but could not be fetched", "mlRunID", existingID, "error", err)
		return r.mlRunURL(nil, existingID)
	}
	if hypercomputeclient.MLRunAdoptionComplete(existing) {
		return r.mlRunURL(existing, existingID)
	}

	res, err := r.mlRunClient.UpdateMLRun(ctx, existingID, &hypercomputeclient.UpdateMLRunOptions{
		DisplayName: mlCtx.displayName,
		RunGroup:    mlCtx.runGroup,
		Labels:      hypercomputeclient.WithExistingSessionsOnlyLabel(mlCtx.createOpts.Labels),
		Artifacts:   mlCtx.createOpts.Artifacts,
	})
	if err != nil {
		logging.Warn(ctx, "Failed to update existing MLRun", "mlRunID", existingID, "error", err)
		return r.mlRunURL(existing, existingID)
	}
	return r.mlRunURL(res, existingID)
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
