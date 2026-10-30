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

package hypercomputeclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"xmanager_cloud/internal/logging"
)

func TestGetMLRun(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"displayName": "run-1"})
	}))
	defer server.Close()

	got, err := newTestClient(server).GetMLRun(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("GetMLRun() unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want %q", gotMethod, http.MethodGet)
	}
	if want := "/projects/test-project/locations/us-central1/machineLearningRuns/run-1"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if got["displayName"] != "run-1" {
		t.Errorf("displayName = %v, want %q", got["displayName"], "run-1")
	}
}

func TestCreateMLRun(t *testing.T) {
	var gotMethod, gotPath, gotRunID string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotRunID = r.URL.Query().Get("machine_learning_run_id")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"done":     true,
			"response": map[string]any{"displayName": "My Run"},
		})
	}))
	defer server.Close()

	opts := &CreateMLRunOptions{RunGroup: "grp-1", Labels: map[string]string{"team": "xmc"}}
	got, err := newTestClient(server).CreateMLRun(context.Background(), "My Run", "My Run", "RUN_PHASE_TRAINING", opts)
	if err != nil {
		t.Fatalf("CreateMLRun() unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want %q", gotMethod, http.MethodPost)
	}
	if !strings.HasSuffix(gotPath, "/machineLearningRuns") {
		t.Errorf("path = %q, want suffix %q", gotPath, "/machineLearningRuns")
	}
	if want := SanitizeIdentifier("My Run"); gotRunID != want {
		t.Errorf("machine_learning_run_id = %q, want %q", gotRunID, want)
	}
	for k, want := range map[string]any{
		"name":        "My Run",
		"displayName": "My Run",
		"runPhase":    "RUN_PHASE_TRAINING",
		"runSet":      "grp-1",
	} {
		if gotBody[k] != want {
			t.Errorf("body[%q] = %v, want %v", k, gotBody[k], want)
		}
	}
	if _, ok := gotBody["labels"]; !ok {
		t.Errorf("body missing labels")
	}
	if got["displayName"] != "My Run" {
		t.Errorf("response displayName = %v, want %q", got["displayName"], "My Run")
	}
}

func TestCreateMLRunErrorLogging(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		wantErrorLog bool
	}{
		{
			name:         "409 conflict does not emit ERROR log",
			statusCode:   http.StatusConflict,
			wantErrorLog: false,
		},
		{
			name:         "500 internal error emits ERROR log",
			statusCode:   http.StatusInternalServerError,
			wantErrorLog: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, `{"error":"boom"}`, tc.statusCode)
			}))
			defer server.Close()

			var logBuf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError}))
			ctx := logging.WithLogger(context.Background(), logger)

			_, err := newTestClient(server).CreateMLRun(ctx, "run-1", "run-1", "ACTIVE", nil)
			if err == nil {
				t.Fatalf("CreateMLRun() expected error for status %d, got nil", tc.statusCode)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.statusCode {
				t.Fatalf("CreateMLRun() error = %v, want APIError with status %d", err, tc.statusCode)
			}

			gotErrorLog := strings.Contains(logBuf.String(), "CreateMLRun failed")
			if gotErrorLog != tc.wantErrorLog {
				t.Errorf("gotErrorLog = %v (logs=%s), want %v", gotErrorLog, logBuf.String(), tc.wantErrorLog)
			}
		})
	}
}

