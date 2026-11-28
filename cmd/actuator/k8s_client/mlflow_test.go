// Copyright 2026 Google LLC
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

package k8sclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	esspb "xmanager_cloud/api/protos/experiment"
	constants "xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/internal/storage"
)

type fakeMLflowServer struct {
	mu          sync.Mutex
	experiments map[string]string
	runs        map[string]*fakeRun
	runExpID    map[string]string
	expSeq      int
	runSeq      int
	failAll     bool
}

type fakeRun struct {
	RunID   string
	RunName string
	Status  string
	Tags    map[string]string
}

func (f *fakeMLflowServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAll {
		http.Error(w, `{"error_code":"INTERNAL_ERROR","message":"server down"}`, http.StatusServiceUnavailable)
		return
	}

	switch r.URL.Path {
	case "/api/2.0/mlflow/experiments/get-by-name":
		name := r.URL.Query().Get("experiment_name")
		id, ok := f.experiments[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error_code": "RESOURCE_DOES_NOT_EXIST"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"experiment": map[string]string{"experiment_id": id}})
	case "/api/2.0/mlflow/experiments/create":
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.expSeq++
		id := fmt.Sprintf("exp-%d", f.expSeq)
		f.experiments[req.Name] = id
		_ = json.NewEncoder(w).Encode(map[string]string{"experiment_id": id})
	case "/api/2.0/mlflow/runs/search":
		var req struct {
			ExperimentIDs []string `json:"experiment_ids"`
			Filter        string   `json:"filter"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var matched []map[string]any
		for id, run := range f.runs {
			if len(req.ExperimentIDs) > 0 && f.runExpID[id] != req.ExperimentIDs[0] {
				continue
			}
			if matchFakeFilter(run, req.Filter) {
				var tags []map[string]string
				for k, v := range run.Tags {
					tags = append(tags, map[string]string{"key": k, "value": v})
				}
				matched = append(matched, map[string]any{
					"info": map[string]string{"run_id": run.RunID, "status": run.Status},
					"data": map[string]any{"tags": tags},
				})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"runs": matched})
	case "/api/2.0/mlflow/runs/create":
		var req struct {
			ExperimentID string              `json:"experiment_id"`
			RunName      string              `json:"run_name"`
			Tags         []map[string]string `json:"tags"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.runSeq++
		id := fmt.Sprintf("run-%d", f.runSeq)
		tags := map[string]string{"mlflow.runName": req.RunName}
		for _, t := range req.Tags {
			tags[t["key"]] = t["value"]
		}
		f.runs[id] = &fakeRun{RunID: id, RunName: req.RunName, Status: "RUNNING", Tags: tags}
		f.runExpID[id] = req.ExperimentID
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run": map[string]any{"info": map[string]string{"run_id": id, "status": "RUNNING"}},
		})
	case "/api/2.0/mlflow/runs/set-tag":
		var req struct {
			RunID string `json:"run_id"`
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if run, ok := f.runs[req.RunID]; ok {
			run.Tags[req.Key] = req.Value
		}
		_ = json.NewEncoder(w).Encode(map[string]any{})
	case "/api/2.0/mlflow/runs/update":
		var req struct {
			RunID  string `json:"run_id"`
			Status string `json:"status"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if run, ok := f.runs[req.RunID]; ok {
			run.Status = req.Status
		}
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}
}

func matchFakeFilter(run *fakeRun, filter string) bool {
	for _, part := range strings.Split(filter, " AND ") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.Trim(strings.TrimPrefix(strings.TrimSpace(kv[0]), "tags."), "`")
		val := strings.Trim(strings.TrimSpace(kv[1]), "'")
		if run.Tags[key] != val {
			return false
		}
	}
	return true
}

func newMLflowComponentObj(trackingURI string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata": map[string]any{
				"name":      "mlflow",
				"namespace": "xmc",
			},
			"spec": map[string]any{
				"enabled": true,
				"parameters": map[string]any{
					"trackingUri":    trackingURI,
					"secretName":     "mlflow-creds",
					"secretTokenKey": "token",
				},
			},
			"status": map[string]any{
				"componentGeneration": int64(1),
				"state":               "COMPONENT_STATE_ENABLED",
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True"},
				},
				"access": map[string]any{
					"envVars": map[string]any{
						"TRACKING_URI":     trackingURI,
						"UI_BASE_URL":      trackingURI,
						"SECRET_NAME":      "mlflow-creds",
						"SECRET_TOKEN_KEY": "token",
					},
				},
			},
		},
	}
}

