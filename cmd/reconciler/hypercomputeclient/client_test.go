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
