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
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	spb "xmanager_cloud/api/protos/experiment"
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
