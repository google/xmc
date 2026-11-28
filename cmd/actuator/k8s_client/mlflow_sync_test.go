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
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
)

type mlflowSyncFakeStorage struct {
	storage.Storage
	mu  sync.Mutex
	wus map[int64]*essmsgpb.WorkUnit
}

func (f *mlflowSyncFakeStorage) FetchWorkUnit(_ context.Context, _, wid int64) (*essmsgpb.WorkUnit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wus[wid], nil
}

func (f *mlflowSyncFakeStorage) ListWorkUnits(_ context.Context, _ string, _ int32, _, _, _ string, _ time.Duration) ([]*essmsgpb.WorkUnit, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*essmsgpb.WorkUnit
	for _, wu := range f.wus {
		out = append(out, wu)
	}
	return out, "", nil
}

type mlflowRunUpdateCall struct {
	RunID   string `json:"run_id"`
	Status  string `json:"status"`
	EndTime int64  `json:"end_time"`
}

type fakeMLflowSyncServer struct {
	mu          sync.Mutex
	server      *httptest.Server
	statuses    map[string]string
	endTimes    map[string]int64
	failNext    int
	updateCalls []mlflowRunUpdateCall
}

func newFakeMLflowSyncServer() *fakeMLflowSyncServer {
	f := &fakeMLflowSyncServer{statuses: make(map[string]string), endTimes: make(map[string]int64)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failNext > 0 {
			f.failNext--
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error_code":"TEMPORARILY_UNAVAILABLE","message":"outage"}`))
			return
		}
		var req mlflowRunUpdateCall
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.updateCalls = append(f.updateCalls, req)
		f.statuses[req.RunID] = req.Status
		f.endTimes[req.RunID] = req.EndTime
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	return f
}

func newMLflowSyncTestClient(t *testing.T, trackingURI string, httpClient *http.Client, st storage.Storage) *Client {
	t.Helper()
	comp := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "xmc.dev/v1alpha1",
		"kind":       "Component",
		"metadata":   map[string]any{"name": "mlflow", "namespace": "default"},
		"spec":       map[string]any{"enabled": true, "type": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER", "parameters": map[string]any{"trackingUri": trackingURI}},
		"status":     map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
	}}
	scheme := runtime.NewScheme()
	c := NewWithClients(k8sfake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}}), dynamicfake.NewSimpleDynamicClient(scheme, comp), ActuatorRuntimeConfig{}, nil, st)
	c.mlflowHTTP = httpClient
	return c
}

func makeSyncWU(wid, eid int64, st essmsgpb.WorkUnitStatus_WorkUnitState, runID, parentID string, completeSec int64) *essmsgpb.WorkUnit {
	return &essmsgpb.WorkUnit{
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name:         "experiments/10/workUnits/" + string(rune('0'+wid)) + "/executions/" + string(rune('0'+eid)),
			CompleteTime: timestamppb.New(time.Unix(completeSec, 0)),
			Status:       &essmsgpb.WorkUnitStatus{State: st},
			Metadata:     &essmsgpb.WorkUnitExecutionMetadata{Namespace: "default", MlflowExperimentId: "1", MlflowParentRunId: parentID, MlflowRunId: runID},
		},
	}
}

