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
)

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

			got, err := newTestClient(server).resolveOperation(context.Background(), tc.initialLRO)
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
