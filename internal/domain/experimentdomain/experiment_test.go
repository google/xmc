// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package experimentdomain

import (
	"context"
	"testing"
	"time"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestPrepareExperimentCreation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		exp     *essmsgpb.Experiment
		wantXID int64
		wantErr bool
	}{
		{
			name:    "nil_experiment",
			exp:     nil,
			wantErr: true,
		},
		{
			name: "invalid_name",
			exp: &essmsgpb.Experiment{
				Name: "invalid-experiment-name",
			},
			wantErr: true,
		},
		{
			name: "valid_experiment",
			exp: &essmsgpb.Experiment{
				Name: "experiments/12345",
			},
			wantXID: 12345,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xid, err := PrepareExperimentCreation(ctx, tt.exp, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("PrepareExperimentCreation() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if xid != tt.wantXID {
				t.Errorf("PrepareExperimentCreation() xid = %d, want %d", xid, tt.wantXID)
			}
			if tt.exp.GetCreateTime() == nil || tt.exp.GetUpdateTime() == nil {
				t.Errorf("PrepareExperimentCreation() did not set timestamps on exp: %v", tt.exp)
			}
			if tt.exp.GetStatus().GetUpdateTime() == nil || tt.exp.GetStatus().GetLastChildStatusUpdateTime() == nil {
				t.Errorf("PrepareExperimentCreation() did not set status timestamps on exp: %v", tt.exp)
			}
		})
	}
}

func TestPrepareWorkUnitCreation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name                 string
		exp                  *essmsgpb.Experiment
		wu                   *essmsgpb.WorkUnit
		wantResourcesChanged bool
		wantErr              bool
	}{
		{
			name:    "nil_experiment",
			exp:     nil,
			wu:      &essmsgpb.WorkUnit{},
			wantErr: true,
		},
		{
			name:    "nil_workunit",
			exp:     &essmsgpb.Experiment{Name: "experiments/100"},
			wu:      nil,
			wantErr: true,
		},
		{
			name: "valid_creation_no_resources",
			exp: &essmsgpb.Experiment{
				Name: "experiments/100",
			},
			wu: &essmsgpb.WorkUnit{
				Name: "experiments/100/work_units/1",
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Name: "experiments/100/work_units/1/executions/1",
					Status: &essmsgpb.WorkUnitStatus{
						State: essmsgpb.WorkUnitStatus_RUNNING,
					},
				},
			},
			wantResourcesChanged: false,
			wantErr:              false,
		},
		{
			name: "valid_creation_with_resources",
			exp: &essmsgpb.Experiment{
				Name: "experiments/100",
			},
			wu: &essmsgpb.WorkUnit{
				Name: "experiments/100/work_units/1",
				Executable: &essmsgpb.Executable{
					Type: &essmsgpb.Executable_Jobset{
						Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Replicas: 2,
									Spec: &essmsgpb.ExecutableSpec{
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{
												CpuResources: &essmsgpb.CpuResources{
													CpuMillicores: 1000,
													MemoryBytes:   1024,
												},
											},
										},
									},
								},
							},
						},
					},
				},
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Name: "experiments/100/work_units/1/executions/1",
					Status: &essmsgpb.WorkUnitStatus{
						State: essmsgpb.WorkUnitStatus_RUNNING,
					},
				},
			},
			wantResourcesChanged: true,
			wantErr:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotExp, gotChanged, err := PrepareWorkUnitCreation(ctx, tt.exp, tt.wu, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("PrepareWorkUnitCreation() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if gotChanged != tt.wantResourcesChanged {
				t.Errorf("PrepareWorkUnitCreation() resourcesChanged = %v, want %v", gotChanged, tt.wantResourcesChanged)
			}
			if gotExp.GetUpdateTime() == nil || gotExp.GetStatus().GetUpdateTime() == nil {
				t.Errorf("PrepareWorkUnitCreation() did not update timestamps: %v", gotExp)
			}
			if tt.wantResourcesChanged && len(gotExp.GetResources()) == 0 {
				t.Errorf("PrepareWorkUnitCreation() expected resources to be merged, got empty: %v", gotExp)
			}
		})
	}
}

func TestGetWorkUnitResources(t *testing.T) {
	wu := &essmsgpb.WorkUnit{
		Executable: &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
						{
							Replicas: 2,
							Spec: &essmsgpb.ExecutableSpec{
								Resources: &essmsgpb.ResourceSet{
									Resources: &essmsgpb.ResourceSet_CpuResources{
										CpuResources: &essmsgpb.CpuResources{
											CpuMillicores: 500,
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

	got := GetWorkUnitResources(wu)
	want := []*essmsgpb.ResourceSet{
		{
			Resources: &essmsgpb.ResourceSet_CpuResources{
				CpuResources: &essmsgpb.CpuResources{
					CpuMillicores: 1000,
					MemoryBytes:   4096,
				},
			},
		},
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetWorkUnitResources() mismatch (-want +got):\n%s", diff)
	}
}
