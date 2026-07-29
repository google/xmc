// Copyright 2026 Google LLC
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

package workunit_test

import (
	"context"
	"testing"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	workunitservice "xmanager_cloud/internal/domain/workunit"
	storagetesting "xmanager_cloud/internal/storage/testing"
)

func TestService_CreateWorkUnit(t *testing.T) {
	ctx := context.Background()
	fakeStorage := storagetesting.NewFakeStorage()

	// Create parent experiment
	exp := &essmsgpb.Experiment{
		Name:  "experiments/100",
		Title: "Test Exp",
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
		},
	}
	if _, err := fakeStorage.CreateExperiment(ctx, exp); err != nil {
		t.Fatalf("CreateExperiment() failed: %v", err)
	}

	service, err := workunitservice.NewService(fakeStorage)
	if err != nil {
		t.Fatalf("NewService() failed: %v", err)
	}

	wu := &essmsgpb.WorkUnit{
		Name: "experiments/100/workUnits/0",
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: "experiments/100/workUnits/0/executions/1",
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RUNNING,
				SystemMessage: &essmsgpb.StructuredMessage{
					UserFacingText: "Job running",
					Severity:       essmsgpb.StructuredMessage_INFO,
				},
			},
		},
	}

	created, err := service.CreateWorkUnit(ctx, wu)
	if err != nil {
		t.Fatalf("CreateWorkUnit() failed: %v", err)
	}

	if created.GetName() != "experiments/100/workUnits/1" {
		t.Errorf("CreateWorkUnit() name = %q, want experiments/100/workUnits/1", created.GetName())
	}

	// Verify experiment status was updated via denormalization
	updatedExp, err := fakeStorage.FetchExperiment(ctx, 100)
	if err != nil {
		t.Fatalf("FetchExperiment() failed: %v", err)
	}
	if updatedExp.GetStatus() == nil {
		t.Fatalf("Updated experiment status is nil")
	}
}

func TestService_RestartWorkUnitExecutions(t *testing.T) {
	ctx := context.Background()
	fakeStorage := storagetesting.NewFakeStorage()

	// Create parent experiment
	exp := &essmsgpb.Experiment{
		Name:  "experiments/200",
		Title: "Test Exp 200",
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
		},
	}
	if _, err := fakeStorage.CreateExperiment(ctx, exp); err != nil {
		t.Fatalf("CreateExperiment() failed: %v", err)
	}

	service, err := workunitservice.NewService(fakeStorage)
	if err != nil {
		t.Fatalf("NewService() failed: %v", err)
	}

	wu := &essmsgpb.WorkUnit{
		Name: "experiments/200/workUnits/1",
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: "experiments/200/workUnits/1/executions/1",
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_FAILED,
				SystemMessage: &essmsgpb.StructuredMessage{
					UserFacingText: "Job failed",
					Severity:       essmsgpb.StructuredMessage_ERROR,
				},
			},
		},
	}
	if _, err := fakeStorage.CreateWorkUnit(ctx, wu); err != nil {
		t.Fatalf("CreateWorkUnit() in storage failed: %v", err)
	}

	restarted, err := service.RestartWorkUnitExecutions(ctx, "tester@example.com", 200, []int64{1})
	if err != nil {
		t.Fatalf("RestartWorkUnitExecutions() failed: %v", err)
	}

	newExec, exists := restarted[1]
	if !exists {
		t.Fatalf("RestartWorkUnitExecutions() missing execution for WID 1")
	}
	if newExec.GetStatus().GetState() != essmsgpb.WorkUnitStatus_RESTARTING {
		t.Errorf("New execution state = %v, want RESTARTING", newExec.GetStatus().GetState())
	}
}
