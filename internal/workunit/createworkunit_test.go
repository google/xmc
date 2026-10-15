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
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/workunit"
)

func TestCreateWorkUnit(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	spanner.CreateBlankExperiment(ctx, t, storageClient, 123)

	wu := spanner.BlankWorkUnitProto(123, 1)
	wu.CurrentWorkUnitExecution.Status.SystemMessage = &essmsgpb.StructuredMessage{
		UserFacingText: "test message",
		Severity:       essmsgpb.StructuredMessage_INFO,
	}

	wu.Executable.Type = &essmsgpb.Executable_Jobset{
		Jobset: &essmsgpb.JobSetExecutable{},
	}
	jobsets := wu.Executable.GetJobset()
	jobsets.Jobs = append(jobsets.Jobs, &essmsgpb.KubernetesJob{
		Spec: &essmsgpb.ExecutableSpec{
			Resources: &essmsgpb.ResourceSet{
				Resources: &essmsgpb.ResourceSet_CpuResources{
					CpuResources: &essmsgpb.CpuResources{
						CpuMillicores: 1,
						MemoryBytes:   2,
					},
				},
				EphemeralStorageBytes: 3,
			},
		},
	})
	jobsets.Jobs = append(jobsets.Jobs, &essmsgpb.KubernetesJob{
		Spec: &essmsgpb.ExecutableSpec{
			Resources: &essmsgpb.ResourceSet{
				Resources: &essmsgpb.ResourceSet_CpuResources{
					CpuResources: &essmsgpb.CpuResources{
						CpuMillicores: 10,
						MemoryBytes:   20,
					},
				},
				EphemeralStorageBytes: 30,
			},
		},
	})
	createdWu, err := workunit.CreateWorkUnit(ctx, storageClient, wu)
	if err != nil {
		t.Fatalf("CreateWorkUnit failed: %v", err)
	}

	if createdWu.GetCreateTime() == nil {
		t.Errorf("CreateWorkUnit returned work unit with nil CreateTime")
	}
	if createdWu.GetUpdateTime() == nil {
		t.Errorf("CreateWorkUnit returned work unit with nil UpdateTime")
	}
	if wu.GetCreateTime() == nil || wu.GetUpdateTime() == nil {
		t.Errorf("CreateWorkUnit did not write timestamps back onto caller's work unit proto")
	}

	opts := []cmp.Option{
		protocmp.Transform(),
		protocmp.IgnoreFields(&essmsgpb.WorkUnit{}, "create_time", "update_time"),
		protocmp.IgnoreFields(&essmsgpb.WorkUnitExecution{}, "create_time", "update_time"),
		protocmp.IgnoreFields(&essmsgpb.WorkUnitStatus{}, "update_time", "state_update_time"),
		protocmp.IgnoreFields(&essmsgpb.StructuredMessage{}, "parent"),
	}
	if diff := cmp.Diff(wu, createdWu, opts...); diff != "" {
		t.Errorf("CreateWorkUnit returned diff (-want +got):\n%s", diff)
	}

	// Check experiment status.
	exp, err := storageClient.FetchExperiment(ctx, 123)
	if err != nil {
		t.Fatalf("FetchExperiment(123) failed: %v", err)
	}
	if got, want := spanner.ChildStateCountInState(t, exp, essmsgpb.WorkUnitStatus_INITIALIZING), int64(1); got != want {
		t.Errorf("Experiment state count for INITIALIZING is %d, want %d", got, want)
	}
	if got, want := spanner.MessageSeverityCountInState(t, exp, essmsgpb.StructuredMessage_INFO), int64(1); got != want {
		t.Errorf("Experiment message severity count for INFO is %d, want %d", got, want)
	}

	resources := exp.GetResources()
	if len(resources) != 1 {
		t.Fatalf("Experiment has %d resources, want 1", len(resources))
	}

	resource := resources[0]
	if resource.GetCpuResources() == nil {
		t.Errorf("Experiment has no CPU resources")
	}
	if got, want := resource.GetCpuResources().GetCpuMillicores(), int64(11); got != want {
		t.Errorf("Experiment CPU millicores is %d, want %d", got, want)
	}
	if got, want := resource.GetCpuResources().GetMemoryBytes(), int64(22); got != want {
		t.Errorf("Experiment CPU memory bytes is %d, want %d", got, want)
	}
	if got, want := resource.GetEphemeralStorageBytes(), int64(33); got != want {
		t.Errorf("Experiment ephemeral storage bytes is %d, want %d", got, want)
	}
}

