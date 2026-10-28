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

package hypercomputeclient

import (
	"fmt"
	"strings"
	"time"

	"xmanager_cloud/internal/kubernetes/common/constants"
)

// managedXprofGCSPathArtifactKey is the MLRun artifacts key carrying the GCS folder
// where ManagedXprof writes profiles.
const managedXprofGCSPathArtifactKey = "gcs_path"

// ManagedXprofWorkload identifies the GKE JobSet a ManagedXprof MLRun tracks.
type ManagedXprofWorkload struct {
	// ClusterPath is the cluster resource path (see ResolveClusterPath).
	ClusterPath  string
	Namespace    string
	JobSetName   string
	CreationTime time.Time
}

// ResolveClusterPath returns the full cluster resource path
// (projects/<project>/locations/<location>/clusters/<name>) for a bare cluster name,
// or clusterName unchanged when it is already a path or project/location are unknown.
func ResolveClusterPath(clusterName, project, location string) string {
	if strings.HasPrefix(clusterName, "projects/") || project == "" || location == "" {
		return clusterName
	}
	return fmt.Sprintf("projects/%s/locations/%s/clusters/%s", project, location, clusterName)
}

// ManagedXprofArtifacts returns the MLRun artifacts map for a ManagedXprof run.
func ManagedXprofArtifacts(gcsPath string) map[string]string {
	return map[string]string{managedXprofGCSPathArtifactKey: gcsPath}
}

// FormatWorkloadCreateTime formats a workload creation time for MLRun workload
// details, returning "" for a zero time.
func FormatWorkloadCreateTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(constants.MLRunCreationTimeFormat)
}

// BuildManagedXprofCreateOptions returns the CreateMLRun options shared by every
// ManagedXprof MLRun producer, so all producers send an identical request.
func BuildManagedXprofCreateOptions(w ManagedXprofWorkload, runGroup string, labels, artifacts map[string]string) *CreateMLRunOptions {
	return &CreateMLRunOptions{
		RunGroup:     runGroup,
		Tools:        []map[string]any{{"xprof": map[string]any{}}},
		Labels:       labels,
		Artifacts:    artifacts,
		Orchestrator: "GKE",
		WorkloadDetails: &GKEWorkloadDetails{
			Kind:       "JobSet",
			Cluster:    w.ClusterPath,
			Namespace:  w.Namespace,
			ID:         w.JobSetName,
			CreateTime: FormatWorkloadCreateTime(w.CreationTime),
		},
	}
}
