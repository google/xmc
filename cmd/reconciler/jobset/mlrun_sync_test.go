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
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/cmd/reconciler/hypercomputeclient"
)

// mockMLRunCreator records the arguments of the most recent CreateMLRun call so
// tests can assert on the request the reconciler builds, and returns canned
// results/errors to exercise both the success and failure paths.
type mockMLRunCreator struct {
	createCalls int
	lastID      string
	lastDisplay string
	lastPhase   string
	lastOpts    *hypercomputeclient.CreateMLRunOptions
	retRes      map[string]any
	retErr      error

	getCalls int
	getRes   map[string]any
	getErr   error

	updateCalls    int
	lastUpdateID   string
	lastUpdateOpts *hypercomputeclient.UpdateMLRunOptions
	updateRes      map[string]any
	updateErr      error
}

func (m *mockMLRunCreator) CreateMLRun(ctx context.Context, name, displayName, runPhase string, opts *hypercomputeclient.CreateMLRunOptions) (map[string]any, error) {
	m.createCalls++
	m.lastID = name
	m.lastDisplay = displayName
	m.lastPhase = runPhase
	m.lastOpts = opts
	return m.retRes, m.retErr
}

func (m *mockMLRunCreator) GetMLRun(ctx context.Context, name string) (map[string]any, error) {
	m.getCalls++
	return m.getRes, m.getErr
}

func (m *mockMLRunCreator) UpdateMLRun(ctx context.Context, name string, opts *hypercomputeclient.UpdateMLRunOptions) (map[string]any, error) {
	m.updateCalls++
	m.lastUpdateID = name
	m.lastUpdateOpts = opts
	return m.updateRes, m.updateErr
}

