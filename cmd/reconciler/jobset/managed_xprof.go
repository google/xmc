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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/hypercomputeclient"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
)

// createdByXManagerLabelValue marks an MLRun as owned by the XManager reconciler.
// On a create conflict, a run carrying created_by equal to this value is treated as
// our own (idempotent success); a run without it is adopted (updated) with our fields.
const createdByXManagerLabelValue = "xmanager"

// listExistingSessionsOnlyLabel tells Diagon to scan the run's GCS folder for profiles.
// It is applied via UpdateMLRun post-creation because setting it at create time forces
// the run directly to COMPLETED stage, preventing on-demand profiling.
const listExistingSessionsOnlyLabel = "list_existing_sessions_only"

// artifactListPageSize bounds the single ListArtifacts page read by
// lookupManagedXprofArtifact. At most one ManagedXprof artifact is expected per
// work unit, so a small page is sufficient.
const artifactListPageSize = 10

// managedXprofArtifactFilter restricts ListArtifacts to ManagedXprof artifacts
// server-side (via the payload_type schema filter field), so the reconciler no
// longer fetches and discards unrelated artifact types while searching.
const managedXprofArtifactFilter = `payload_type = "managed_xprof"`

// mlRunResourceNameRE captures the MLRun id embedded in an AlreadyExists error's
// resource name, e.g. "projects/p/locations/l/machineLearningRuns/some-id".
var mlRunResourceNameRE = regexp.MustCompile(`machineLearningRuns/([a-zA-Z0-9_-]+)`)

// buildMLRunLabels returns the labels stamped on every MLRun the reconciler owns.
// Extracted so create and upsert apply an identical label set (including the
// created_by marker that lets us detect ownership on a create conflict).
func buildMLRunLabels(xid, wid int64) map[string]string {
	return map[string]string{
		"experiment_id": fmt.Sprintf("%d", xid),
		"workunit_id":   fmt.Sprintf("%d", wid),
		"created_by":    createdByXManagerLabelValue,
	}
}

// normalizeClusterName extracts the short cluster name from either a bare name or a full GCP resource path.
//
// Extracted to isolate resource path parsing from identifier formatting and ensure parity with the
// Python ML Diagnostics SDK's cluster name extraction.
func normalizeClusterName(cluster string) string {
	cluster = strings.TrimRight(cluster, "/")
	if idx := strings.LastIndex(cluster, "/"); idx != -1 {
		return cluster[idx+1:]
	}
	return cluster
}

// formatMLRunIdentifierTimestamp returns the creation timestamp formatted for the raw MLRun identifier,
// or an empty string if uninitialized.
//
// Extracted so timestamp timezone normalization and zero-value handling can be tested independently
// from hash computation.
func formatMLRunIdentifierTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(constants.MLRunIdentifierTimeFormat)
}

// ConstructMLRunRawIdentifier assembles the deterministic raw identifier string for a GKE workload
// following the format: clustername_namespace_kind_workloadid_YYYYMMDD-HHmmss.
//
// Extracted to allow verifying the unhashed raw string against the GKE ML Diagnostics SDK specification.
// If the creation timestamp is uninitialized (zero), the timestamp segment is omitted to avoid a dangling separator.
func ConstructMLRunRawIdentifier(cluster, namespace, kind, jobSetName string, creationTime time.Time) string {
	clusterName := normalizeClusterName(cluster)
	timestamp := formatMLRunIdentifierTimestamp(creationTime)
	if timestamp == "" {
		return fmt.Sprintf("%s_%s_%s_%s", clusterName, namespace, kind, jobSetName)
	}
	return fmt.Sprintf("%s_%s_%s_%s_%s", clusterName, namespace, kind, jobSetName, timestamp)
}

// ConstructMLRunID deterministically constructs the SHA-256 hex digest of the raw workload identifier.
//
// Extracted into its own pure function so that ID construction is independently testable and
// guaranteed idempotent when reconciliations are retried.
func ConstructMLRunID(cluster, namespace, kind, jobSetName string, creationTime time.Time) string {
	rawID := ConstructMLRunRawIdentifier(cluster, namespace, kind, jobSetName, creationTime)
	hash := sha256.Sum256([]byte(rawID))
	return hex.EncodeToString(hash[:])
}