func TestUpdateMLRun(t *testing.T) {
	t.Run("no-op returns current without patch", func(t *testing.T) {
		patchCalled := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPatch {
				patchCalled = true
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"displayName": "old", "createTime": "t0"})
		}))
		defer server.Close()

		got, err := newTestClient(server).UpdateMLRun(context.Background(), "run-1", &UpdateMLRunOptions{})
		if err != nil {
			t.Fatalf("UpdateMLRun() unexpected error: %v", err)
		}
		if patchCalled {
			t.Errorf("PATCH was called, want no-op")
		}
		if got["displayName"] != "old" {
			t.Errorf("displayName = %v, want %q", got["displayName"], "old")
		}
	})

	t.Run("update patches with mask and strips output-only fields", func(t *testing.T) {
		var patchMask string
		var patchBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.Method {
			case http.MethodGet:
				json.NewEncoder(w).Encode(map[string]any{
					"displayName":     "old",
					"runPhase":        "RUN_PHASE_TRAINING",
					"createTime":      "t0",
					"updateTime":      "t1",
					"endTime":         "t2",
					"orchestrator":    "GKE",
					"workloadDetails": map[string]any{"gke": map[string]any{"id": "js-1"}},
				})
			case http.MethodPatch:
				patchMask = r.URL.Query().Get("update_mask")
				json.NewDecoder(r.Body).Decode(&patchBody)
				json.NewEncoder(w).Encode(map[string]any{
					"done":     true,
					"response": map[string]any{"displayName": "new"},
				})
			default:
				http.Error(w, "unexpected method", http.StatusInternalServerError)
			}
		}))
		defer server.Close()

		got, err := newTestClient(server).UpdateMLRun(context.Background(), "run-1", &UpdateMLRunOptions{DisplayName: "new"})
		if err != nil {
			t.Fatalf("UpdateMLRun() unexpected error: %v", err)
		}
		if patchMask != "*" {
			t.Errorf("update_mask = %q, want %q", patchMask, "*")
		}
		if patchBody["displayName"] != "new" {
			t.Errorf("patched displayName = %v, want %q", patchBody["displayName"], "new")
		}
		for _, f := range []string{"createTime", "updateTime", "endTime"} {
			if _, ok := patchBody[f]; ok {
				t.Errorf("patch body still contains output-only field %q", f)
			}
		}
		if patchBody["orchestrator"] != "GKE" {
			t.Errorf("orchestrator = %v, want %q", patchBody["orchestrator"], "GKE")
		}
		if _, ok := patchBody["workloadDetails"]; !ok {
			t.Errorf("workloadDetails missing, want preserved from GET")
		}
		if got["displayName"] != "new" {
			t.Errorf("response displayName = %v, want %q", got["displayName"], "new")
		}
	})

	t.Run("keeps orchestrator when explicitly updated", func(t *testing.T) {
		var patchBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.Method {
			case http.MethodGet:
				json.NewEncoder(w).Encode(map[string]any{"displayName": "old", "orchestrator": "STALE"})
			case http.MethodPatch:
				json.NewDecoder(r.Body).Decode(&patchBody)
				json.NewEncoder(w).Encode(map[string]any{"done": true, "response": map[string]any{}})
			default:
				http.Error(w, "unexpected method", http.StatusInternalServerError)
			}
		}))
		defer server.Close()

		_, err := newTestClient(server).UpdateMLRun(context.Background(), "run-1", &UpdateMLRunOptions{
			Orchestrator:    "GKE",
			WorkloadDetails: &GKEWorkloadDetails{},
		})
		if err != nil {
			t.Fatalf("UpdateMLRun() unexpected error: %v", err)
		}
		if patchBody["orchestrator"] != "GKE" {
			t.Errorf("orchestrator = %v, want %q (explicit update must be kept)", patchBody["orchestrator"], "GKE")
		}
		if _, ok := patchBody["workloadDetails"]; !ok {
			t.Errorf("workloadDetails missing, want present on explicit orchestrator update")
		}
	})

	t.Run("labels-only update merges labels and preserves orchestrator", func(t *testing.T) {
		var patchMask string
		var patchBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.Method {
			case http.MethodGet:
				json.NewEncoder(w).Encode(map[string]any{
					"orchestrator":    "GKE",
					"workloadDetails": map[string]any{"gke": map[string]any{"id": "js-1"}},
					"labels":          map[string]any{"experiment_id": "100", "created_by": "xmanager"},
				})
			case http.MethodPatch:
				patchMask = r.URL.Query().Get("update_mask")
				json.NewDecoder(r.Body).Decode(&patchBody)
				json.NewEncoder(w).Encode(map[string]any{"done": true, "response": map[string]any{}})
			default:
				http.Error(w, "unexpected method", http.StatusInternalServerError)
			}
		}))
		defer server.Close()

		_, err := newTestClient(server).UpdateMLRun(context.Background(), "run-1", &UpdateMLRunOptions{
			Labels: map[string]string{"list_existing_sessions_only": "true"},
		})
		if err != nil {
			t.Fatalf("UpdateMLRun() unexpected error: %v", err)
		}
		if patchMask != "*" {
			t.Errorf("update_mask = %q, want %q", patchMask, "*")
		}
		if patchBody["orchestrator"] != "GKE" {
			t.Errorf("orchestrator = %v, want %q (must not be cleared)", patchBody["orchestrator"], "GKE")
		}
		labels, ok := patchBody["labels"].(map[string]any)
		if !ok {
			t.Fatalf("labels missing from patch body")
		}
		wantLabels := map[string]any{
			"experiment_id":               "100",
			"created_by":                  "xmanager",
			"list_existing_sessions_only": "true",
		}
		if !reflect.DeepEqual(labels, wantLabels) {
			t.Errorf("labels = %v, want %v", labels, wantLabels)
		}
	})
}