func TestCreateWorkUnit_InvalidName(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	spanner.CreateBlankExperiment(ctx, t, storageClient, 123)

	wu := spanner.BlankWorkUnitProto(123, 1)
	wu.Name = "invalid-name"
	_, err = workunit.CreateWorkUnit(ctx, storageClient, wu)
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("CreateWorkUnit with invalid name returned %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
}

func TestCreateWorkUnit_AutoID(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	xid := int64(123)
	spanner.CreateBlankExperiment(ctx, t, storageClient, xid)

	tests := []struct {
		name    string
		inputID int64
		wantID  int64
	}{
		{
			name:    "First auto ID",
			inputID: 0,
			wantID:  1,
		},
		{
			name:    "Second auto ID",
			inputID: 0,
			wantID:  2,
		},
		{
			name:    "Explicit ID 10",
			inputID: 10,
			wantID:  10,
		},
		{
			name:    "Auto ID after 10",
			inputID: 0,
			wantID:  11,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wu := spanner.BlankWorkUnitProto(xid, tc.inputID)
			createdWu, err := workunit.CreateWorkUnit(ctx, storageClient, wu)
			if err != nil {
				t.Fatalf("CreateWorkUnit failed: %v", err)
			}

			wantName := fmt.Sprintf("experiments/%d/workUnits/%d", xid, tc.wantID)
			if got, want := createdWu.GetName(), wantName; got != want {
				t.Errorf("CreateWorkUnit(inputID=%d) work unit name = %q, want %q", tc.inputID, got, want)
			}
			wantExecutionName := fmt.Sprintf("experiments/%d/workUnits/%d/executions/1", xid, tc.wantID)
			if got, want := createdWu.GetCurrentWorkUnitExecution().GetName(), wantExecutionName; got != want {
				t.Errorf("CreateWorkUnit(inputID=%d) execution name = %q, want %q", tc.inputID, got, want)
			}
			wantParent := fmt.Sprintf("experiments/%d/workUnits/%d", xid, tc.wantID)
			if got, want := createdWu.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage().GetParent(), wantParent; got != want {
				t.Errorf("CreateWorkUnit(inputID=%d) status message parent = %q, want %q", tc.inputID, got, want)
			}
		})
	}
}

func TestCreateWorkUnit_TransactionRetryAndErrorPaths(t *testing.T) {
	ctx := context.Background()
	boomErr := errors.New("boom")

	tests := []struct {
		name    string
		wid     int64
		storage *storage.MockStorage
		wantErr error
	}{
		{
			name: "retry_idempotency_with_auto_id",
			wid:  0,
			storage: &storage.MockStorage{
				RunInTransactionAttempts: 2,
				NextWorkUnitID:           7,
				Experiment: &essmsgpb.Experiment{
					Name:   "experiments/123",
					Status: &essmsgpb.ExperimentStatus{},
				},
			},
		},
		{
			name: "fetch_next_work_unit_id_error",
			wid:  0,
			storage: &storage.MockStorage{
				FetchNextWorkUnitIDErr: boomErr,
			},
			wantErr: boomErr,
		},
		{
			name: "insert_work_unit_error",
			wid:  1,
			storage: &storage.MockStorage{
				InsertWorkUnitErr: boomErr,
			},
			wantErr: boomErr,
		},
		{
			name: "fetch_experiment_error",
			wid:  1,
			storage: &storage.MockStorage{
				FetchExperimentErr: boomErr,
			},
			wantErr: boomErr,
		},
		{
			name: "save_status_message_history_error",
			wid:  1,
			storage: &storage.MockStorage{
				Experiment:                  &essmsgpb.Experiment{Name: "experiments/123"},
				SaveStatusMessageHistoryErr: boomErr,
			},
			wantErr: boomErr,
		},
		{
			name: "save_work_unit_status_history_error",
			wid:  1,
			storage: &storage.MockStorage{
				Experiment:                   &essmsgpb.Experiment{Name: "experiments/123"},
				SaveWorkUnitStatusHistoryErr: boomErr,
			},
			wantErr: boomErr,
		},
		{
			name: "update_experiment_status_error",
			wid:  1,
			storage: &storage.MockStorage{
				Experiment:                &essmsgpb.Experiment{Name: "experiments/123"},
				UpdateExperimentStatusErr: boomErr,
			},
			wantErr: boomErr,
		},
		{
			name: "update_experiment_resources_error",
			wid:  1,
			storage: &storage.MockStorage{
				Experiment:                   &essmsgpb.Experiment{Name: "experiments/123"},
				UpdateExperimentResourcesErr: boomErr,
			},
			wantErr: boomErr,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wu := spanner.BlankWorkUnitProto(123, tc.wid)
			wu.Executable.Type = &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
						{
							Spec: &essmsgpb.ExecutableSpec{
								Resources: &essmsgpb.ResourceSet{
									Resources: &essmsgpb.ResourceSet_CpuResources{
										CpuResources: &essmsgpb.CpuResources{CpuMillicores: 100},
									},
								},
							},
						},
					},
				},
			}

			got, err := workunit.CreateWorkUnit(ctx, tc.storage, wu)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("CreateWorkUnit() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateWorkUnit() unexpected error = %v", err)
			}
			if got.GetName() != "experiments/123/workUnits/7" {
				t.Errorf("CreateWorkUnit() name = %q, want %q", got.GetName(), "experiments/123/workUnits/7")
			}
			if gotCount := spanner.ChildStateCountInState(t, tc.storage.UpdatedExperiment, essmsgpb.WorkUnitStatus_INITIALIZING); gotCount != 1 {
				t.Errorf("UpdatedExperiment INITIALIZING count = %d, want 1", gotCount)
			}
		})
	}
}