func setupMLflowTestClient(t *testing.T, comp *unstructured.Unstructured, exp *esspb.Experiment, execs map[int64]*esspb.WorkUnitExecution, hc *http.Client) (*Client, *mlflowTestStorage) {
	t.Helper()
	scheme := runtime.NewScheme()
	objs := []runtime.Object{}
	if comp != nil {
		objs = append(objs, comp)
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		JobSetGVR:        "JobSetList",
		KueueWorkloadGVR: "WorkloadList",
		{Group: "xmc.dev", Version: "v1alpha1", Resource: "components"}: "ComponentList",
	}, objs...)
	cs := kubernetesfake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "mlflow-creds", Namespace: "xmc"},
		Data:       map[string][]byte{"token": []byte("super-secret-token-value")},
	})
	if execs == nil {
		execs = make(map[int64]*esspb.WorkUnitExecution)
	}
	store := &mlflowTestStorage{
		MockStorage: &storage.MockStorage{
			Experiment: exp,
			WorkUnit:   &esspb.WorkUnit{Name: "experiments/123/workUnits/1"},
		},
		execs: execs,
	}
	c := NewWithClients(cs, dyn, ActuatorRuntimeConfig{DefaultExecutionEnvironment: "gke"}, &alwaysPassingPreconditionsValidator{}, store)
	c.SetMLflowHTTPClient(hc)
	return c, store
}

type mlflowTestStorage struct {
	*storage.MockStorage
	mu    sync.Mutex
	execs map[int64]*esspb.WorkUnitExecution
}

func (m *mlflowTestStorage) RunInTransaction(ctx context.Context, fn func(ctx context.Context, tx *storage.Transaction) error) error {
	return fn(ctx, &storage.Transaction{
		WorkUnits:      m,
		Experiments:    m.MockStorage,
		StatusMessages: m.MockStorage,
		StatusHistory:  m.MockStorage,
	})
}

func (m *mlflowTestStorage) FetchUpdatingWorkUnits(_ context.Context, xid int64, wids []int64) (map[int64]*esspb.WorkUnit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[int64]*esspb.WorkUnit, len(wids))
	for _, wid := range wids {
		if ex, ok := m.execs[wid]; ok {
			out[wid] = &esspb.WorkUnit{
				Name:                     fmt.Sprintf("experiments/%d/workUnits/%d", xid, wid),
				CurrentWorkUnitExecution: ex,
			}
		}
	}
	return out, nil
}

func (m *mlflowTestStorage) UpdateCurrentWorkUnitExecution(_ context.Context, _ int64, wid, _ int64, exec *esspb.WorkUnitExecution) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.execs[wid] = exec
	return nil
}

func TestConstructJobSet_MLflowLifecycleAndEnvInjection(t *testing.T) {
	srv := &fakeMLflowServer{
		experiments: make(map[string]string),
		runs:        make(map[string]*fakeRun),
		runExpID:    make(map[string]string),
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	exp := &esspb.Experiment{
		Name:           "experiments/123",
		Title:          "my-sweep",
		ExperimentType: esspb.Experiment_STANDARD,
	}
	exec1 := &esspb.WorkUnitExecution{
		Name:   "experiments/123/workUnits/1/executions/1",
		Status: &esspb.WorkUnitStatus{State: esspb.WorkUnitStatus_SCHEDULING},
		Metadata: &esspb.WorkUnitExecutionMetadata{
			ResourceName: "xmc-operation-123-1",
			Namespace:    "team-a",
		},
	}
	exec2 := &esspb.WorkUnitExecution{
		Name:   "experiments/123/workUnits/2/executions/1",
		Status: &esspb.WorkUnitStatus{State: esspb.WorkUnitStatus_SCHEDULING},
		Metadata: &esspb.WorkUnitExecutionMetadata{
			ResourceName: "xmc-operation-123-2",
			Namespace:    "team-a",
		},
	}

	client, store := setupMLflowTestClient(t, newMLflowComponentObj(ts.URL), exp, map[int64]*esspb.WorkUnitExecution{1: exec1, 2: exec2}, ts.Client())
	ctx := context.Background()

	wu1 := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Executable.GetJobset().Annotations = map[string]string{
			constants.ComponentSnapshotsAnnotation: `{"mlflow": 1}`,
		}
		wu.Executable.GetJobset().Jobs[0].Spec.EnvVars = map[string]string{
			"MLFLOW_TRACKING_URI": "http://user-override:5000",
		}
		wu.Executable.GetJobset().Jobs[0].Sidecars = []*esspb.ExecutableSpec{
			{DockerImage: "busybox:latest"},
		}
	})

	js1, err := client.constructJobSet(ctx, wu1, exec1.GetName(), "author", "team-a", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet(wu1) error: %v", err)
	}

	// Parallel second work unit reuses experiment and parent run.
	js2, err := client.constructJobSet(ctx, baseCPUWorkUnit, exec2.GetName(), "author", "team-a", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet(wu2) error: %v", err)
	}

	// Verify 1 parent + 2 child runs created.
	if len(srv.runs) != 3 {
		t.Fatalf("got %d MLflow runs, want 3 (1 parent + 2 children)", len(srv.runs))
	}
	if js1.Annotations[constants.MLflowParentRunIDAnnotation] != js2.Annotations[constants.MLflowParentRunIDAnnotation] {
		t.Errorf("parent run IDs differ: %q vs %q", js1.Annotations[constants.MLflowParentRunIDAnnotation], js2.Annotations[constants.MLflowParentRunIDAnnotation])
	}
	if js1.Annotations[constants.MLflowRunIDAnnotation] == js2.Annotations[constants.MLflowRunIDAnnotation] {
		t.Errorf("child run IDs unexpectedly identical: %q", js1.Annotations[constants.MLflowRunIDAnnotation])
	}

	// Verify user-defined MLFLOW_TRACKING_URI was preserved and slice 1 raw env vars were not leaked.
	primaryEnv := envMap(js1.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers[0].Env)
	if primaryEnv["MLFLOW_TRACKING_URI"].Value != "http://user-override:5000" {
		t.Errorf("MLFLOW_TRACKING_URI = %q, want user override", primaryEnv["MLFLOW_TRACKING_URI"].Value)
	}
	if primaryEnv["MLFLOW_EXPERIMENT_ID"].Value != "exp-1" {
		t.Errorf("MLFLOW_EXPERIMENT_ID = %q, want exp-1", primaryEnv["MLFLOW_EXPERIMENT_ID"].Value)
	}
	if primaryEnv["MLFLOW_TRACKING_TOKEN"].ValueFrom.SecretKeyRef.Name != "mlflow-creds" {
		t.Errorf("MLFLOW_TRACKING_TOKEN SecretKeyRef missing")
	}
	for _, forbidden := range []string{"TRACKING_URI", "UI_BASE_URL", "SECRET_NAME", "SECRET_TOKEN_KEY"} {
		if _, leaked := primaryEnv[forbidden]; leaked {
			t.Errorf("forbidden slice-1 env var %q leaked into container", forbidden)
		}
	}

	// Verify sidecar (initContainer) also received MLFLOW_* vars.
	sidecarEnv := envMap(js1.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.InitContainers[0].Env)
	if sidecarEnv["MLFLOW_RUN_ID"].Value == "" {
		t.Errorf("sidecar container missing MLFLOW_RUN_ID")
	}

	// Verify no credential value appears anywhere in the serialized JobSet.
	rawJS, _ := json.Marshal(js1)
	if strings.Contains(string(rawJS), "super-secret-token-value") {
		t.Errorf("credential value leaked into JobSet manifest")
	}

	// Verify metadata merge preserved ResourceName and Namespace.
	gotExec := store.execs[1]
	if gotExec.GetMetadata().GetResourceName() != "xmc-operation-123-1" || gotExec.GetMetadata().GetMlflowRunId() == "" {
		t.Errorf("WorkUnitExecution metadata not merged cleanly: %+v", gotExec.GetMetadata())
	}
}

