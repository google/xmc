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

package artifactlifecycle_test

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/cmd/experiment_state_server/artifactlifecycle"
	"xmanager_cloud/internal/hypercomputeclient"
	"xmanager_cloud/internal/storage"
)

type fakeMLRunClient struct {
	createdRuns map[string]string
	updatedRuns map[string]string
}

func newFakeMLRunClient() *fakeMLRunClient {
	return &fakeMLRunClient{
		createdRuns: make(map[string]string),
		updatedRuns: make(map[string]string),
	}
}

func (f *fakeMLRunClient) CreateMLRun(_ context.Context, name, _, runPhase string, _ *hypercomputeclient.CreateMLRunOptions) (map[string]any, error) {
	f.createdRuns[name] = runPhase
	return map[string]any{
		"metadata": map[string]any{
			"url": "https://console.cloud.google.com/cluster-director/locations/us-central1/runs/" + name,
		},
	}, nil
}

func (f *fakeMLRunClient) GetMLRun(_ context.Context, name string) (map[string]any, error) {
	return map[string]any{
		"displayName": name,
		"labels": map[string]any{
			"created_by": "xmanager",
		},
	}, nil
}

func (f *fakeMLRunClient) UpdateMLRun(_ context.Context, name string, opts *hypercomputeclient.UpdateMLRunOptions) (map[string]any, error) {
	if opts.RunPhase != "" {
		f.updatedRuns[name] = opts.RunPhase
	}
	return map[string]any{
		"metadata": map[string]any{
			"url": "https://console.cloud.google.com/cluster-director/locations/us-central1/runs/" + name,
		},
	}, nil
}

