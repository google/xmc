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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/hypercomputeclient"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
)

// artifactListPageSize bounds the single ListArtifacts page read by
// lookupManagedXprofArtifact. At most one ManagedXprof artifact is expected per
// work unit, so a small page is sufficient.
const artifactListPageSize = 10

// managedXprofArtifactFilter restricts ListArtifacts to ManagedXprof artifacts
// server-side (via the payload_type schema filter field), so the reconciler no
// longer fetches and discards unrelated artifact types while searching.
const managedXprofArtifactFilter = `payload_type = "managed_xprof"`

// formatCreationTime returns the timestamp formatted for MLRun workload creation times,
// or an empty string if the timestamp is uninitialized.
//
// Extracted into a standalone helper so that creation time formatting and zero-value
// handling are independently testable and isolated from the MLRun creation flow.
func formatCreationTime(t metav1.Time) string {
	return hypercomputeclient.FormatWorkloadCreateTime(t.Time)
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

// mlRunURL returns the URL to surface for an MLRun resource (see
// hypercomputeclient.MLRunURL) in the reconciler's MLRun project and location.
func (r *Reconciler) mlRunURL(mlRun map[string]any, mlRunID string) string {
	return hypercomputeclient.MLRunURL(mlRun, r.consoleLocation(), mlRunID, r.consoleProject())
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
