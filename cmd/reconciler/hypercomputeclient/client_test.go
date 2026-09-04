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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

type mockTokenSource struct{}

func (m mockTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "mock-token"}, nil
}

func TestResolveOperation(t *testing.T) {
	tests := []struct {
		name       string
		initialLRO map[string]any
		handler    http.HandlerFunc
		want       map[string]any
		wantErr    string
	}{
		{
			name: "completed success with response",
			initialLRO: map[string]any{
				"done":     true,
				"name":     "operations/op-123",
				"response": map[string]any{"displayName": "run-1"},
			},
			want: map[string]any{"displayName": "run-1"},
		},
		{
			name: "completed with error",
			initialLRO: map[string]any{
				"done": true,
				"name": "operations/op-123",
				"error": map[string]any{
					"code":    3,
					"message": "failed execution",
				},
			},
			wantErr: "failed execution",
		},
		{
			name: "pending success after polling",
			initialLRO: map[string]any{
				"done": false,
				"name": "operations/op-123",
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/operations/op-123") {
					op := map[string]any{
						"done":     true,
						"name":     "operations/op-123",
						"response": map[string]any{"displayName": "run-1"},
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(op)
					return
				}
				http.Error(w, "not found", http.StatusNotFound)
			},
			want: map[string]any{"displayName": "run-1"},
		},
		{
			name: "pending error after polling",
			initialLRO: map[string]any{
				"done": false,
				"name": "operations/op-123",
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/operations/op-123") {
					op := map[string]any{
						"done": true,
						"name": "operations/op-123",
						"error": map[string]any{
							"message": "polling failed",
						},
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(op)
					return
				}
				http.Error(w, "not found", http.StatusNotFound)
			},
			wantErr: "polling failed",
		},
		{
			name: "completed success fallback to target",
			initialLRO: map[string]any{
				"done": true,
				"name": "operations/op-123",
				"metadata": map[string]any{
					"target": "projects/p/locations/l/machineLearningRuns/run-fallback",
				},
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/machineLearningRuns/run-fallback") {
					run := map[string]any{"displayName": "fallback-run"}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(run)
					return
				}
				http.Error(w, "not found", http.StatusNotFound)
			},
			want: map[string]any{"displayName": "fallback-run"},
		},
		{
			name:       "nil operation result",
			initialLRO: nil,
			wantErr:    "operation result is nil",
		},
		{
			name: "missing name for pending operation",
			initialLRO: map[string]any{
				"done": false,
			},
			wantErr: "operation name is empty",
		},
		{
			name: "missing response and target metadata",
			initialLRO: map[string]any{
				"done": true,
				"name": "operations/op-123",
			},
			wantErr: "no response or target in operation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.handler != nil {
					tc.handler(w, r)
					return
				}
				http.Error(w, "not configured", http.StatusInternalServerError)
			}))
			defer server.Close()

			client := &ControlPlaneClient{
				projectID:   "test-project",
				location:    "us-central1",
				baseURL:     server.URL,
				mlRunsPath:  server.URL + "/projects/test-project/locations/us-central1/machineLearningRuns",
				tokenSource: mockTokenSource{},
				httpClient:  server.Client(),
			}

			got, err := client.resolveOperation(context.Background(), tc.initialLRO)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("resolveOperation() returned nil error, want error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("resolveOperation() returned error %v, want error containing %q", err, tc.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("resolveOperation() returned unexpected error: %v", err)
			}

			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("resolveOperation() = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

// newTestClient builds a client whose endpoints point at the given test server.
func newTestClient(server *httptest.Server) *ControlPlaneClient {
	return &ControlPlaneClient{
		projectID:   "test-project",
		location:    "us-central1",
		baseURL:     server.URL,
		mlRunsPath:  server.URL + "/projects/test-project/locations/us-central1/machineLearningRuns",
		tokenSource: mockTokenSource{},
		httpClient:  server.Client(),
	}
}

func TestResolveEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		location    string
		wantBaseURL string
		wantPath    string
		wantLoc     string
		wantErr     string
	}{
		{
			name:        "prod defaults location",
			environment: "prod",
			location:    "",
			wantBaseURL: "https://hypercomputecluster.googleapis.com/v1alpha",
			wantPath:    "https://hypercomputecluster.googleapis.com/v1alpha/projects/p/locations/us-central1/machineLearningRuns",
			wantLoc:     "us-central1",
		},
		{
			name:        "staging honors explicit location",
			environment: "staging",
			location:    "europe-west4",
			wantBaseURL: "https://staging-hypercomputecluster.sandbox.googleapis.com/v1alpha",
			wantPath:    "https://staging-hypercomputecluster.sandbox.googleapis.com/v1alpha/projects/p/locations/europe-west4/machineLearningRuns",
			wantLoc:     "europe-west4",
		},
		{
			name:        "autopush defaults location",
			environment: "autopush",
			location:    "",
			wantBaseURL: "https://autopush-hypercomputecluster.sandbox.googleapis.com/v1alpha",
			wantPath:    "https://autopush-hypercomputecluster.sandbox.googleapis.com/v1alpha/projects/p/locations/us-central1/machineLearningRuns",
			wantLoc:     "us-central1",
		},
		{
			name:        "invalid environment errors",
			environment: "bogus",
			wantErr:     "invalid environment",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := resolveEndpoint("p", tc.environment, tc.location)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("resolveEndpoint() error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveEndpoint() unexpected error: %v", err)
			}
			if cfg.baseURL != tc.wantBaseURL {
				t.Errorf("baseURL = %q, want %q", cfg.baseURL, tc.wantBaseURL)
			}
			if cfg.mlRunsPath != tc.wantPath {
				t.Errorf("mlRunsPath = %q, want %q", cfg.mlRunsPath, tc.wantPath)
			}
			if cfg.location != tc.wantLoc {
				t.Errorf("location = %q, want %q", cfg.location, tc.wantLoc)
			}
		})
	}
}

