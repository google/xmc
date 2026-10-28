// Copyright 2026 Google LLC
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

package hypercomputeclient

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"xmanager_cloud/internal/kubernetes/common/constants"
)

var (
	// mlRunResourceNameRE captures the MLRun id embedded in a resource name, e.g.
	// "projects/p/locations/l/machineLearningRuns/some-id".
	mlRunResourceNameRE = regexp.MustCompile(`machineLearningRuns/([a-zA-Z0-9_-]+)`)
	// mlRunConsolePathRE captures the MLRun id from a Cloud Console run URL path
	// ".../kubernetes/aiml/run/{location}/{id}" (see ConsoleURL).
	mlRunConsolePathRE = regexp.MustCompile(`/aiml/run/[^/?#]+/([a-zA-Z0-9_-]+)`)
	// mlRunRunsSegmentRE captures the MLRun id from a ".../runs/{id}" URL path.
	mlRunRunsSegmentRE = regexp.MustCompile(`/runs/([a-zA-Z0-9_-]+)`)
)

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
func formatMLRunIdentifierTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(constants.MLRunIdentifierTimeFormat)
}

// ConstructMLRunRawIdentifier assembles the deterministic raw identifier string for a GKE workload
// following the format: clustername_namespace_kind_workloadid_YYYYMMDD-HHmmss.
//
// If the creation timestamp is uninitialized (zero), the timestamp segment is omitted to avoid a
// dangling separator.
func ConstructMLRunRawIdentifier(cluster, namespace, kind, workloadName string, creationTime time.Time) string {
	clusterName := normalizeClusterName(cluster)
	timestamp := formatMLRunIdentifierTimestamp(creationTime)
	if timestamp == "" {
		return fmt.Sprintf("%s_%s_%s_%s", clusterName, namespace, kind, workloadName)
	}
	return fmt.Sprintf("%s_%s_%s_%s_%s", clusterName, namespace, kind, workloadName, timestamp)
}

// ConstructMLRunID deterministically constructs the SHA-256 hex digest of the raw workload identifier.
//
// Every producer (JobSet reconciler and Experiment State Server) must derive the id through this
// function so that runs created by one are found by the other.
func ConstructMLRunID(cluster, namespace, kind, workloadName string, creationTime time.Time) string {
	rawID := ConstructMLRunRawIdentifier(cluster, namespace, kind, workloadName, creationTime)
	hash := sha256.Sum256([]byte(rawID))
	return hex.EncodeToString(hash[:])
}

// ParseMLRunIDFromResourceName extracts the MLRun id from text containing an MLRun resource name.
// Because only one MLRun may exist per workload, the control plane embeds the existing run's
// resource name in AlreadyExists errors. Returns "" if not found.
func ParseMLRunIDFromResourceName(text string) string {
	m := mlRunResourceNameRE.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// ResolveExistingMLRunID returns the id of the run that caused createErr, falling back to
// fallbackID when the error does not carry a resource name.
func ResolveExistingMLRunID(createErr error, fallbackID string) string {
	if createErr == nil {
		return fallbackID
	}
	if id := ParseMLRunIDFromResourceName(createErr.Error()); id != "" {
		return id
	}
	return fallbackID
}

// ParseMLRunIDFromURL extracts the MLRun id from a stored MLRun URL: a Cloud Console run URL
// (see ConsoleURL), a ".../runs/{id}" URL, or a URL containing an MLRun resource name.
// Returns "" if no id can be found.
func ParseMLRunIDFromURL(rawURL string) string {
	for _, re := range []*regexp.Regexp{mlRunConsolePathRE, mlRunRunsSegmentRE, mlRunResourceNameRE} {
		if m := re.FindStringSubmatch(rawURL); len(m) >= 2 {
			return m[1]
		}
	}
	return ""
}