func TestManagedXprofRule(t *testing.T) {
	ctx := context.Background()

	t.Run("Matches", func(t *testing.T) {
		rule := artifactlifecycle.NewManagedXprofRule(nil, "proj", "us-central1")
		if !rule.Matches(&essmsgpb.Artifact{
			Payload: &essmsgpb.ArtifactPayload{
				Type: &essmsgpb.ArtifactPayload_ManagedXprof{ManagedXprof: &essmsgpb.ManagedXprofArtifact{}},
			},
		}) {
			t.Errorf("Matches() = false for ManagedXprof artifact")
		}
		if rule.Matches(&essmsgpb.Artifact{
			Payload: &essmsgpb.ArtifactPayload{
				Type: &essmsgpb.ArtifactPayload_Text{Text: &essmsgpb.TextArtifact{}},
			},
		}) {
			t.Errorf("Matches() = true for Text artifact")
		}
	})

	t.Run("Active state creates MLRun and persists URL on matching EID", func(t *testing.T) {
		mlClient := newFakeMLRunClient()
		rule := artifactlifecycle.NewManagedXprofRule(mlClient, "test-proj", "us-central1")

		mockStore := &storage.MockStorage{
			WorkUnit: &essmsgpb.WorkUnit{
				Name: "experiments/10/workUnits/20",
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Name: "experiments/10/workUnits/20/executions/1",
				},
			},
			Artifact: &essmsgpb.Artifact{
				Name: "experiments/10/workUnits/20/artifacts/30",
				Payload: &essmsgpb.ArtifactPayload{
					Type: &essmsgpb.ArtifactPayload_ManagedXprof{
						ManagedXprof: &essmsgpb.ManagedXprofArtifact{},
					},
				},
			},
		}

		event := &taskqueuepb.ArtifactLifecycleEvent{
			ExperimentId:        proto.Int64(10),
			WorkUnitId:          proto.Int64(20),
			WorkUnitExecutionId: proto.Int64(1),
			NewState:            essmsgpb.WorkUnitStatus_RUNNING.Enum(),
			Payload: &taskqueuepb.ArtifactLifecycleEvent_JobSetRuntimeContext{
				JobSetRuntimeContext: &taskqueuepb.JobSetRuntimeContext{
					Cluster:    proto.String("cluster-1"),
					Namespace:  proto.String("default"),
					JobSetName: proto.String("xmc-wu-20-1"),
				},
			},
		}

		art := &essmsgpb.Artifact{
			Name: "experiments/10/workUnits/20/artifacts/30",
			Payload: &essmsgpb.ArtifactPayload{
				Type: &essmsgpb.ArtifactPayload_ManagedXprof{
					ManagedXprof: &essmsgpb.ManagedXprofArtifact{},
				},
			},
		}

		err := rule.OnExecutionStateChanged(ctx, mockStore, event, art)
		if err != nil {
			t.Fatalf("OnExecutionStateChanged failed: %v", err)
		}

		if len(mlClient.createdRuns) != 1 {
			t.Errorf("createdRuns count = %d, want 1", len(mlClient.createdRuns))
		}
	})

	t.Run("Active state skips persisting URL when execution is superseded", func(t *testing.T) {
		mlClient := newFakeMLRunClient()
		rule := artifactlifecycle.NewManagedXprofRule(mlClient, "test-proj", "us-central1")

		mockStore := &storage.MockStorage{
			WorkUnit: &essmsgpb.WorkUnit{
				Name: "experiments/10/workUnits/20",
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Name: "experiments/10/workUnits/20/executions/2", // Already advanced to 2
				},
			},
			Artifact: &essmsgpb.Artifact{
				Name: "experiments/10/workUnits/20/artifacts/30",
				Payload: &essmsgpb.ArtifactPayload{
					Type: &essmsgpb.ArtifactPayload_ManagedXprof{
						ManagedXprof: &essmsgpb.ManagedXprofArtifact{},
					},
				},
			},
		}

		event := &taskqueuepb.ArtifactLifecycleEvent{
			ExperimentId:        proto.Int64(10),
			WorkUnitId:          proto.Int64(20),
			WorkUnitExecutionId: proto.Int64(1), // Delayed event from old EID 1
			NewState:            essmsgpb.WorkUnitStatus_RUNNING.Enum(),
		}

		art := &essmsgpb.Artifact{
			Name: "experiments/10/workUnits/20/artifacts/30",
			Payload: &essmsgpb.ArtifactPayload{
				Type: &essmsgpb.ArtifactPayload_ManagedXprof{
					ManagedXprof: &essmsgpb.ManagedXprofArtifact{},
				},
			},
		}

		err := rule.OnExecutionStateChanged(ctx, mockStore, event, art)
		if err != nil {
			t.Fatalf("OnExecutionStateChanged failed: %v", err)
		}

		// UpdateArtifact should NOT have been called because curEID (2) != eventEID (1)
		if mockStore.UpdateArtifactResp != nil {
			t.Errorf("UpdateArtifact was called unexpectedly for superseded execution")
		}
	})

	t.Run("Terminal state transitions MLRun to COMPLETED", func(t *testing.T) {
		mlClient := newFakeMLRunClient()
		rule := artifactlifecycle.NewManagedXprofRule(mlClient, "test-proj", "us-central1")

		mockStore := &storage.MockStorage{
			WorkUnit: &essmsgpb.WorkUnit{
				Name: "experiments/10/workUnits/20",
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Name: "experiments/10/workUnits/20/executions/1",
				},
			},
			Artifact: &essmsgpb.Artifact{
				Name: "experiments/10/workUnits/20/artifacts/30",
				Payload: &essmsgpb.ArtifactPayload{
					Type: &essmsgpb.ArtifactPayload_ManagedXprof{
						ManagedXprof: &essmsgpb.ManagedXprofArtifact{},
					},
				},
			},
		}

		event := &taskqueuepb.ArtifactLifecycleEvent{
			ExperimentId:        proto.Int64(10),
			WorkUnitId:          proto.Int64(20),
			WorkUnitExecutionId: proto.Int64(1),
			NewState:            essmsgpb.WorkUnitStatus_COMPLETED.Enum(),
		}

		art := &essmsgpb.Artifact{
			Name: "experiments/10/workUnits/20/artifacts/30",
			Payload: &essmsgpb.ArtifactPayload{
				Type: &essmsgpb.ArtifactPayload_ManagedXprof{
					ManagedXprof: &essmsgpb.ManagedXprofArtifact{},
				},
			},
		}

		err := rule.OnExecutionStateChanged(ctx, mockStore, event, art)
		if err != nil {
			t.Fatalf("OnExecutionStateChanged failed: %v", err)
		}

		foundCompleted := false
		for _, phase := range mlClient.updatedRuns {
			if phase == "COMPLETED" {
				foundCompleted = true
				break
			}
		}
		if !foundCompleted {
			t.Errorf("updatedRuns missing COMPLETED phase: %v", mlClient.updatedRuns)
		}
	})

	t.Run("OnExecutionSuperseded finalizes old EID MLRun", func(t *testing.T) {
		mlClient := newFakeMLRunClient()
		rule := artifactlifecycle.NewManagedXprofRule(mlClient, "test-proj", "us-central1")

		mockStore := &storage.MockStorage{}
		art := &essmsgpb.Artifact{
			Name: "experiments/10/workUnits/20/artifacts/30",
		}

		err := rule.OnExecutionSuperseded(ctx, mockStore, 10, 20, 1, art)
		if err != nil {
			t.Fatalf("OnExecutionSuperseded failed: %v", err)
		}

		foundCompleted := false
		for _, phase := range mlClient.updatedRuns {
			if phase == "COMPLETED" {
				foundCompleted = true
				break
			}
		}
		if !foundCompleted {
			t.Errorf("updatedRuns missing COMPLETED phase for superseded execution: %v", mlClient.updatedRuns)
		}
	})
}
