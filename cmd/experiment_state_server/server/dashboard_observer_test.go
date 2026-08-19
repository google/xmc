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

package server

import (
	"context"
	"errors"
	"sync"
	"testing"

	"xmanager_cloud/cmd/experiment_state_server/toollibs"
	dashboard_apipb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/storage"

	"golang/protobuf/v2/proto/proto"
)

type recordingStorage struct {
	*storage.MockStorage
	mu            sync.Mutex
	updateUpdates []*storage.ArtifactUpdate
}

func (s *recordingStorage) UpdateArtifact(ctx context.Context, update *storage.ArtifactUpdate) (storage.UpdatedArtifacts, error) {
	s.mu.Lock()
	s.updateUpdates = append(s.updateUpdates, update)
	s.mu.Unlock()
	return s.MockStorage.UpdateArtifact(ctx, update)
}

func TestNewDashboardObserver(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	obs := NewDashboardObserver(toolClient, &storage.MockStorage{}, &wg)
	if obs == nil {
		t.Fatal("NewDashboardObserver() returned nil")
	}
}

func TestDashboardObserver_OnArtifactCreated_NonChartArtifact(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	recStorage := &recordingStorage{MockStorage: &storage.MockStorage{}}
	obs := NewDashboardObserver(toolClient, recStorage, &wg)

	artifact := &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Url{
				Url: &essmsgpb.UrlArtifact{Url: "http://example.com"},
			},
		},
	}

	obs.OnArtifactCreated(t.Context(), artifact)
	wg.Wait()

	if len(mockDash.reqs) != 0 {
		t.Errorf("len(mockDash.reqs) = %d, want 0", len(mockDash.reqs))
	}
	if len(recStorage.updateUpdates) != 0 {
		t.Errorf("len(recStorage.updateUpdates) = %d, want 0", len(recStorage.updateUpdates))
	}
}

func TestDashboardObserver_OnArtifactCreated_Success(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{
		createChartResp: &dashboard_apipb.Chart{
			Title: "Custom Title",
		},
	}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	recStorage := &recordingStorage{MockStorage: &storage.MockStorage{}}
	obs := NewDashboardObserver(toolClient, recStorage, &wg)

	artifact := &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Chart{
				Chart: &essmsgpb.ChartArtifact{
					Title:       "Custom Title",
					MetricNames: []string{"loss", "accuracy"},
				},
			},
		},
	}

	obs.OnArtifactCreated(t.Context(), proto.Clone(artifact).(*essmsgpb.Artifact))
	wg.Wait()

	if len(mockDash.reqs) != 1 {
		t.Fatalf("len(mockDash.reqs) = %d, want 1", len(mockDash.reqs))
	}
	req := mockDash.reqs[0]
	if req.GetParent() != "experiments/100/workUnits/200" {
		t.Errorf("req.Parent = %q, want 'experiments/100/workUnits/200'", req.GetParent())
	}
	if req.GetChart().GetTitle() != "Custom Title" {
		t.Errorf("req.Chart.Title = %q, want 'Custom Title'", req.GetChart().GetTitle())
	}
	if req.GetChart().GetXid() != 100 || req.GetChart().GetWid() != 200 {
		t.Errorf("req.Chart xid/wid = (%d, %d), want (100, 200)", req.GetChart().GetXid(), req.GetChart().GetWid())
	}

	if len(recStorage.updateUpdates) != 1 {
		t.Fatalf("len(recStorage.updateUpdates) = %d, want 1", len(recStorage.updateUpdates))
	}
	up := recStorage.updateUpdates[0]
	if up.AID != 300 {
		t.Errorf("up.AID = %d, want 300", up.AID)
	}
	if up.Artifact.GetPayload().GetChart().GetStatus() != essmsgpb.ChartArtifact_SUCCESS {
		t.Errorf("Chart status = %v, want SUCCESS", up.Artifact.GetPayload().GetChart().GetStatus())
	}
	if up.UpdateMask == nil || len(up.UpdateMask.Paths) != 1 || up.UpdateMask.Paths[0] != "payload" {
		t.Errorf("up.UpdateMask = %v, want ['payload']", up.UpdateMask)
	}
}

func TestDashboardObserver_OnArtifactCreated_DefaultTitle(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	recStorage := &recordingStorage{MockStorage: &storage.MockStorage{}}
	obs := NewDashboardObserver(toolClient, recStorage, &wg)

	artifact := &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Chart{
				Chart: &essmsgpb.ChartArtifact{
					Title: "",
				},
			},
		},
	}

	obs.OnArtifactCreated(t.Context(), proto.Clone(artifact).(*essmsgpb.Artifact))
	wg.Wait()

	if len(mockDash.reqs) != 1 {
		t.Fatalf("len(mockDash.reqs) = %d, want 1", len(mockDash.reqs))
	}
	wantTitle := "Chart for XID 100 WID 200"
	if mockDash.reqs[0].GetChart().GetTitle() != wantTitle {
		t.Errorf("req.Chart.Title = %q, want %q", mockDash.reqs[0].GetChart().GetTitle(), wantTitle)
	}
}

func TestDashboardObserver_OnArtifactCreated_ClientError(t *testing.T) {
	t.Parallel()
	clientErr := errors.New("dashboard service down")
	mockDash := &mockDashboardClient{
		createChartErr: clientErr,
	}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	recStorage := &recordingStorage{MockStorage: &storage.MockStorage{}}
	obs := NewDashboardObserver(toolClient, recStorage, &wg)

	artifact := &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Chart{
				Chart: &essmsgpb.ChartArtifact{
					Title: "Failing Chart",
				},
			},
		},
	}

	obs.OnArtifactCreated(t.Context(), proto.Clone(artifact).(*essmsgpb.Artifact))
	wg.Wait()

	if len(recStorage.updateUpdates) != 1 {
		t.Fatalf("len(recStorage.updateUpdates) = %d, want 1", len(recStorage.updateUpdates))
	}
	up := recStorage.updateUpdates[0]
	chart := up.Artifact.GetPayload().GetChart()
	if chart.GetStatus() != essmsgpb.ChartArtifact_FAILED {
		t.Errorf("Chart status = %v, want FAILED", chart.GetStatus())
	}
	if chart.GetErrorMessage() != clientErr.Error() {
		t.Errorf("Chart error message = %q, want %q", chart.GetErrorMessage(), clientErr.Error())
	}
}

func TestDashboardObserver_OnArtifactCreated_StorageUpdateError(t *testing.T) {
	t.Parallel()
	mockDash := &mockDashboardClient{}
	toolClient := toollibs.NewDashboardToolClient(mockDash)
	var wg sync.WaitGroup
	mockStorage := &storage.MockStorage{
		UpdateArtifactErr: errors.New("spanner transaction aborted"),
	}
	obs := NewDashboardObserver(toolClient, mockStorage, &wg)

	artifact := &essmsgpb.Artifact{
		Name:   "artifacts/300",
		Parent: "experiments/100/workUnits/200",
		Payload: &essmsgpb.ArtifactPayload{
			Type: &essmsgpb.ArtifactPayload_Chart{
				Chart: &essmsgpb.ChartArtifact{},
			},
		},
	}

	// Should not panic or block
	obs.OnArtifactCreated(t.Context(), proto.Clone(artifact).(*essmsgpb.Artifact))
	wg.Wait()
}
