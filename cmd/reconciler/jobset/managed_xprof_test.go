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
	"errors"
	"fmt"
	"maps"
	"net/http"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	"xmanager_cloud/cmd/reconciler/hypercomputeclient"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/kubernetes/common/constants"
)

func jobSetWithWorkerContainers(containers ...corev1.Container) *v1alpha2.JobSet {
	return &v1alpha2.JobSet{
		Spec: v1alpha2.JobSetSpec{
			ReplicatedJobs: []v1alpha2.ReplicatedJob{
				{
					Name: "worker",
					Template: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{Containers: containers},
							},
						},
					},
				},
			},
		},
	}
}

func withManagedXprofEnvs(jobSet *v1alpha2.JobSet) *v1alpha2.JobSet {
	jobSet.Spec.ReplicatedJobs = jobSetWithWorkerContainers(corev1.Container{
		Env: []corev1.EnvVar{
			{Name: constants.XmcEnvVarManagedXprofDisplayName, Value: "test-display-name"},
			{Name: constants.XmcEnvVarManagedXprofRunGroup, Value: "test-run-group"},
			{Name: constants.XmcEnvVarManagedXprofGCSObjectFolder, Value: "gs://test-bucket/xmc-123-1-timestamp"},
		},
	}).Spec.ReplicatedJobs
	return jobSet
}