// formatCreationTime returns the timestamp formatted for MLRun workload creation times,
// or an empty string if the timestamp is uninitialized.
//
// Extracted into a standalone helper so that creation time formatting and zero-value
// handling are independently testable and isolated from the MLRun creation flow.
func formatCreationTime(t metav1.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(constants.MLRunCreationTimeFormat)
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

// mlRunHasLabel reports whether an MLRun resource carries the specified key-value label pair.
// Because a GET response decodes labels as map[string]any, it type-asserts defensively.
func mlRunHasLabel(mlRun map[string]any, key, value string) bool {
	labels, ok := mlRun["labels"].(map[string]any)
	if !ok {
		return false
	}
	v, ok := labels[key].(string)
	return ok && v == value
}

// mlRunOwnedByXManager reports whether an MLRun resource carries our
// created_by=xmanager label.
func mlRunOwnedByXManager(mlRun map[string]any) bool {
	return mlRunHasLabel(mlRun, "created_by", createdByXManagerLabelValue)
}

// ensureProfilesTab guarantees that an MLRun URL targets the profiles view in
// Google Cloud Console / Diagon. The control plane API returns the base URL of the
// run (defaulting to the overview tab), so we explicitly stamp the tab query
// parameter so users clicking the profile action button land on the profiles view.
// Pre-existing tab parameters are replaced, other query parameters are preserved,
// and malformed URLs fall back to the original string.
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

// consoleProject and consoleLocation return the target project and location
// where the MLRun resource is created, falling back to the reconciler's default
// Project and Location when unset.
func (r *Reconciler) consoleProject() string {
	if r.MLRunProject != "" {
		return r.MLRunProject
	}
	return r.Project
}

func (r *Reconciler) consoleLocation() string {
	if r.MLRunLocation != "" {
		return r.MLRunLocation
	}
	return r.Location
}

// mlRunURL returns the URL to surface for an MLRun: the API-provided url (with the
// profiles tab preselected) when present, otherwise the deterministic Cloud
// Console diagnostics URL for the id.
func (r *Reconciler) mlRunURL(res map[string]any, mlRunID string) string {
	if res != nil {
		if urlVal, ok := res["url"].(string); ok && urlVal != "" {
			return ensureProfilesTab(urlVal)
		}
	}
	return constructDiagnosticsConsoleURL(r.consoleLocation(), mlRunID, r.consoleProject())
}

// constructDiagnosticsConsoleURL constructs the standard ML Diagnostics detail view
// URL in Google Cloud Console with the profiles tab preselected.
func constructDiagnosticsConsoleURL(location, mlRunID, project string) string {
	if location == "" || mlRunID == "" || project == "" {
		return ""
	}
	return fmt.Sprintf("https://console.cloud.google.com/kubernetes/aiml/run/%s/%s?project=%s&tab=profiles", location, mlRunID, project)
}

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
	if jobSet == nil {
		return ManagedXprofEnvs{}
	}
	for _, rj := range jobSet.Spec.ReplicatedJobs {
		for _, c := range rj.Template.Spec.Template.Spec.Containers {
			envs := extractContainerManagedXprofEnvs(c)
			// These environment variables are same on all containers, so we can return early.
			if envs.DisplayName != "" || envs.RunGroup != "" || envs.GCSPath != "" {
				return envs
			}
		}
	}
	return ManagedXprofEnvs{}
}

func extractContainerManagedXprofEnvs(c corev1.Container) ManagedXprofEnvs {
	var envs ManagedXprofEnvs
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
	if jobSet == nil {
		return
	}
	if managedXprofMisconfigured(displayName, runGroup, gcsPath) {
		logging.Warn(ctx, "No GCS path found for Managed Xprof, skipping MLRun URL creation.", "namespace", jobSet.Namespace, "xid", xid, "wid", wid)
	}
}