func TestHandleMLflowSyncTask_MappingsAndMixedStop(t *testing.T) {
	tests := []struct {
		name           string
		wus            map[int64]*essmsgpb.WorkUnit
		triggerWID     int64
		initialChild   string
		wantChildState string
		wantParent     string
	}{
		{
			name:           "failed overrides client FINISHED (authority rule)",
			wus:            map[int64]*essmsgpb.WorkUnit{1: makeSyncWU(1, 1, essmsgpb.WorkUnitStatus_FAILED, "c1", "p1", 200)},
			triggerWID:     1,
			initialChild:   "FINISHED",
			wantChildState: "FAILED",
			wantParent:     "FAILED",
		},
		{
			name: "all stopped closes parent KILLED",
			wus: map[int64]*essmsgpb.WorkUnit{
				1: makeSyncWU(1, 1, essmsgpb.WorkUnitStatus_STOPPED, "c1", "p1", 300),
				2: makeSyncWU(2, 1, essmsgpb.WorkUnitStatus_STOPPED, "c2", "p1", 310),
			},
			triggerWID: 2, initialChild: "RUNNING", wantChildState: "KILLED", wantParent: "KILLED",
		},
		{
			name: "mixed stop (1 completed + 2 stopped) closes parent FINISHED",
			wus: map[int64]*essmsgpb.WorkUnit{
				1: makeSyncWU(1, 1, essmsgpb.WorkUnitStatus_COMPLETED, "c1", "p1", 100),
				2: makeSyncWU(2, 1, essmsgpb.WorkUnitStatus_STOPPED, "c2", "p1", 300),
				3: makeSyncWU(3, 1, essmsgpb.WorkUnitStatus_STOPPED, "c3", "p1", 320),
			},
			triggerWID: 3, initialChild: "RUNNING", wantChildState: "KILLED", wantParent: "FINISHED",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakeMLflowSyncServer()
			defer srv.server.Close()
			c := newMLflowSyncTestClient(t, srv.server.URL, srv.server.Client(), &mlflowSyncFakeStorage{wus: tc.wus})
			res := c.HandleMLflowSyncTask(context.Background(), `{"xid":10,"wid":`+string(rune('0'+tc.triggerWID))+`,"eid":1}`, MLflowSyncOptions{InitialBackoff: time.Millisecond})
			if !res.WasRunSuccessful {
				t.Fatalf("HandleMLflowSyncTask() failed: %v", res.Error)
			}
			cid := "c" + string(rune('0'+tc.triggerWID))
			if srv.statuses[cid] != tc.wantChildState || srv.statuses["p1"] != tc.wantParent || srv.endTimes[cid] == 0 {
				t.Errorf("got child=%q parent=%q endTime=%d, want child=%q parent=%q", srv.statuses[cid], srv.statuses["p1"], srv.endTimes[cid], tc.wantChildState, tc.wantParent)
			}
		})
	}
}

func TestHandleMLflowSyncTask_OutageRecoveryAndStaleRetry(t *testing.T) {
	srv := newFakeMLflowSyncServer()
	defer srv.server.Close()
	srv.failNext = 2

	st := &mlflowSyncFakeStorage{wus: map[int64]*essmsgpb.WorkUnit{1: makeSyncWU(1, 1, essmsgpb.WorkUnitStatus_FAILED, "c1", "p1", 400)}}
	c := newMLflowSyncTestClient(t, srv.server.URL, srv.server.Client(), st)
	extended := 0
	res := c.HandleMLflowSyncTask(context.Background(), `{"xid":10,"wid":1,"eid":1}`, MLflowSyncOptions{
		ExtendClaim:     func(context.Context) error { extended++; return nil },
		InitialBackoff:  time.Millisecond,
		MaxBackoff:      2 * time.Millisecond,
		InHandlerTrials: 3,
	})
	if !res.WasRunSuccessful || extended != 2 || srv.statuses["c1"] != "FAILED" {
		t.Fatalf("expected recovery after 2 outages: res=%+v extended=%d status=%q", res, extended, srv.statuses["c1"])
	}

	st.wus[1] = makeSyncWU(1, 2, essmsgpb.WorkUnitStatus_RUNNING, "c1", "p1", 0)
	srv.statuses["c1"] = "SCHEDULED"
	before := len(srv.updateCalls)
	res = c.HandleMLflowSyncTask(context.Background(), `{"xid":10,"wid":1,"eid":1}`, MLflowSyncOptions{InitialBackoff: time.Millisecond})
	if !res.WasRunSuccessful || len(srv.updateCalls) != before || srv.statuses["c1"] != "SCHEDULED" {
		t.Fatalf("stale retry overwrote MLflow: calls=%d status=%q", len(srv.updateCalls)-before, srv.statuses["c1"])
	}
}