func TestGetManagedXprofEnvs(t *testing.T) {
	tests := []struct {
		name            string
		jobSet          *v1alpha2.JobSet
		wantDisplayName string
		wantRunGroup    string
		wantGCSPath     string
	}{
		{name: "empty jobset", jobSet: &v1alpha2.JobSet{}},
		{name: "no replicated jobs", jobSet: &v1alpha2.JobSet{Spec: v1alpha2.JobSetSpec{ReplicatedJobs: []v1alpha2.ReplicatedJob{}}}},
		{name: "no containers", jobSet: jobSetWithWorkerContainers()},
		{name: "no env vars", jobSet: jobSetWithWorkerContainers(corev1.Container{})},
		{
			name:   "irrelevant env vars",
			jobSet: jobSetWithWorkerContainers(corev1.Container{Env: []corev1.EnvVar{{Name: "OTHER", Value: "value"}}}),
		},
		{
			name:            "partial env vars - display name only",
			jobSet:          jobSetWithWorkerContainers(corev1.Container{Env: []corev1.EnvVar{{Name: constants.XmcEnvVarManagedXprofDisplayName, Value: "test-display-name"}}}),
			wantDisplayName: "test-display-name",
		},
		{
			name:            "all env vars",
			jobSet:          withManagedXprofEnvs(&v1alpha2.JobSet{}),
			wantDisplayName: "test-display-name",
			wantRunGroup:    "test-run-group",
			wantGCSPath:     "gs://test-bucket/xmc-123-1-timestamp",
		},
		{
			name: "multiple containers - early return",
			jobSet: jobSetWithWorkerContainers(
				corev1.Container{Env: []corev1.EnvVar{{Name: constants.XmcEnvVarManagedXprofDisplayName, Value: "test-display-name"}}},
				corev1.Container{Env: []corev1.EnvVar{{Name: constants.XmcEnvVarManagedXprofRunGroup, Value: "test-run-group"}}},
			),
			wantDisplayName: "test-display-name",
			wantRunGroup:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getManagedXprofEnvs(tc.jobSet)
			if got.DisplayName != tc.wantDisplayName {
				t.Errorf("getManagedXprofEnvs() gotDisplayName = %q, want %q", got.DisplayName, tc.wantDisplayName)
			}
			if got.RunGroup != tc.wantRunGroup {
				t.Errorf("getManagedXprofEnvs() gotRunGroup = %q, want %q", got.RunGroup, tc.wantRunGroup)
			}
			if got.GCSPath != tc.wantGCSPath {
				t.Errorf("getManagedXprofEnvs() gotGCSPath = %q, want %q", got.GCSPath, tc.wantGCSPath)
			}
		})
	}
}

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
		jobSetName   string
		creationTime time.Time
		want         string
	}{
		{
			name: "standard inputs", cluster: "cluster-1", namespace: "test-ns", kind: "JobSet",
			jobSetName: "my-jobset", creationTime: creationTime, want: "cluster-1_test-ns_JobSet_my-jobset_20230328-104000",
		},
		{
			name: "cluster resource path normalized to name", cluster: "projects/my-project/locations/us-central1/clusters/cluster-1",
			namespace: "test-ns", kind: "JobSet", jobSetName: "my-jobset", creationTime: creationTime,
			want: "cluster-1_test-ns_JobSet_my-jobset_20230328-104000",
		},
		{
			name: "zero creation timestamp omits timestamp segment", cluster: "cluster-1", namespace: "test-ns",
			kind: "JobSet", jobSetName: "my-jobset", creationTime: time.Time{}, want: "cluster-1_test-ns_JobSet_my-jobset",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ConstructMLRunRawIdentifier(tc.cluster, tc.namespace, tc.kind, tc.jobSetName, tc.creationTime)
			if got != tc.want {
				t.Errorf("ConstructMLRunRawIdentifier() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConstructMLRunID(t *testing.T) {
	utcTime := time.Date(2023, 3, 28, 10, 40, 0, 0, time.UTC)
	edtTime := time.Date(2023, 3, 28, 6, 40, 0, 0, time.FixedZone("EDT", -4*3600))
	const stdHash = "b8b90f4909350e43f6ac04e5a50e5f89d8f0514661af52f40ddea9b4ade00aa1"

	tests := []struct {
		name         string
		cluster      string
		namespace    string
		kind         string
		jobSetName   string
		creationTime time.Time
		want         string
	}{
		{
			name: "standard inputs with UTC timestamp", cluster: "cluster-1", namespace: "test-ns",
			kind: "JobSet", jobSetName: "my-jobset", creationTime: utcTime, want: stdHash,
		},
		{
			name: "cluster resource path produces identical hash", cluster: "projects/my-project/locations/us-central1/clusters/cluster-1",
			namespace: "test-ns", kind: "JobSet", jobSetName: "my-jobset", creationTime: utcTime, want: stdHash,
		},
		{
			name: "cluster resource path with trailing slash", cluster: "projects/my-project/locations/us-central1/clusters/cluster-1/",
			namespace: "test-ns", kind: "JobSet", jobSetName: "my-jobset", creationTime: utcTime, want: stdHash,
		},
		{
			name: "non-UTC timezone converted to UTC yields same hash", cluster: "cluster-1", namespace: "test-ns",
			kind: "JobSet", jobSetName: "my-jobset", creationTime: edtTime, want: stdHash,
		},
		{
			name: "zero creation timestamp produces deterministic hash", cluster: "cluster-1", namespace: "test-ns",
			kind: "JobSet", jobSetName: "my-jobset", creationTime: time.Time{},
			want: "683e79a8574a045090201ce462b54b127e4cee94a9835f0d23c08d5ce89bc2ca",
		},
		{
			name: "combined conditions: resource path, custom kind, non-UTC timestamp", cluster: "projects/p/locations/l/clusters/cluster-1",
			namespace: "test-ns", kind: "JobSet", jobSetName: "my-jobset", creationTime: edtTime, want: stdHash,
		},
		{
			name: "timestamp_1", cluster: "/projects/p/locations/l/clusters/test-cluster", namespace: "test-namespace",
			kind: "test-kind", jobSetName: "test-id", creationTime: time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC),
			want: "666cf6b4e2cbfd70c5db5b51e1e7d6873c56fe290188e5fe98ff42df8fb8e0e8",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ConstructMLRunID(tc.cluster, tc.namespace, tc.kind, tc.jobSetName, tc.creationTime)
			if got != tc.want {
				t.Errorf("ConstructMLRunID() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMLRunHasLabel(t *testing.T) {
	tests := []struct {
		name     string
		mlRun    map[string]any
		key      string
		value    string
		wantBool bool
	}{
		{name: "nil mlRun", mlRun: nil, key: "k", value: "v", wantBool: false},
		{name: "missing labels entry", mlRun: map[string]any{}, key: "k", value: "v", wantBool: false},
		{name: "non-map labels entry", mlRun: map[string]any{"labels": "not-a-map"}, key: "k", value: "v", wantBool: false},
		{name: "missing key in labels", mlRun: map[string]any{"labels": map[string]any{"other": "v"}}, key: "k", value: "v", wantBool: false},
		{name: "mismatched value in labels", mlRun: map[string]any{"labels": map[string]any{"k": "wrong"}}, key: "k", value: "v", wantBool: false},
		{name: "matching key and value", mlRun: map[string]any{"labels": map[string]any{"k": "v"}}, key: "k", value: "v", wantBool: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mlRunHasLabel(tc.mlRun, tc.key, tc.value)
			if got != tc.wantBool {
				t.Errorf("mlRunHasLabel(%v, %q, %q) = %v, want %v", tc.mlRun, tc.key, tc.value, got, tc.wantBool)
			}
		})
	}
}

func TestFormatCreationTime(t *testing.T) {
	tests := []struct {
		name      string
		timestamp metav1.Time
		want      string
	}{
		{name: "zero uninitialized timestamp returns empty string", timestamp: metav1.Time{}, want: ""},
		{name: "standard UTC timestamp formatted to RFC3339Nano", timestamp: metav1.NewTime(time.Date(2026, 9, 9, 10, 0, 0, 123456789, time.UTC)), want: "2026-09-09T10:00:00.123456789Z"},
		{name: "non-UTC timezone timestamp converted to UTC", timestamp: metav1.NewTime(time.Date(2026, 9, 9, 6, 0, 0, 0, time.FixedZone("EDT", -4*3600))), want: "2026-09-09T10:00:00Z"},
		{name: "zero fractional seconds formatted without trailing dot", timestamp: metav1.NewTime(time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)), want: "2026-09-09T10:00:00Z"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatCreationTime(tc.timestamp); got != tc.want {
				t.Errorf("formatCreationTime(%v) = %q, want %q", tc.timestamp, got, tc.want)
			}
		})
	}
}

func TestParseExistingMLRunID(t *testing.T) {
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
			if got := parseExistingMLRunID(tc.text); got != tc.want {
				t.Errorf("parseExistingMLRunID(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestIsMLRunAlreadyExistsError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "other error", err: errors.New("deadline exceeded"), want: false},
		{name: "http 409 conflict", err: &hypercomputeclient.APIError{StatusCode: http.StatusConflict, Body: "ALREADY_EXISTS"}, want: true},
		{name: "http other status", err: &hypercomputeclient.APIError{StatusCode: http.StatusInternalServerError}, want: false},
		{name: "lro already exists", err: &hypercomputeclient.OperationError{Code: hypercomputeclient.CodeAlreadyExists}, want: true},
		{name: "lro other code", err: &hypercomputeclient.OperationError{Code: 3}, want: false},
		{name: "wrapped http 409", err: fmt.Errorf("create failed: %w", &hypercomputeclient.APIError{StatusCode: http.StatusConflict}), want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMLRunAlreadyExistsError(tc.err); got != tc.want {
				t.Errorf("isMLRunAlreadyExistsError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestMLRunOwnedByXManager(t *testing.T) {
	tests := []struct {
		name  string
		mlRun map[string]any
		want  bool
	}{
		{name: "no labels", mlRun: map[string]any{}, want: false},
		{name: "other owner", mlRun: map[string]any{"labels": map[string]any{"created_by": "someone"}}, want: false},
		{name: "owned", mlRun: map[string]any{"labels": map[string]any{"created_by": createdByXManagerLabelValue}}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mlRunOwnedByXManager(tc.mlRun); got != tc.want {
				t.Errorf("mlRunOwnedByXManager(%v) = %v, want %v", tc.mlRun, got, tc.want)
			}
		})
	}
}

func managedXprofArtifact(displayName, runGroup string) *spb.Artifact {
	return &spb.Artifact{
		Payload: &spb.ArtifactPayload{
			Type: &spb.ArtifactPayload_ManagedXprof{
				ManagedXprof: &spb.ManagedXprofArtifact{DisplayName: displayName, RunGroup: runGroup},
			},
		},
	}
}

// managedXprofWant is the expected (displayName, runGroup) of a ManagedXprof artifact.
type managedXprofWant struct {
	display, runGroup string
}

func TestManagedXprofMisconfigured(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		runGroup    string
		gcsPath     string
		want        bool
	}{
		{name: "standard jobset all empty", want: false},
		{name: "fully configured", displayName: "d", runGroup: "g", gcsPath: "gs://b", want: false},
		{name: "gcsPath set metadata missing is transient", gcsPath: "gs://b", want: false},
		{name: "gcsPath set only run group missing is transient", displayName: "d", gcsPath: "gs://b", want: false},
		{name: "metadata present but no gcsPath is misconfig", displayName: "d", runGroup: "g", want: true},
		{name: "only display no gcsPath not enough signal", displayName: "d", want: false},
		{name: "only run group no gcsPath not enough signal", runGroup: "g", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedXprofMisconfigured(tc.displayName, tc.runGroup, tc.gcsPath); got != tc.want {
				t.Errorf("managedXprofMisconfigured(%q,%q,%q) = %v, want %v", tc.displayName, tc.runGroup, tc.gcsPath, got, tc.want)
			}
		})
	}
}

func TestCollectManagedXprofArtifacts(t *testing.T) {
	tests := []struct {
		name      string
		artifacts []*spb.Artifact
		want      []managedXprofWant
	}{
		{name: "nil list", artifacts: nil},
		{name: "empty list", artifacts: []*spb.Artifact{}},
		{name: "no managed xprof payload", artifacts: []*spb.Artifact{{}}},
		{name: "single", artifacts: []*spb.Artifact{managedXprofArtifact("disp", "grp")}, want: []managedXprofWant{{"disp", "grp"}}},
		{
			name:      "collects all in order, skipping non-managed",
			artifacts: []*spb.Artifact{{}, managedXprofArtifact("d1", "g1"), managedXprofArtifact("d2", "g2")},
			want:      []managedXprofWant{{"d1", "g1"}, {"d2", "g2"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := collectManagedXprofArtifacts(tc.artifacts)
			if len(got) != len(tc.want) {
				t.Fatalf("collectManagedXprofArtifacts() returned %d items, want %d", len(got), len(tc.want))
			}
			for i, w := range tc.want {
				if got[i].GetDisplayName() != w.display || got[i].GetRunGroup() != w.runGroup {
					t.Errorf("item %d = (%q,%q), want (%q,%q)", i, got[i].GetDisplayName(), got[i].GetRunGroup(), w.display, w.runGroup)
				}
			}
		})
	}
}

func TestLookupManagedXprofArtifact(t *testing.T) {
	tests := []struct {
		name         string
		storage      ReconcilerStorage
		wantDisplay  string
		wantRunGroup string
		wantCalls    int
	}{
		{name: "nil storage", storage: nil},
		{name: "list error", storage: &mockStorageUpdater{listArtifactsErr: errors.New("boom")}},
		{name: "no artifact", storage: &mockStorageUpdater{}},
		{
			name:         "artifact present",
			storage:      &mockStorageUpdater{listArtifacts: []*spb.Artifact{managedXprofArtifact("disp", "grp")}},
			wantDisplay:  "disp",
			wantRunGroup: "grp",
			wantCalls:    1,
		},
		{
			name:         "multiple artifacts uses first",
			storage:      &mockStorageUpdater{listArtifacts: []*spb.Artifact{managedXprofArtifact("d1", "g1"), managedXprofArtifact("d2", "g2")}},
			wantDisplay:  "d1",
			wantRunGroup: "g1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Reconciler{Storage: tc.storage}
			gotDisplay, gotRunGroup := r.lookupManagedXprofArtifact(context.Background(), 1, 2)
			if gotDisplay != tc.wantDisplay || gotRunGroup != tc.wantRunGroup {
				t.Errorf("got (%q,%q), want (%q,%q)", gotDisplay, gotRunGroup, tc.wantDisplay, tc.wantRunGroup)
			}
			if tc.wantCalls > 0 {
				m, ok := tc.storage.(*mockStorageUpdater)
				if !ok {
					t.Fatalf("wantCalls set but storage is %T, not *mockStorageUpdater", tc.storage)
				}
				if m.listArtifactsCalls != tc.wantCalls {
					t.Errorf("ListArtifacts called %d times, want %d", m.listArtifactsCalls, tc.wantCalls)
				}
			}
			if m, ok := tc.storage.(*mockStorageUpdater); ok {
				for _, f := range m.gotFilters {
					if f != managedXprofArtifactFilter {
						t.Errorf("ListArtifacts filter = %q, want %q", f, managedXprofArtifactFilter)
					}
				}
			}
		})
	}
}

func TestConstructDiagnosticsConsoleURL(t *testing.T) {
	tests := []struct {
		name     string
		location string
		mlRunID  string
		project  string
		want     string
	}{
		{
			name: "valid parameters construct url with profiles tab", location: "us-central1", mlRunID: "xmc-test-run-123",
			project: "test-project", want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/xmc-test-run-123?project=test-project&tab=profiles",
		},
		{name: "empty location returns empty string", location: "", mlRunID: "xmc-test-run-123", project: "test-project", want: ""},
		{name: "empty mlRunID returns empty string", location: "us-central1", mlRunID: "", project: "test-project", want: ""},
		{name: "empty project returns empty string", location: "us-central1", mlRunID: "xmc-test-run-123", project: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := constructDiagnosticsConsoleURL(tc.location, tc.mlRunID, tc.project)
			if got != tc.want {
				t.Errorf("constructDiagnosticsConsoleURL(%q, %q, %q) = %q, want %q", tc.location, tc.mlRunID, tc.project, got, tc.want)
			}
		})
	}
}

func TestMLRunURL(t *testing.T) {
	r := &Reconciler{Project: "test-project", Location: "us-central1"}

	tests := []struct {
		name    string
		res     map[string]any
		mlRunID string
		want    string
	}{
		{
			name: "nil res falls back to constructed url with profiles tab", res: nil, mlRunID: "run-1",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project&tab=profiles",
		},
		{
			name: "res without url falls back to constructed url", res: map[string]any{"other": "field"}, mlRunID: "run-2",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-2?project=test-project&tab=profiles",
		},
		{
			name: "res with empty url falls back to constructed url", res: map[string]any{"url": ""}, mlRunID: "run-3",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-3?project=test-project&tab=profiles",
		},
		{
			name: "res with api url returns api url directly", res: map[string]any{"url": "https://console.cloud.google.com/aiml/run?project=p"},
			mlRunID: "run-4", want: "https://console.cloud.google.com/aiml/run?project=p&tab=profiles",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.mlRunURL(tc.res, tc.mlRunID)
			if got != tc.want {
				t.Errorf("r.mlRunURL(%v, %q) = %q, want %q", tc.res, tc.mlRunID, got, tc.want)
			}
		})
	}
}

func TestEnsureProfilesTab(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty url returns empty string", raw: "", want: ""},
		{
			name: "url without query params appends tab=profiles",
			raw:  "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?tab=profiles",
		},
		{
			name: "url with project param appends tab=profiles",
			raw:  "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project&tab=profiles",
		},
		{
			name: "url with multiple query params preserves params and appends tab=profiles",
			raw:  "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?authuser=1&project=test-project",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?authuser=1&project=test-project&tab=profiles",
		},
		{
			name: "url already containing tab=profiles is idempotent",
			raw:  "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project&tab=profiles",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project&tab=profiles",
		},
		{
			name: "url with different tab replaces tab with profiles",
			raw:  "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project&tab=overview",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=test-project&tab=profiles",
		},
		{
			name: "url with fragment preserves fragment",
			raw:  "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=p#summary",
			want: "https://console.cloud.google.com/kubernetes/aiml/run/us-central1/run-1?project=p&tab=profiles#summary",
		},
		{name: "malformed url returns raw input unchanged", raw: "http://invalid domain with spaces", want: "http://invalid domain with spaces"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ensureProfilesTab(tc.raw)
			if got != tc.want {
				t.Errorf("ensureProfilesTab(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestBuildMLRunLabels verifies the reconciler stamps ownership and metadata labels
// using snake_case keys (created_by, experiment_id, workunit_id).
func TestBuildMLRunLabels(t *testing.T) {
	got := buildMLRunLabels(12, 3)
	want := map[string]string{
		"experiment_id": "12",
		"workunit_id":   "3",
		"created_by":    createdByXManagerLabelValue,
	}
	if !maps.Equal(got, want) {
		t.Errorf("buildMLRunLabels(12, 3) = %v, want %v", got, want)
	}
}