func TestEnsureMLRunCreated(t *testing.T) {
	now := time.UnixMilli(1680000000000)
	jobSet := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-jobset",
			Namespace:         "test-ns",
			CreationTimestamp: metav1.NewTime(now),
		},
	}

	tests := []struct {
		name            string
		client          MLRunCreator
		jobSet          *v1alpha2.JobSet
		wantCalls       int
		wantID          string
		wantGroup       string
		wantBucket      string
		wantUpdateCalls int
		wantEmptyURL    bool
		wantConfirmed   bool
	}{
		{
			name:            "nil client",
			client:          nil,
			jobSet:          jobSet,
			wantCalls:       0,
			wantUpdateCalls: 0,
			wantEmptyURL:    true,
			wantConfirmed:   false,
		},
		{
			name:            "nil jobSet",
			client:          &mockMLRunCreator{},
			jobSet:          nil,
			wantCalls:       0,
			wantUpdateCalls: 0,
			wantEmptyURL:    true,
			wantConfirmed:   false,
		},
		{
			name:            "successful creation with correct options",
			client:          &mockMLRunCreator{retRes: map[string]any{"name": "op-123", "url": "https://mlrun/op-123"}},
			jobSet:          jobSet,
			wantCalls:       1,
			wantID:          "c0a5802815479054d7e6d629cf3339211c6f9520580f93a52b8d0e143495b033",
			wantGroup:       "group-abc",
			wantBucket:      "gs://bucket/folder",
			wantUpdateCalls: 1,
			wantConfirmed:   true,
		},
		{
			name:            "creation error handled without panic",
			client:          &mockMLRunCreator{retErr: errors.New("api error")},
			jobSet:          jobSet,
			wantCalls:       1,
			wantID:          "c0a5802815479054d7e6d629cf3339211c6f9520580f93a52b8d0e143495b033",
			wantGroup:       "group-abc",
			wantBucket:      "gs://bucket/folder",
			wantUpdateCalls: 0,
			wantEmptyURL:    true,
			wantConfirmed:   false,
		},
		{
			name:            "successful creation with zero creation timestamp leaves CreateTime empty",
			client:          &mockMLRunCreator{retRes: map[string]any{"name": "op-123", "url": "https://mlrun/op-123"}},
			jobSet:          &v1alpha2.JobSet{ObjectMeta: metav1.ObjectMeta{Name: "test-jobset", Namespace: "test-ns"}},
			wantCalls:       1,
			wantID:          "99b83bba7686db7f4ed15aebf53cf4ec5b2acc80f44c55c39168f355e2c930ac",
			wantGroup:       "group-abc",
			wantBucket:      "gs://bucket/folder",
			wantUpdateCalls: 1,
			wantConfirmed:   true,
		},
		{
			name:            "update failure returns empty url",
			client:          &mockMLRunCreator{retRes: map[string]any{"name": "op-123", "url": "https://mlrun/op-123"}, updateErr: errors.New("update failed")},
			jobSet:          jobSet,
			wantCalls:       1,
			wantID:          "c0a5802815479054d7e6d629cf3339211c6f9520580f93a52b8d0e143495b033",
			wantGroup:       "group-abc",
			wantBucket:      "gs://bucket/folder",
			wantUpdateCalls: 1,
			wantEmptyURL:    true,
			wantConfirmed:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock, ok := tc.client.(*mockMLRunCreator)
			r := &Reconciler{
				Project:     "test-proj",
				Location:    "us-central1",
				ClusterName: "cluster-a",
				MLRunClient: tc.client,
			}
			gotURL, gotConfirmed := r.ensureMLRunCreated(context.Background(), tc.jobSet, "test-proj", "us-central1", "cluster-a", 10, 20, 30, "display-name", "group-abc", "gs://bucket/folder")
			if tc.wantEmptyURL && gotURL != "" {
				t.Errorf("ensureMLRunCreated() url = %q, want empty", gotURL)
			}
			if gotConfirmed != tc.wantConfirmed {
				t.Errorf("ensureMLRunCreated() confirmed = %v, want %v", gotConfirmed, tc.wantConfirmed)
			}

			if !ok {
				if tc.wantCalls > 0 {
					t.Fatalf("unexpected non-mock client")
				}
				return
			}
			if mock.createCalls != tc.wantCalls {
				t.Errorf("createCalls = %d, want %d", mock.createCalls, tc.wantCalls)
			}
			if tc.wantCalls == 0 {
				return
			}
			if mock.lastID != tc.wantID {
				t.Errorf("lastID = %q, want %q", mock.lastID, tc.wantID)
			}
			if mock.lastPhase != "ACTIVE" {
				t.Errorf("lastPhase = %q, want %q", mock.lastPhase, "ACTIVE")
			}
			if mock.lastOpts.RunGroup != tc.wantGroup {
				t.Errorf("RunGroup = %q, want %q", mock.lastOpts.RunGroup, tc.wantGroup)
			}
			if gotPath := mock.lastOpts.Artifacts["gcs_path"]; gotPath != tc.wantBucket {
				t.Errorf("gcs_path artifact = %q, want %q", gotPath, tc.wantBucket)
			}
			wantCluster := "projects/test-proj/locations/us-central1/clusters/cluster-a"
			if mock.lastOpts.WorkloadDetails.Cluster != wantCluster {
				t.Errorf("WorkloadDetails.Cluster = %q, want %q", mock.lastOpts.WorkloadDetails.Cluster, wantCluster)
			}
			if !maps.Equal(buildMLRunLabels(10, 20), mock.lastOpts.Labels) {
				t.Errorf("Labels = %v, want %v", mock.lastOpts.Labels, buildMLRunLabels(10, 20))
			}
			var wantCreateTime string
			if tc.jobSet != nil {
				wantCreateTime = formatCreationTime(tc.jobSet.CreationTimestamp)
			}
			if mock.lastOpts.WorkloadDetails.CreateTime != wantCreateTime {
				t.Errorf("WorkloadDetails.CreateTime = %q, want %q", mock.lastOpts.WorkloadDetails.CreateTime, wantCreateTime)
			}
			if mock.updateCalls != tc.wantUpdateCalls {
				t.Errorf("updateCalls = %d, want %d", mock.updateCalls, tc.wantUpdateCalls)
			}
			if tc.wantUpdateCalls > 0 {
				if mock.lastUpdateID != tc.wantID {
					t.Errorf("lastUpdateID = %q, want %q", mock.lastUpdateID, tc.wantID)
				}
				wantUpdateLabels := map[string]string{listExistingSessionsOnlyLabel: "true"}
				if !maps.Equal(wantUpdateLabels, mock.lastUpdateOpts.Labels) {
					t.Errorf("update Labels = %v, want %v", mock.lastUpdateOpts.Labels, wantUpdateLabels)
				}
			}
		})
	}
}