func TestNewControlPlaneClientInvalidEnvironment(t *testing.T) {
	// The valid-environment path calls google.DefaultTokenSource, which needs
	// ambient credentials and cannot run hermetically; URL construction is
	// covered by TestResolveEndpoint. Here we only assert the exported
	// constructor surfaces the validation error.
	_, err := NewControlPlaneClient(context.Background(), "p", "bogus", "")
	if err == nil || !strings.Contains(err.Error(), "invalid environment") {
		t.Fatalf("NewControlPlaneClient() error = %v, want containing %q", err, "invalid environment")
	}
}

func TestGetOperation(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"done": true, "name": "operations/op-1"})
	}))
	defer server.Close()

	got, err := newTestClient(server).GetOperation(context.Background(), "operations/op-1")
	if err != nil {
		t.Fatalf("GetOperation() unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want %q", gotMethod, http.MethodGet)
	}
	if gotPath != "/operations/op-1" {
		t.Errorf("path = %q, want %q", gotPath, "/operations/op-1")
	}
	if done, _ := got["done"].(bool); !done {
		t.Errorf("done = %v, want true", got["done"])
	}
}

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
		// The API only accepts update_mask=*, which clears any field absent from the
		// body. orchestrator/workloadDetails are immutable after creation, so they must
		// be re-sent unchanged from the GET rather than dropped.
		if patchBody["orchestrator"] != "GKE" {
			t.Errorf("orchestrator = %v, want %q (must be preserved from GET)", patchBody["orchestrator"], "GKE")
		}
		if _, ok := patchBody["workloadDetails"]; !ok {
			t.Errorf("workloadDetails missing, want preserved from GET")
		}
		if got["displayName"] != "new" {
			t.Errorf("response displayName = %v, want %q", got["displayName"], "new")
		}
	})

	t.Run("labels-only update preserves orchestrator and uses mask=*", func(t *testing.T) {
		var patchMask string
		var patchBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.Method {
			case http.MethodGet:
				json.NewEncoder(w).Encode(map[string]any{
					"orchestrator":    "GKE",
					"workloadDetails": map[string]any{"gke": map[string]any{"id": "js-1"}},
					"labels":          map[string]any{"experiment-id": "6170"},
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
			Labels: map[string]string{"experiment-id": "6170", "list_existing_sessions_only": "true"},
			Force:  true,
		})
		if err != nil {
			t.Fatalf("UpdateMLRun() unexpected error: %v", err)
		}
		if patchMask != "*" {
			t.Errorf("update_mask = %q, want %q", patchMask, "*")
		}
		if patchBody["orchestrator"] != "GKE" {
			t.Errorf("orchestrator = %v, want %q (labels-only update must not clear it)", patchBody["orchestrator"], "GKE")
		}
		labels, ok := patchBody["labels"].(map[string]any)
		if !ok || labels["list_existing_sessions_only"] != "true" {
			t.Errorf("labels = %v, want list_existing_sessions_only=true", patchBody["labels"])
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

func TestWaitForOperationNilResponse(t *testing.T) {
	// GET returns 200 with an empty body, so GetOperation yields a nil op.
	// WaitForOperation must fail fast with a clear error instead of polling
	// until the deadline.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // empty body
	}))
	defer server.Close()

	got, err := newTestClient(server).WaitForOperation(context.Background(), "op-1", nil)
	if err == nil {
		t.Fatalf("WaitForOperation() error = nil, want non-nil for empty response")
	}
	if got != nil {
		t.Errorf("WaitForOperation() = %v, want nil result on error", got)
	}
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("error = %q, want containing %q", err.Error(), "empty response")
	}
}

func TestResolveEndpointEmptyProjectID(t *testing.T) {
	// An empty projectID must be rejected up front to avoid malformed URLs.
	if _, err := resolveEndpoint("", "prod", "us-central1"); err == nil {
		t.Fatal("resolveEndpoint() error = nil, want error for empty projectID")
	}
}
