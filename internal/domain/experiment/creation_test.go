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

package experiment

import (
	"context"
	"testing"
	"time"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestPrepareWorkUnitCreation_NilValidation(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	exp := &essmsgpb.Experiment{}
	wu := &essmsgpb.WorkUnit{}

	if _, err := PrepareWorkUnitCreation(ctx, nil, wu, now); err == nil {
		t.Errorf("PrepareWorkUnitCreation(nil exp) succeeded, want error")
	}
	if _, err := PrepareWorkUnitCreation(ctx, exp, nil, now); err == nil {
		t.Errorf("PrepareWorkUnitCreation(nil wu) succeeded, want error")
	}
}

func TestPrepareWorkUnitCreation_Success(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	exp := &essmsgpb.Experiment{
		Name: "experiments/1001",
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
		},
	}

	wu := &essmsgpb.WorkUnit{
		Name: "experiments/1001/workUnits/1",
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: "experiments/1001/workUnits/1/executions/1",
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RUNNING,
			},
		},
		Executable: &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
						{
							Name:     "job1",
							Replicas: 1,
							Spec: &essmsgpb.ExecutableSpec{
								Resources: &essmsgpb.ResourceSet{
									Resources: &essmsgpb.ResourceSet_CpuResources{
										CpuResources: &essmsgpb.CpuResources{
											CpuMillicores: 1000,
											MemoryBytes:   2048,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	updatedExp, err := PrepareWorkUnitCreation(ctx, exp, wu, now)
	if err != nil {
		t.Fatalf("PrepareWorkUnitCreation failed: %v", err)
	}

	if updatedExp == nil {
		t.Fatalf("PrepareWorkUnitCreation returned nil experiment")
	}

	if len(updatedExp.Resources) != 1 {
		t.Errorf("Updated experiment resources count = %d, want 1", len(updatedExp.Resources))
	}

	wantCpu := int64(1000)
	gotCpu := updatedExp.Resources[0].GetCpuResources().GetCpuMillicores()
	if gotCpu != wantCpu {
		t.Errorf("CpuMillicores = %d, want %d", gotCpu, wantCpu)
	}
}