func TestCheckWorkUnitStatusAndMLRun(t *testing.T) {
	tests := []struct {
		name          string
		storage       ReconcilerStorage
		wantURL       string
		wantSkip      bool
		wantCachedURL string
	}{
		{
			name:          "nil storage",
			storage:       nil,
			wantURL:       "",
			wantSkip:      false,
			wantCachedURL: "",
		},
		{
			name:          "fetch error returns empty and false",
			storage:       &mockStorageUpdater{fetchErr: errors.New("fetch error")},
			wantURL:       "",
			wantSkip:      false,
			wantCachedURL: "",
		},
		{
			name: "running execution with matching eid caches mlrun url",
			storage: &mockStorageUpdater{
				fetchRes: &spb.WorkUnitExecution{
					Name:   "experiments/1/workUnits/1/executions/1",
					Status: &spb.WorkUnitStatus{State: spb.WorkUnitStatus_RUNNING},
					Metadata: &spb.WorkUnitExecutionMetadata{
						MlrunUrl: "https://mlrun.example.com/123",
					},
				},
			},
			wantURL:       "https://mlrun.example.com/123",
			wantSkip:      false,
			wantCachedURL: "https://mlrun.example.com/123",
		},
		{
			name: "stale execution mlrun url is not cached under reconciled eid",
			storage: &mockStorageUpdater{
				fetchRes: &spb.WorkUnitExecution{
					Name:   "experiments/1/workUnits/1/executions/2",
					Status: &spb.WorkUnitStatus{State: spb.WorkUnitStatus_RUNNING},
					Metadata: &spb.WorkUnitExecutionMetadata{
						MlrunUrl: "https://mlrun.example.com/123",
					},
				},
			},
			wantURL:       "https://mlrun.example.com/123",
			wantSkip:      false,
			wantCachedURL: "",
		},
		{
			name: "terminal status sets skip to true and caches matching eid url",
			storage: &mockStorageUpdater{
				fetchRes: &spb.WorkUnitExecution{
					Name:   "experiments/1/workUnits/1/executions/1",
					Status: &spb.WorkUnitStatus{State: spb.WorkUnitStatus_COMPLETED},
					Metadata: &spb.WorkUnitExecutionMetadata{
						MlrunUrl: "https://mlrun.example.com/123",
					},
				},
			},
			wantURL:       "https://mlrun.example.com/123",
			wantSkip:      true,
			wantCachedURL: "https://mlrun.example.com/123",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Reconciler{Storage: tc.storage}
			gotURL, gotSkip := r.checkWorkUnitStatusAndMLRun(context.Background(), 1, 1, 1)
			if gotURL != tc.wantURL || gotSkip != tc.wantSkip {
				t.Errorf("checkWorkUnitStatusAndMLRun() = (%q, %v), want (%q, %v)", gotURL, gotSkip, tc.wantURL, tc.wantSkip)
			}
			if gotCached := r.getCachedMLRunURL(1, 1, 1); gotCached != tc.wantCachedURL {
				t.Errorf("getCachedMLRunURL(1, 1, 1) = %q, want %q", gotCached, tc.wantCachedURL)
			}
		})
	}
}

