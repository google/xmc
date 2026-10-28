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
	"errors"
	"testing"
	"time"
)

func TestNormalizeClusterName(t *testing.T) {
	tests := []struct {
		name    string
		cluster string
		want    string
	}{
		{name: "bare cluster name", cluster: "cluster-1", want: "cluster-1"},
		{name: "full GCP resource path", cluster: "projects/my-proj/locations/us-central1/clusters/cluster-1", want: "cluster-1"},
		{name: "cluster path with trailing slash", cluster: "projects/my-proj/locations/us-central1/clusters/cluster-1/", want: "cluster-1"},
		{name: "empty cluster", cluster: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeClusterName(tc.cluster); got != tc.want {
				t.Errorf("normalizeClusterName(%q) = %q, want %q", tc.cluster, got, tc.want)
			}
		})
	}
}

func TestFormatMLRunIdentifierTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		timestamp time.Time
		want      string
	}{
		{name: "zero timestamp returns empty string", timestamp: time.Time{}, want: ""},
		{name: "standard UTC timestamp", timestamp: time.Date(2023, 3, 28, 10, 40, 0, 0, time.UTC), want: "20230328-104000"},
		{name: "non-UTC timezone converted to UTC", timestamp: time.Date(2023, 3, 28, 6, 40, 0, 0, time.FixedZone("EDT", -4*3600)), want: "20230328-104000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatMLRunIdentifierTimestamp(tc.timestamp); got != tc.want {
				t.Errorf("formatMLRunIdentifierTimestamp(%v) = %q, want %q", tc.timestamp, got, tc.want)
			}
		})
	}
}

