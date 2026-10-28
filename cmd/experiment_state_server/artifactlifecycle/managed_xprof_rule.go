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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
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
	createdByXManagerLabelValue   = "xmanager"
	listExistingSessionsOnlyLabel = "list_existing_sessions_only"
	runPhaseActive                = "ACTIVE"
	runPhaseCompleted             = "COMPLETED"
	runPhaseFailed                = "FAILED"
)

var (
	mlRunResourceNameRE = regexp.MustCompile(`machineLearningRuns/([a-zA-Z0-9_-]+)`)
	mlRunURLSegmentRE   = regexp.MustCompile(`/runs/([a-zA-Z0-9_-]+)`)
)

func parseMLRunIDFromArtifact(artifact *essmsgpb.Artifact) string {
	if artifact == nil {
		return ""
	}
	var rawURL string
	if mx := artifact.GetPayload().GetManagedXprof(); mx != nil && mx.GetXprof() != nil {
		rawURL = mx.GetXprof().GetXprofUrl()
	}
	if rawURL == "" && artifact.GetUrl() != nil {
		rawURL = artifact.GetUrl().GetUrl()
	}
	if rawURL == "" {
		return ""
	}
	if m := mlRunURLSegmentRE.FindStringSubmatch(rawURL); len(m) >= 2 {
		return m[1]
	}
	if m := mlRunResourceNameRE.FindStringSubmatch(rawURL); len(m) >= 2 {
		return m[1]
	}
	return ""
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
func NewManagedXprofRule(mlRunClient MLRunClient, projectID, location string) LifecycleRule {
	if location == "" {
		location = "us-central1"
	}
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
	mlCtx := r.buildMLRunContext(event)
	xprofURL := r.getOrCreateMLRun(ctx, mlCtx.mlRunID, mlCtx.displayName, mlCtx.runGroup, runPhaseActive, mlCtx.createOpts)

	if xprofURL == "" {
		return nil
	}

	return r.persistArtifactURL(ctx, store, event.GetExperimentId(), event.GetWorkUnitId(), event.GetWorkUnitExecutionId(), artifact, xprofURL)
}

// handleTerminalState transitions the MLRun to COMPLETED or FAILED and backfills xprof_url if not yet stored.
func (r *ManagedXprofRule) handleTerminalState(
	ctx context.Context,
	store storage.Storage,
	event *taskqueuepb.ArtifactLifecycleEvent,
	artifact *essmsgpb.Artifact,
) error {
	mlCtx := r.buildMLRunContext(event)
	xprofURL := r.getOrCreateMLRun(ctx, mlCtx.mlRunID, mlCtx.displayName, mlCtx.runGroup, runPhaseActive, mlCtx.createOpts)

	if xprofURL != "" {
		_ = r.persistArtifactURL(ctx, store, event.GetExperimentId(), event.GetWorkUnitId(), event.GetWorkUnitExecutionId(), artifact, xprofURL)
	}

	terminalPhase := runPhaseFailed
	if event.GetNewState() == essmsgpb.WorkUnitStatus_COMPLETED || event.GetNewState() == essmsgpb.WorkUnitStatus_STOPPED {
		terminalPhase = runPhaseCompleted
	}

	if r.mlRunClient != nil {
		_, err := r.mlRunClient.UpdateMLRun(ctx, mlCtx.mlRunID, &hypercomputeclient.UpdateMLRunOptions{
			RunPhase: terminalPhase,
		})
		if err != nil {
			logging.Warn(ctx, "Failed to update MLRun to terminal phase", "mlRunID", mlCtx.mlRunID, "phase", terminalPhase, "error", err)
		}
	}

	return nil
}

// OnExecutionSuperseded closes out the external MLRun for a superseded execution.
func (r *ManagedXprofRule) OnExecutionSuperseded(
	ctx context.Context,
	store storage.Storage,
	xid, wid, oldEID int64,
	artifact *essmsgpb.Artifact,
) error {
	if r.mlRunClient == nil {
		return nil
	}
	mlRunID := parseMLRunIDFromArtifact(artifact)
	if mlRunID == "" {
		jobSetName := fmt.Sprintf("xmc-wu-%d-%d", wid, oldEID)
		mlRunID = ConstructMLRunID("", "", "JobSet", jobSetName, time.Time{})
	}

	logging.Info(ctx, "Finalizing superseded execution MLRun", "xid", xid, "wid", wid, "oldEID", oldEID, "mlRunID", mlRunID)
	_, err := r.mlRunClient.UpdateMLRun(ctx, mlRunID, &hypercomputeclient.UpdateMLRunOptions{
		RunPhase: runPhaseCompleted,
	})
	if err != nil {
		logging.Warn(ctx, "Failed to mark superseded MLRun as COMPLETED", "mlRunID", mlRunID, "error", err)
	}
	return nil
}

func parseArtifactID(name string) (int64, error) {
	aid, err := validator.ValidateArtifactName(name)
	if err == nil {
		return aid, nil
	}
	if idx := strings.LastIndex(name, "/artifacts/"); idx != -1 {
		return validator.ValidateArtifactName(name[idx+1:])
	}
	return 0, err
}

// persistArtifactURL updates the shared Artifact row inside a Spanner RW transaction,
// enforcing Shared-Artifact Execution Fencing (event.EID == CurrentWorkUnitExecution.EID).
func (r *ManagedXprofRule) persistArtifactURL(
	ctx context.Context,
	store storage.Storage,
	xid, wid, eventEID int64,
	artifact *essmsgpb.Artifact,
	xprofURL string,
) error {
	aid, err := parseArtifactID(artifact.GetName())
	if err != nil {
		return fmt.Errorf("invalid artifact name %q: %w", artifact.GetName(), err)
	}

	return store.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		fenced, err := isExecutionSupersededInTx(ctx, tx, xid, wid, eventEID)
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

func isExecutionSupersededInTx(ctx context.Context, tx *storage.Transaction, xid, wid, eventEID int64) (bool, error) {
	wu, err := tx.WorkUnits.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		return false, fmt.Errorf("failed to fetch work unit %d: %w", wid, err)
	}
	if cur := wu.GetCurrentWorkUnitExecution(); cur != nil {
		if _, _, curEID, err := validator.ValidateWorkUnitExecutionName(cur.GetName()); err == nil && curEID != eventEID {
			logging.Info(ctx, "Skipping artifact xprof_url update; execution is superseded", "eventEID", eventEID, "curEID", curEID)
			return true, nil
		}
	}
	return false, nil
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

func (r *ManagedXprofRule) getOrCreateMLRun(
	ctx context.Context,
	mlRunID, displayName, runGroup, initialPhase string,
	opts *hypercomputeclient.CreateMLRunOptions,
) string {
	if r.mlRunClient == nil {
		return r.mlRunURL(nil, mlRunID)
	}

	_, err := r.mlRunClient.CreateMLRun(ctx, mlRunID, displayName, initialPhase, opts)
	if err == nil {
		// Stamp list_existing_sessions_only post-creation to preserve ACTIVE phase.
		res, err := r.mlRunClient.UpdateMLRun(ctx, mlRunID, &hypercomputeclient.UpdateMLRunOptions{
			Labels: map[string]string{listExistingSessionsOnlyLabel: "true"},
		})
		if err != nil {
			logging.Warn(ctx, "Failed to stamp list_existing_sessions_only label on MLRun", "mlRunID", mlRunID, "error", err)
		}
		return r.mlRunURL(res, mlRunID)
	}

	if isMLRunAlreadyExistsError(err) {
		existingID := resolveExistingMLRunID(err, mlRunID)
		existing, getErr := r.mlRunClient.GetMLRun(ctx, existingID)
		if getErr != nil {
			logging.Warn(ctx, "MLRun already exists but could not be fetched", "mlRunID", existingID, "error", getErr)
			return r.mlRunURL(nil, existingID)
		}
		if mlRunOwnedByXManager(existing) && mlRunHasLabel(existing, listExistingSessionsOnlyLabel, "true") {
			return r.mlRunURL(existing, existingID)
		}

		updRes, updErr := r.mlRunClient.UpdateMLRun(ctx, existingID, &hypercomputeclient.UpdateMLRunOptions{
			DisplayName: displayName,
			RunGroup:    runGroup,
			Labels:      map[string]string{listExistingSessionsOnlyLabel: "true", "created_by": createdByXManagerLabelValue},
		})
		if updErr != nil {
			logging.Warn(ctx, "Failed to update existing MLRun", "mlRunID", existingID, "error", updErr)
			return r.mlRunURL(existing, existingID)
		}
		return r.mlRunURL(updRes, existingID)
	}

	logging.Error(ctx, "CreateMLRun failed", "mlRunID", mlRunID, "error", err)
	return r.mlRunURL(nil, mlRunID)
}

func (r *ManagedXprofRule) buildMLRunContext(event *taskqueuepb.ArtifactLifecycleEvent) mlRunContext {
	rt := event.GetJobSetRuntimeContext()
	cluster := rt.GetCluster()
	namespace := rt.GetNamespace()
	jobSetName := rt.GetJobSetName()
	if jobSetName == "" {
		jobSetName = fmt.Sprintf("xmc-wu-%d-%d", event.GetWorkUnitId(), event.GetWorkUnitExecutionId())
	}
	var creationTime time.Time
	if rt.GetCreationTime() != nil {
		creationTime = rt.GetCreationTime().AsTime()
	}

	mlRunID := ConstructMLRunID(cluster, namespace, "JobSet", jobSetName, creationTime)
	envVars := rt.GetEnvironmentVariables()

	displayName := envVars[constants.XmcEnvVarManagedXprofDisplayName]
	if displayName == "" {
		displayName = jobSetName
	}
	runGroup := envVars[constants.XmcEnvVarManagedXprofRunGroup]

	labels := buildMLRunLabels(event.GetExperimentId(), event.GetWorkUnitId())
	artifacts := make(map[string]string)
	if folder := envVars[constants.XmcEnvVarManagedXprofGCSObjectFolder]; folder != "" {
		artifacts["xprof_gcs_path"] = folder
	}

	opts := &hypercomputeclient.CreateMLRunOptions{
		RunGroup:     runGroup,
		Labels:       labels,
		Artifacts:    artifacts,
		Orchestrator: "GKE",
		WorkloadDetails: &hypercomputeclient.GKEWorkloadDetails{
			ID:        jobSetName,
			Kind:      "JobSet",
			Cluster:   cluster,
			Namespace: namespace,
		},
	}
	return mlRunContext{
		mlRunID:     mlRunID,
		displayName: displayName,
		runGroup:    runGroup,
		createOpts:  opts,
	}
}

func (r *ManagedXprofRule) mlRunURL(res map[string]any, mlRunID string) string {
	if res != nil {
		if meta, ok := res["metadata"].(map[string]any); ok {
			if u, ok := meta["url"].(string); ok && u != "" {
				return ensureProfilesTab(u)
			}
		}
	}
	return constructDiagnosticsConsoleURL(r.location, mlRunID, r.projectID)
}

func buildMLRunLabels(xid, wid int64) map[string]string {
	return map[string]string{
		"experiment_id": fmt.Sprintf("%d", xid),
		"workunit_id":   fmt.Sprintf("%d", wid),
		"created_by":    createdByXManagerLabelValue,
	}
}

func normalizeClusterName(cluster string) string {
	cluster = strings.TrimRight(cluster, "/")
	if idx := strings.LastIndex(cluster, "/"); idx != -1 {
		return cluster[idx+1:]
	}
	return cluster
}

func formatMLRunIdentifierTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(constants.MLRunIdentifierTimeFormat)
}

// ConstructMLRunRawIdentifier assembles the deterministic raw identifier string for a GKE workload.
func ConstructMLRunRawIdentifier(cluster, namespace, kind, jobSetName string, creationTime time.Time) string {
	clusterName := normalizeClusterName(cluster)
	timestamp := formatMLRunIdentifierTimestamp(creationTime)
	if timestamp == "" {
		return fmt.Sprintf("%s_%s_%s_%s", clusterName, namespace, kind, jobSetName)
	}
	return fmt.Sprintf("%s_%s_%s_%s_%s", clusterName, namespace, kind, jobSetName, timestamp)
}

// ConstructMLRunID deterministically constructs the SHA-256 hex digest of the raw workload identifier.
func ConstructMLRunID(cluster, namespace, kind, jobSetName string, creationTime time.Time) string {
	rawID := ConstructMLRunRawIdentifier(cluster, namespace, kind, jobSetName, creationTime)
	hash := sha256.Sum256([]byte(rawID))
	return hex.EncodeToString(hash[:])
}

func isMLRunAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
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

func parseExistingMLRunID(errText string) string {
	m := mlRunResourceNameRE.FindStringSubmatch(errText)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func resolveExistingMLRunID(createErr error, fallbackID string) string {
	if id := parseExistingMLRunID(createErr.Error()); id != "" {
		return id
	}
	return fallbackID
}

func mlRunHasLabel(mlRun map[string]any, key, value string) bool {
	labels, ok := mlRun["labels"].(map[string]any)
	if !ok {
		return false
	}
	v, ok := labels[key].(string)
	return ok && v == value
}

func mlRunOwnedByXManager(mlRun map[string]any) bool {
	return mlRunHasLabel(mlRun, "created_by", createdByXManagerLabelValue)
}

func ensureProfilesTab(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := parsed.Query()
	q.Set("tab", "profiles")
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func constructDiagnosticsConsoleURL(location, mlRunID, project string) string {
	rawURL := fmt.Sprintf("https://console.cloud.google.com/cluster-director/locations/%s/runs/%s?project=%s", location, mlRunID, project)
	return ensureProfilesTab(rawURL)
}