func TestUpsertExistingMLRun(t *testing.T) {
	conflictErr := &hypercomputeclient.APIError{StatusCode: http.StatusConflict, Body: "ALREADY_EXISTS"}
	tests := []struct {
		name            string
		client          *mockMLRunCreator
		wantURLContains string
		wantConfirmed   bool
		wantGetCalls    int
		wantUpdateCalls int
	}{
		{
			name: "already exists, owned and has scan label is idempotent success",
			client: &mockMLRunCreator{getRes: map[string]any{
				"labels": map[string]any{
					"created_by":                  createdByXManagerLabelValue,
					listExistingSessionsOnlyLabel: "true",
				},
			}},
			wantURLContains: "console.cloud.google.com",
			wantConfirmed:   true,
			wantGetCalls:    1,
			wantUpdateCalls: 0,
		},
		{
			name: "already exists, owned but missing scan label triggers update",
			client: &mockMLRunCreator{
				getRes: map[string]any{
					"labels": map[string]any{"created_by": createdByXManagerLabelValue},
				},
				updateRes: map[string]any{"url": "https://mlrun/updated"},
			},
			wantURLContains: "https://mlrun/updated",
			wantConfirmed:   true,
			wantGetCalls:    1,
			wantUpdateCalls: 1,
		},
		{
			name: "already exists and foreign is updated",
			client: &mockMLRunCreator{
				getRes:    map[string]any{"labels": map[string]any{"created_by": "someone-else"}},
				updateRes: map[string]any{"url": "https://mlrun/updated"},
			},
			wantURLContains: "https://mlrun/updated",
			wantConfirmed:   true,
			wantGetCalls:    1,
			wantUpdateCalls: 1,
		},
		{
			name:            "get failure returns unconfirmed console url fallback",
			client:          &mockMLRunCreator{getErr: errors.New("not found")},
			wantURLContains: "console.cloud.google.com",
			wantConfirmed:   false,
			wantGetCalls:    1,
			wantUpdateCalls: 0,
		},
		{
			name: "update failure returns unconfirmed console url fallback",
			client: &mockMLRunCreator{
				getRes:    map[string]any{"labels": map[string]any{"created_by": "someone-else"}},
				updateErr: errors.New("update failed"),
			},
			wantURLContains: "console.cloud.google.com",
			wantConfirmed:   false,
			wantGetCalls:    1,
			wantUpdateCalls: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Reconciler{Project: "proj", Location: "us-central1", ClusterName: "clus", MLRunClient: tc.client}
			got, gotConfirmed := r.upsertExistingMLRun(context.Background(), conflictErr, "xmc-fallback-id", "disp", "grp", buildMLRunLabels(1, 2), map[string]string{"gcs_path": "gs://b/o"})
			if !strings.Contains(got, tc.wantURLContains) {
				t.Errorf("upsertExistingMLRun() = %q, want containing %q", got, tc.wantURLContains)
			}
			if gotConfirmed != tc.wantConfirmed {
				t.Errorf("upsertExistingMLRun() confirmed = %v, want %v", gotConfirmed, tc.wantConfirmed)
			}
			if tc.client.getCalls != tc.wantGetCalls {
				t.Errorf("getCalls = %d, want %d", tc.client.getCalls, tc.wantGetCalls)
			}
			if tc.client.updateCalls != tc.wantUpdateCalls {
				t.Errorf("updateCalls = %d, want %d", tc.client.updateCalls, tc.wantUpdateCalls)
			}
		})
	}
}

func TestIsStaleExecutionForMLRun(t *testing.T) {
	tests := []struct {
		name          string
		executionName string
		reconciledEID int64
		want          bool
	}{
		{
			name:          "same eid is not stale",
			executionName: "experiments/1/workUnits/2/executions/3",
			reconciledEID: 3,
			want:          false,
		},
		{
			name:          "different eid is stale",
			executionName: "experiments/1/workUnits/2/executions/3",
			reconciledEID: 4,
			want:          true,
		},
		{
			name:          "unparseable name is not stale",
			executionName: "garbage",
			reconciledEID: 3,
			want:          false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStaleExecutionForMLRun(tc.executionName, tc.reconciledEID); got != tc.want {
				t.Errorf("isStaleExecutionForMLRun(%q, %d) = %v, want %v", tc.executionName, tc.reconciledEID, got, tc.want)
			}
		})
	}
}

