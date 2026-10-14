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

	"google.golang.org/protobuf/proto"
	essmsgpb "xmanager_cloud/api/protos/experiment"
)

func TestPrepareWorkUnitCreation_NilValidation(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	exp := &essmsgpb.Experiment{}
	wu := &essmsgpb.WorkUnit{}

	if _, err := PrepareWorkUnitCreation(ctx, nil, wu, now); err == nil || err.Error() != "experiment must not be nil" {
		t.Errorf("PrepareWorkUnitCreation(nil exp) error = %v, want 'experiment must not be nil'", err)
	}
	if _, err := PrepareWorkUnitCreation(ctx, exp, nil, now); err == nil || err.Error() != "work unit, current execution, and status must not be nil" {
		t.Errorf("PrepareWorkUnitCreation(nil wu) error = %v, want 'work unit, current execution, and status must not be nil'", err)
	}
}

func TestPrepareWorkUnitCreation_Immutability(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	exp := &essmsgpb.Experiment{
		Name: "experiments/1001",
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
		},
	}
	expClone := proto.Clone(exp).(*essmsgpb.Experiment)

	wu := &essmsgpb.WorkUnit{
		Name: "experiments/1001/workUnits/1",
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: "experiments/1001/workUnits/1/executions/1",
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RUNNING,
			},
		},
	}
	wuClone := proto.Clone(wu).(*essmsgpb.WorkUnit)

	_, err := PrepareWorkUnitCreation(ctx, exp, wu, now)
	if err != nil {
		t.Fatalf("PrepareWorkUnitCreation failed: %v", err)
	}

	if !proto.Equal(exp, expClone) {
		t.Errorf("PrepareWorkUnitCreation mutated input experiment! got: %v, want: %v", exp, expClone)
	}
	if !proto.Equal(wu, wuClone) {
		t.Errorf("PrepareWorkUnitCreation mutated input work unit! got: %v, want: %v", wu, wuClone)
	}
}

func TestPrepareWorkUnitCreation_NilStatus(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	exp := &essmsgpb.Experiment{
		Name: "experiments/1001",
	}

	wu := &essmsgpb.WorkUnit{
		Name: "experiments/1001/workUnits/1",
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: "experiments/1001/workUnits/1/executions/1",
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RUNNING,
			},
		},
	}

	res, err := PrepareWorkUnitCreation(ctx, exp, wu, now)
	if err != nil {
		t.Fatalf("PrepareWorkUnitCreation failed: %v", err)
	}

	if res.Experiment.Status == nil {
		t.Errorf("updatedExp.Status is nil, expected initialized status")
	}
}

func TestPrepareWorkUnitCreation_NoResourceChange(t *testing.T) {
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
	}

	res, err := PrepareWorkUnitCreation(ctx, exp, wu, now)
	if err != nil {
		t.Fatalf("PrepareWorkUnitCreation failed: %v", err)
	}

	if res.ResourcesChanged {
		t.Errorf("resourcesChanged = true for work unit with no resources, want false")
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

	res, err := PrepareWorkUnitCreation(ctx, exp, wu, now)
	if err != nil {
		t.Fatalf("PrepareWorkUnitCreation failed: %v", err)
	}

	if !res.ResourcesChanged {
		t.Errorf("resourcesChanged = false, want true")
	}

	if res.Experiment == nil {
		t.Fatalf("PrepareWorkUnitCreation returned nil experiment")
	}

	if len(res.Experiment.Resources) != 1 {
		t.Errorf("Updated experiment resources count = %d, want 1", len(res.Experiment.Resources))
	}

	wantCpu := int64(1000)
	gotCpu := res.Experiment.Resources[0].GetCpuResources().GetCpuMillicores()
	if gotCpu != wantCpu {
		t.Errorf("CpuMillicores = %d, want %d", gotCpu, wantCpu)
	}

	if res.Experiment.UpdateTime.AsTime() != now {
		t.Errorf("UpdateTime = %v, want %v", res.Experiment.UpdateTime.AsTime(), now)
	}
	if res.Experiment.Status.UpdateTime.AsTime() != now {
		t.Errorf("Status.UpdateTime = %v, want %v", res.Experiment.Status.UpdateTime.AsTime(), now)
	}
	if res.Experiment.Status.LastChildStatusUpdateTime.AsTime() != now {
		t.Errorf("Status.LastChildStatusUpdateTime = %v, want %v", res.Experiment.Status.LastChildStatusUpdateTime.AsTime(), now)
	}
}