func TestConstructMLRunRawIdentifier(t *testing.T) {
	creationTime := time.Date(2023, 3, 28, 10, 40, 0, 0, time.UTC)
	tests := []struct {
		name         string
		cluster      string
		namespace    string
		kind         string
		workloadName string
		creationTime time.Time
		want         string
	}{
		{
			name: "standard inputs", cluster: "cluster-1", namespace: "test-ns", kind: "JobSet",
			workloadName: "my-jobset", creationTime: creationTime, want: "cluster-1_test-ns_JobSet_my-jobset_20230328-104000",
		},
		{
			name: "cluster resource path normalized to name", cluster: "projects/my-project/locations/us-central1/clusters/cluster-1",
			namespace: "test-ns", kind: "JobSet", workloadName: "my-jobset", creationTime: creationTime,
			want: "cluster-1_test-ns_JobSet_my-jobset_20230328-104000",
		},
		{
			name: "zero creation timestamp omits timestamp segment", cluster: "cluster-1", namespace: "test-ns",
			kind: "JobSet", workloadName: "my-jobset", creationTime: time.Time{}, want: "cluster-1_test-ns_JobSet_my-jobset",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ConstructMLRunRawIdentifier(tc.cluster, tc.namespace, tc.kind, tc.workloadName, tc.creationTime)
			if got != tc.want {
				t.Errorf("ConstructMLRunRawIdentifier() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConstructMLRunID pins known digests produced by the legacy JobSet reconciler.
// They must never change: the Experiment State Server looks up MLRuns created by the
// reconciler before the artifact lifecycle flag flip using the same id.
func TestConstructMLRunID(t *testing.T) {
	utcTime := time.Date(2023, 3, 28, 10, 40, 0, 0, time.UTC)
	edtTime := time.Date(2023, 3, 28, 6, 40, 0, 0, time.FixedZone("EDT", -4*3600))
	const stdHash = "b8b90f4909350e43f6ac04e5a50e5f89d8f0514661af52f40ddea9b4ade00aa1"

	tests := []struct {
		name         string
		cluster      string
		namespace    string
		kind         string
		workloadName string
		creationTime time.Time
		want         string
	}{
		{
			name: "standard inputs with UTC timestamp", cluster: "cluster-1", namespace: "test-ns",
			kind: "JobSet", workloadName: "my-jobset", creationTime: utcTime, want: stdHash,
		},
		{
			name: "cluster resource path produces identical hash", cluster: "projects/my-project/locations/us-central1/clusters/cluster-1",
			namespace: "test-ns", kind: "JobSet", workloadName: "my-jobset", creationTime: utcTime, want: stdHash,
		},
		{
			name: "cluster resource path with trailing slash", cluster: "projects/my-project/locations/us-central1/clusters/cluster-1/",
			namespace: "test-ns", kind: "JobSet", workloadName: "my-jobset", creationTime: utcTime, want: stdHash,
		},
		{
			name: "non-UTC timezone converted to UTC yields same hash", cluster: "cluster-1", namespace: "test-ns",
			kind: "JobSet", workloadName: "my-jobset", creationTime: edtTime, want: stdHash,
		},
		{
			name: "zero creation timestamp produces deterministic hash", cluster: "cluster-1", namespace: "test-ns",
			kind: "JobSet", workloadName: "my-jobset", creationTime: time.Time{},
			want: "683e79a8574a045090201ce462b54b127e4cee94a9835f0d23c08d5ce89bc2ca",
		},
		{
			name: "leading slash resource path and custom kind", cluster: "/projects/p/locations/l/clusters/test-cluster", namespace: "test-namespace",
			kind: "test-kind", workloadName: "test-id", creationTime: time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC),
			want: "666cf6b4e2cbfd70c5db5b51e1e7d6873c56fe290188e5fe98ff42df8fb8e0e8",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ConstructMLRunID(tc.cluster, tc.namespace, tc.kind, tc.workloadName, tc.creationTime)
			if got != tc.want {
				t.Errorf("ConstructMLRunID() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseMLRunIDFromResourceName(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "no match", text: "some other error", want: ""},
		{name: "match", text: `already exists: projects/p/locations/l/machineLearningRuns/xmc-abc-123`, want: "xmc-abc-123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseMLRunIDFromResourceName(tc.text); got != tc.want {
				t.Errorf("ParseMLRunIDFromResourceName(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestResolveExistingMLRunID(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil error returns fallback", err: nil, want: "fallback"},
		{name: "error without resource name returns fallback", err: errors.New("conflict"), want: "fallback"},
		{name: "error with resource name returns embedded id", err: errors.New("exists: projects/p/locations/l/machineLearningRuns/other-id"), want: "other-id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveExistingMLRunID(tc.err, "fallback"); got != tc.want {
				t.Errorf("ResolveExistingMLRunID(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestParseMLRunIDFromURL(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		want   string
	}{
		{name: "empty", rawURL: "", want: ""},
		{name: "unrelated url", rawURL: "https://example.com/foo", want: ""},
		{
			name:   "console url",
			rawURL: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/abc123?project=p&tab=profiles",
			want:   "abc123",
		},
		{
			name:   "runs segment url",
			rawURL: "https://console.cloud.google.com/cluster-director/locations/us-central1/runs/def456?project=p",
			want:   "def456",
		},
		{
			name:   "resource name",
			rawURL: "projects/p/locations/l/machineLearningRuns/ghi789",
			want:   "ghi789",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseMLRunIDFromURL(tc.rawURL); got != tc.want {
				t.Errorf("ParseMLRunIDFromURL(%q) = %q, want %q", tc.rawURL, got, tc.want)
			}
		})
	}
}

func TestParseMLRunIDFromURL_RoundTripsConsoleURL(t *testing.T) {
	const id = "b8b90f4909350e43f6ac04e5a50e5f89d8f0514661af52f40ddea9b4ade00aa1"
	if got := ParseMLRunIDFromURL(ConsoleURL("us-central1", id, "p")); got != id {
		t.Errorf("ParseMLRunIDFromURL(ConsoleURL(...)) = %q, want %q", got, id)
	}
}