func TestSyncMLRunCachesURLBeforeBatchFlushAndEvictsOnDelete(t *testing.T) {
	now := time.UnixMilli(1680000000000)
	jobSet := withManagedXprofEnvs(&v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-jobset",
			Namespace:         "test-ns",
			CreationTimestamp: metav1.NewTime(now),
		},
	})
	// Storage simulates the 250ms batcher flush window: MlrunUrl is still empty in ESS.
	storage := &mockStorageUpdater{
		fetchRes: &spb.WorkUnitExecution{
			Name:     "experiments/10/workUnits/20/executions/30",
			Status:   &spb.WorkUnitStatus{State: spb.WorkUnitStatus_RUNNING},
			Metadata: &spb.WorkUnitExecutionMetadata{MlrunUrl: ""},
		},
	}
	client := &mockMLRunCreator{
		retRes:    map[string]any{"name": "op-123", "url": "https://mlrun/op-123"},
		updateRes: map[string]any{"url": "https://mlrun/op-123"},
	}
	r := &Reconciler{
		Project:     "test-proj",
		Location:    "us-central1",
		ClusterName: "cluster-a",
		Storage:     storage,
		MLRunClient: client,
	}

	const wantURL = "https://mlrun/op-123?tab=profiles"
	got1 := r.syncMLRun(context.Background(), jobSet, "test-proj", "us-central1", "cluster-a", 10, 20, 30)
	if got1 != wantURL {
		t.Fatalf("first syncMLRun() = %q, want %q", got1, wantURL)
	}

	// Second reconcile arrives ~70ms later before batcher flushes MlrunUrl to Spanner.
	got2 := r.syncMLRun(context.Background(), jobSet, "test-proj", "us-central1", "cluster-a", 10, 20, 30)
	if got2 != wantURL {
		t.Fatalf("second syncMLRun() = %q, want %q", got2, wantURL)
	}
	if client.createCalls != 1 {
		t.Errorf("createCalls = %d, want 1 (duplicate CreateMLRun during batch flush window)", client.createCalls)
	}
	if client.updateCalls != 1 {
		t.Errorf("updateCalls = %d, want 1", client.updateCalls)
	}

	// Evicting on JobSet deletion must drop cached entries with EID <= 30.
	r.evictPendingJobSetUpdate(10, 20, 30)
	got3 := r.syncMLRun(context.Background(), jobSet, "test-proj", "us-central1", "cluster-a", 10, 20, 30)
	if got3 != wantURL {
		t.Fatalf("third syncMLRun() after eviction = %q, want %q", got3, wantURL)
	}
	if client.createCalls != 2 {
		t.Errorf("createCalls after eviction = %d, want 2", client.createCalls)
	}
}

func TestSyncMLRunDoesNotCacheUnconfirmedFallbackURL(t *testing.T) {
	now := time.UnixMilli(1680000000000)
	jobSet := withManagedXprofEnvs(&v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-jobset",
			Namespace:         "test-ns",
			CreationTimestamp: metav1.NewTime(now),
		},
	})
	storage := &mockStorageUpdater{
		fetchRes: &spb.WorkUnitExecution{
			Name:     "experiments/10/workUnits/20/executions/30",
			Status:   &spb.WorkUnitStatus{State: spb.WorkUnitStatus_RUNNING},
			Metadata: &spb.WorkUnitExecutionMetadata{MlrunUrl: ""},
		},
	}
	conflictErr := &hypercomputeclient.APIError{StatusCode: http.StatusConflict, Body: "ALREADY_EXISTS"}
	client := &mockMLRunCreator{
		retErr:    conflictErr,
		getRes:    map[string]any{"labels": map[string]any{"created_by": "someone-else"}},
		updateErr: errors.New("transient update error"),
	}
	r := &Reconciler{
		Project:     "test-proj",
		Location:    "us-central1",
		ClusterName: "cluster-a",
		Storage:     storage,
		MLRunClient: client,
	}

	// First reconcile hits 409 + UpdateMLRun error and returns fallback console URL,
	// which must not be cached so the next reconcile retries enrichment.
	got1 := r.syncMLRun(context.Background(), jobSet, "test-proj", "us-central1", "cluster-a", 10, 20, 30)
	if !strings.Contains(got1, "console.cloud.google.com") {
		t.Fatalf("first syncMLRun() = %q, want fallback console URL", got1)
	}
	if cached := r.getCachedMLRunURL(10, 20, 30); cached != "" {
		t.Fatalf("getCachedMLRunURL() = %q after unconfirmed fallback, want empty", cached)
	}

	// Second reconcile succeeds on UpdateMLRun and caches the confirmed URL.
	client.updateErr = nil
	client.updateRes = map[string]any{"url": "https://mlrun/enriched"}
	const wantEnrichedURL = "https://mlrun/enriched?tab=profiles"
	got2 := r.syncMLRun(context.Background(), jobSet, "test-proj", "us-central1", "cluster-a", 10, 20, 30)
	if got2 != wantEnrichedURL {
		t.Fatalf("second syncMLRun() = %q, want %q", got2, wantEnrichedURL)
	}
	if client.updateCalls != 2 {
		t.Errorf("updateCalls = %d, want 2 (retry after unconfirmed fallback)", client.updateCalls)
	}
	if cached := r.getCachedMLRunURL(10, 20, 30); cached != wantEnrichedURL {
		t.Errorf("getCachedMLRunURL() = %q, want %q", cached, wantEnrichedURL)
	}
}