func TestUpdateMLRunNilPayloadNoPanic(t *testing.T) {
	// GET returns 200 with an empty body, so GetMLRun yields a nil payload.
	// UpdateMLRun must return an error instead of panicking on a nil-map write.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // empty body
	}))
	defer server.Close()

	got, err := newTestClient(server).UpdateMLRun(context.Background(), "run-1", &UpdateMLRunOptions{DisplayName: "new"})
	if err == nil {
		t.Fatalf("UpdateMLRun() error = nil, want non-nil for empty payload")
	}
	if got != nil {
		t.Errorf("UpdateMLRun() = %v, want nil result on error", got)
	}
	if !strings.Contains(err.Error(), "empty payload") {
		t.Errorf("error = %q, want containing %q", err.Error(), "empty payload")
	}
}

func TestMergeStringMap(t *testing.T) {
	tests := []struct {
		name        string
		payload     map[string]any
		field       string
		updates     map[string]string
		wantChanged bool
		wantMap     map[string]any
	}{
		{
			name:        "nil updates returns false",
			payload:     map[string]any{"labels": map[string]any{"k1": "v1"}},
			field:       "labels",
			updates:     nil,
			wantChanged: false,
			wantMap:     map[string]any{"k1": "v1"},
		},
		{
			name:        "empty updates returns false",
			payload:     map[string]any{"labels": map[string]any{"k1": "v1"}},
			field:       "labels",
			updates:     map[string]string{},
			wantChanged: false,
			wantMap:     map[string]any{"k1": "v1"},
		},
		{
			name:        "missing field creates map and populates",
			payload:     map[string]any{},
			field:       "labels",
			updates:     map[string]string{"k1": "v1"},
			wantChanged: true,
			wantMap:     map[string]any{"k1": "v1"},
		},
		{
			name:        "nil field creates map and populates",
			payload:     map[string]any{"labels": nil},
			field:       "labels",
			updates:     map[string]string{"k1": "v1"},
			wantChanged: true,
			wantMap:     map[string]any{"k1": "v1"},
		},
		{
			name:        "malformed non-map field re-initializes map",
			payload:     map[string]any{"labels": "not-a-map"},
			field:       "labels",
			updates:     map[string]string{"k1": "v1"},
			wantChanged: true,
			wantMap:     map[string]any{"k1": "v1"},
		},
		{
			name:        "existing identical key-values returns unchanged",
			payload:     map[string]any{"labels": map[string]any{"k1": "v1"}},
			field:       "labels",
			updates:     map[string]string{"k1": "v1"},
			wantChanged: false,
			wantMap:     map[string]any{"k1": "v1"},
		},
		{
			name:        "existing key value updated returns true",
			payload:     map[string]any{"labels": map[string]any{"k1": "old"}},
			field:       "labels",
			updates:     map[string]string{"k1": "new"},
			wantChanged: true,
			wantMap:     map[string]any{"k1": "new"},
		},
		{
			name: "merges new keys into existing keys",
			payload: map[string]any{
				"labels": map[string]any{"k1": "v1", "k2": "v2"},
			},
			field: "labels",
			updates: map[string]string{
				"k2": "updated",
				"k3": "v3",
			},
			wantChanged: true,
			wantMap: map[string]any{
				"k1": "v1",
				"k2": "updated",
				"k3": "v3",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotChanged := mergeStringMap(tc.payload, tc.field, tc.updates)
			if gotChanged != tc.wantChanged {
				t.Errorf("mergeStringMap() changed = %v, want %v", gotChanged, tc.wantChanged)
			}
			gotMap, _ := tc.payload[tc.field].(map[string]any)
			if !reflect.DeepEqual(gotMap, tc.wantMap) {
				t.Errorf("merged map = %v, want %v", gotMap, tc.wantMap)
			}
		})
	}
}