func TestConstructJobSet_MLflowFailureNonBlockingWithWarning(t *testing.T) {
	srv := &fakeMLflowServer{failAll: true}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	exp := &esspb.Experiment{Name: "experiments/123", Title: "", ExperimentType: esspb.Experiment_STANDARD}
	exec := &esspb.WorkUnitExecution{
		Name:   "experiments/123/workUnits/1/executions/1",
		Status: &esspb.WorkUnitStatus{State: esspb.WorkUnitStatus_SCHEDULING},
	}
	client, store := setupMLflowTestClient(t, newMLflowComponentObj(ts.URL), exp, map[int64]*esspb.WorkUnitExecution{1: exec}, ts.Client())

	js, err := client.constructJobSet(context.Background(), baseCPUWorkUnit, exec.GetName(), "author", "team-a", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() failed on MLflow error: %v", err)
	}

	env := envMap(js.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers[0].Env)
	if env["MLFLOW_TRACKING_URI"].Value != ts.URL {
		t.Errorf("MLFLOW_TRACKING_URI = %q, want %q", env["MLFLOW_TRACKING_URI"].Value, ts.URL)
	}
	if _, hasRunID := env["MLFLOW_RUN_ID"]; hasRunID {
		t.Errorf("MLFLOW_RUN_ID should be omitted when MLflow run creation failed")
	}
	gotMsg := store.execs[1].GetStatus().GetSystemMessage()
	if gotMsg == nil || gotMsg.GetSeverity() != esspb.StructuredMessage_WARNING {
		t.Errorf("expected WARNING system message, got %+v", gotMsg)
	}
}

func TestConstructJobSet_GoldenWithoutMLflowComponent(t *testing.T) {
	ctx, baseClient, _ := setupTestClient(true, &alwaysPassingPreconditionsValidator{})
	want, err := baseClient.constructJobSet(ctx, baseCPUWorkUnit, "experiments/123/workUnits/456/executions/1", "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("baseline constructJobSet() error: %v", err)
	}

	mlflowClient, _ := setupMLflowTestClient(t, nil, nil, nil, nil)
	mlflowClient.RuntimeConfig.CreateNewNamespaces = true
	got, err := mlflowClient.constructJobSet(ctx, baseCPUWorkUnit, "experiments/123/workUnits/456/executions/1", "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("mlflowClient constructJobSet() error: %v", err)
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("JobSet changed without mlflow component (-want +got):\n%s", diff)
	}
}

func envMap(envs []corev1.EnvVar) map[string]corev1.EnvVar {
	m := make(map[string]corev1.EnvVar, len(envs))
	for _, e := range envs {
		m[e.Name] = e
	}
	return m
}
