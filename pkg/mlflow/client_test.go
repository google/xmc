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

package mlflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeMLflowServer struct {
	mu          sync.Mutex
	experiments map[string]string
	runs        map[string]*mlflowRun
	runExpID    map[string]string
	expSeq      int
	runSeq      int
	authHeader  string
	sawEndTime  bool
}

func newFakeMLflowServer(authHeader string) *fakeMLflowServer {
	return &fakeMLflowServer{
		experiments: make(map[string]string),
		runs:        make(map[string]*mlflowRun),
		runExpID:    make(map[string]string),
		authHeader:  authHeader,
	}
}

func (f *fakeMLflowServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.authHeader != "" && r.Header.Get("Authorization") != f.authHeader {
		http.Error(w, `{"error_code":"PERMISSION_DENIED","message":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	switch r.URL.Path {
	case "/api/2.0/mlflow/experiments/get-by-name":
		name := r.URL.Query().Get("experiment_name")
		id, ok := f.experiments[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error_code": "RESOURCE_DOES_NOT_EXIST", "message": "not found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"experiment": map[string]string{"experiment_id": id}})
	case "/api/2.0/mlflow/experiments/create":
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, exists := f.experiments[req.Name]; exists {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error_code": "RESOURCE_ALREADY_EXISTS", "message": "exists"})
			return
		}
		f.expSeq++
		id := fmt.Sprintf("%d", f.expSeq)
		f.experiments[req.Name] = id
		_ = json.NewEncoder(w).Encode(map[string]string{"experiment_id": id})
	case "/api/2.0/mlflow/runs/search":
		var req struct {
			ExperimentIDs []string `json:"experiment_ids"`
			Filter        string   `json:"filter"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var matched []mlflowRun
		for id, run := range f.runs {
			if len(req.ExperimentIDs) > 0 && f.runExpID[id] != req.ExperimentIDs[0] {
				continue
			}
			if matchesFilter(run, req.Filter) {
				matched = append(matched, *run)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"runs": matched})
	case "/api/2.0/mlflow/runs/create":
		var req struct {
			ExperimentID string   `json:"experiment_id"`
			RunName      string   `json:"run_name"`
			Tags         []runTag `json:"tags"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.runSeq++
		id := fmt.Sprintf("run-%d", f.runSeq)
		tags := append([]runTag{{Key: "mlflow.runName", Value: req.RunName}}, req.Tags...)
		run := &mlflowRun{Info: runInfo{RunID: id, Status: "RUNNING"}, Data: runData{Tags: tags}}
		f.runs[id] = run
		f.runExpID[id] = req.ExperimentID
		_ = json.NewEncoder(w).Encode(map[string]any{"run": run})
	case "/api/2.0/mlflow/runs/set-tag":
		var req struct {
			RunID string `json:"run_id"`
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if run, ok := f.runs[req.RunID]; ok {
			updated := false
			for i := range run.Data.Tags {
				if run.Data.Tags[i].Key == req.Key {
					run.Data.Tags[i].Value = req.Value
					updated = true
					break
				}
			}
			if !updated {
				run.Data.Tags = append(run.Data.Tags, runTag{Key: req.Key, Value: req.Value})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{})
	case "/api/2.0/mlflow/runs/update":
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, hasEnd := req["end_time"]; hasEnd {
			f.sawEndTime = true
		}
		runID, _ := req["run_id"].(string)
		status, _ := req["status"].(string)
		if run, ok := f.runs[runID]; ok {
			run.Info.Status = status
		}
		_ = json.NewEncoder(w).Encode(map[string]any{})
	default:
		http.NotFound(w, r)
	}
}

func matchesFilter(run *mlflowRun, filter string) bool {
	parts := strings.Split(filter, " AND ")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		keyVal := strings.SplitN(part, "=", 2)
		if len(keyVal) != 2 {
			continue
		}
		key := strings.TrimSpace(keyVal[0])
		key = strings.TrimPrefix(key, "tags.")
		key = strings.Trim(key, "`")
		val := strings.Trim(strings.TrimSpace(keyVal[1]), "'")
		got, ok := findTag(run.Data.Tags, key)
		if !ok || got != val {
			return false
		}
	}
	return true
}

func TestClient_RunLifecycleAndIdempotency(t *testing.T) {
	srv := newFakeMLflowServer("Bearer tok-secret")
	ts := httptest.NewServer(srv)
	defer ts.Close()

	client, err := NewClient(&Config{
		TrackingURI: ts.URL,
		creds:       Credentials{token: "tok-secret"},
	}, ts.Client())
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	ctx := context.Background()

	expID, err := client.EnsureExperiment(ctx, "sweep-title")
	if err != nil {
		t.Fatalf("EnsureExperiment() error: %v", err)
	}
	parentID, err := client.EnsureParentRun(ctx, expID, 100)
	if err != nil {
		t.Fatalf("EnsureParentRun() error: %v", err)
	}
	child1, err := client.EnsureChildRun(ctx, expID, parentID, 100, 1, 1)
	if err != nil {
		t.Fatalf("EnsureChildRun(1) error: %v", err)
	}

	// Second work unit reuses experiment and parent run.
	expID2, err := client.EnsureExperiment(ctx, "sweep-title")
	if err != nil || expID2 != expID {
		t.Fatalf("EnsureExperiment(2) = (%q, %v), want %q", expID2, err, expID)
	}
	parentID2, err := client.EnsureParentRun(ctx, expID2, 100)
	if err != nil || parentID2 != parentID {
		t.Fatalf("EnsureParentRun(2) = (%q, %v), want %q", parentID2, err, parentID)
	}
	child2, err := client.EnsureChildRun(ctx, expID2, parentID2, 100, 2, 1)
	if err != nil || child2 == child1 {
		t.Fatalf("EnsureChildRun(2) = (%q, %v), want distinct from %q", child2, err, child1)
	}

	// Retry/restart of work unit 1 reuses child1 and updates xmc.execution_id.
	child1Retry, err := client.EnsureChildRun(ctx, expID, parentID, 100, 1, 2)
	if err != nil || child1Retry != child1 {
		t.Fatalf("EnsureChildRun(retry) = (%q, %v), want %q", child1Retry, err, child1)
	}

	if srv.sawEndTime {
		t.Errorf("runs/update for SCHEDULED unexpectedly included end_time")
	}
	if len(srv.runs) != 3 {
		t.Errorf("expected 3 runs (1 parent + 2 children), got %d", len(srv.runs))
	}
	if got := srv.runs[child1].Info.Status; got != "SCHEDULED" {
		t.Errorf("child1 status = %q, want SCHEDULED", got)
	}
	if got, _ := findTag(srv.runs[child1].Data.Tags, "xmc.execution_id"); got != "2" {
		t.Errorf("child1 xmc.execution_id = %q, want 2", got)
	}
}
