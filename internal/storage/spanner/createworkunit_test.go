// Copyright 2025 Google LLC
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
package spanner

import (
	"context"
	"fmt"
	"testing"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/spanner/emulator/emulator"
)

func TestCreateWorkUnit(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	createBlankExperiment(ctx, t, storage, 123)

	wu := blankWorkUnitProto(123, 1)
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
	createdWu, err := storage.CreateWorkUnit(ctx, wu)
	if err != nil {
		t.Fatalf("CreateWorkUnit failed: %v", err)
	}

	if createdWu.GetCreateTime() == nil {
		t.Errorf("CreateWorkUnit returned work unit with nil CreateTime")
	}
	if createdWu.GetUpdateTime() == nil {
		t.Errorf("CreateWorkUnit returned work unit with nil UpdateTime")
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
	exp, err := storage.FetchExperiment(ctx, 123)
	if err != nil {
		t.Fatalf("FetchExperiment(123) failed: %v", err)
	}
	if got, want := childStateCountInState(t, exp, essmsgpb.WorkUnitStatus_INITIALIZING), int64(1); got != want {
		t.Errorf("Experiment state count for INITIALIZING is %d, want %d", got, want)
	}
	if got, want := messageSeverityCountInState(t, exp, essmsgpb.StructuredMessage_INFO), int64(1); got != want {
		t.Errorf("Experiment message severity count for INFO is %d, want %d", got, want)
	}

	resources := exp.GetResources()
	if len(resources) != 1 {
		t.Errorf("Experiment has %d resources, want 1", len(resources))
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
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	createBlankExperiment(ctx, t, storage, 123)

	wu := blankWorkUnitProto(123, 1)
	wu.Name = "invalid-name"
	_, err = storage.CreateWorkUnit(ctx, wu)
	if err == nil {
		t.Errorf("CreateWorkUnit with invalid name succeeded, want error")
	}
}

func TestCreateWorkUnit_AutoID(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	xid := int64(123)
	createBlankExperiment(ctx, t, storage, xid)

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
			wu := blankWorkUnitProto(xid, tc.inputID)
			createdWu, err := storage.CreateWorkUnit(ctx, wu)
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

func TestGetWorkUnitResources(t *testing.T) {
	tests := []struct {
		name string
		wu   *essmsgpb.WorkUnit
		want []*essmsgpb.ResourceSet
	}{
		{
			name: "no_resources",
			wu:   &essmsgpb.WorkUnit{},
			want: nil,
		},
		{
			name: "cpu_resources",
			wu: &essmsgpb.WorkUnit{
				Executable: &essmsgpb.Executable{
					Type: &essmsgpb.Executable_Jobset{
						Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Spec: &essmsgpb.ExecutableSpec{
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{
												CpuResources: &essmsgpb.CpuResources{
													CpuMillicores: 1000,
													MemoryBytes:   1024,
												},
											},
											EphemeralStorageBytes: 2048,
										},
									},
								},
							},
						},
					},
				},
			},
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: 1000,
							MemoryBytes:   1024,
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
		},
		{
			name: "gpu_resources",
			wu: &essmsgpb.WorkUnit{
				Executable: &essmsgpb.Executable{
					Type: &essmsgpb.Executable_Jobset{
						Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Spec: &essmsgpb.ExecutableSpec{
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_GpuResources{
												GpuResources: &essmsgpb.GpuResources{
													Type:  "nvidia-tesla-v100",
													Count: 2,
												},
											},
											EphemeralStorageBytes: 2048,
										},
									},
								},
							},
						},
					},
				},
			},
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-v100",
							Count: 2,
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
		},
		{
			name: "tpu_resources",
			wu: &essmsgpb.WorkUnit{
				Executable: &essmsgpb.Executable{
					Type: &essmsgpb.Executable_Jobset{
						Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Spec: &essmsgpb.ExecutableSpec{
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_TpuResources{
												TpuResources: &essmsgpb.TpuResources{
													Type:     "tpu-v4-pod",
													Topology: "2x2x2",
												},
											},
											EphemeralStorageBytes: 2048,
										},
									},
								},
							},
						},
					},
				},
			},
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_TpuResources{
						TpuResources: &essmsgpb.TpuResources{
							Type:     "tpu-v4-pod",
							Topology: "2x2x2",
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
		},
		{
			name: "multiple_resources",
			wu: &essmsgpb.WorkUnit{
				Executable: &essmsgpb.Executable{
					Type: &essmsgpb.Executable_Jobset{
						Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Spec: &essmsgpb.ExecutableSpec{
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{
												CpuResources: &essmsgpb.CpuResources{
													CpuMillicores: 1000,
													MemoryBytes:   1024,
												},
											},
											EphemeralStorageBytes: 2048,
										},
									},
								},
								{
									Spec: &essmsgpb.ExecutableSpec{
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_GpuResources{
												GpuResources: &essmsgpb.GpuResources{
													Type:  "nvidia-tesla-v100",
													Count: 2,
												},
											},
											EphemeralStorageBytes: 2048,
										},
									},
								},
							},
						},
					},
				},
			},
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: 1000,
							MemoryBytes:   1024,
						},
					},
					EphemeralStorageBytes: 2048,
				},
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-v100",
							Count: 2,
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
		},
		{
			name: "resources_with_sidecars",
			wu: &essmsgpb.WorkUnit{
				Executable: &essmsgpb.Executable{
					Type: &essmsgpb.Executable_Jobset{
						Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Spec: &essmsgpb.ExecutableSpec{
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{
												CpuResources: &essmsgpb.CpuResources{
													CpuMillicores: 1000,
													MemoryBytes:   1024,
												},
											},
											EphemeralStorageBytes: 1024,
										},
									},
									Sidecars: []*essmsgpb.ExecutableSpec{
										{
											Resources: &essmsgpb.ResourceSet{
												Resources: &essmsgpb.ResourceSet_CpuResources{
													CpuResources: &essmsgpb.CpuResources{
														CpuMillicores: 500,
														MemoryBytes:   512,
													},
												},
												EphemeralStorageBytes: 512,
											},
										},
									},
								},
							},
						},
					},
				},
			},
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: 1500,
							MemoryBytes:   1536,
						},
					},
					EphemeralStorageBytes: 1536,
				},
			},
		},
		{
			name: "work unit with init containers",
			wu: &essmsgpb.WorkUnit{
				Executable: &essmsgpb.Executable{
					Type: &essmsgpb.Executable_Jobset{
						Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Replicas: 1,
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
									InitContainers: []*essmsgpb.ExecutableSpec{
										{
											Resources: &essmsgpb.ResourceSet{
												Resources: &essmsgpb.ResourceSet_CpuResources{
													CpuResources: &essmsgpb.CpuResources{
														CpuMillicores: 200,
														MemoryBytes:   256,
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: 1000,
							MemoryBytes:   1024,
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getWorkUnitResources(tc.wu)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("getWorkUnitResources(%v) returned diff (-want +got):\n%s", tc.wu, diff)
			}
		})
	}
}

func TestMergeResourceSets(t *testing.T) {
	tests := []struct {
		name string
		a    []*essmsgpb.ResourceSet
		b    []*essmsgpb.ResourceSet
		want []*essmsgpb.ResourceSet
	}{
		{
			name: "nil_inputs",
			a:    nil,
			b:    nil,
			want: nil,
		},
		{
			name: "empty_inputs",
			a:    []*essmsgpb.ResourceSet{},
			b:    []*essmsgpb.ResourceSet{},
			want: nil,
		},
		{
			name: "one_cpu",
			a: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: 1000,
							MemoryBytes:   1024,
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
			b: nil,
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: 1000,
							MemoryBytes:   1024,
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
		},
		{
			name: "two_cpu",
			a: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: 1000,
							MemoryBytes:   1024,
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
			b: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: 500,
							MemoryBytes:   512,
						},
					},
					EphemeralStorageBytes: 1024,
				},
			},
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: 1500,
							MemoryBytes:   1536,
						},
					},
					EphemeralStorageBytes: 3072,
				},
			},
		},
		{
			name: "one_gpu",
			a: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-v100",
							Count: 2,
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
			b: nil,
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-v100",
							Count: 2,
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
		},
		{
			name: "two_gpu_same_type",
			a: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-v100",
							Count: 2,
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
			b: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-v100",
							Count: 1,
						},
					},
					EphemeralStorageBytes: 1024,
				},
			},
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-v100",
							Count: 3,
						},
					},
					EphemeralStorageBytes: 3072,
				},
			},
		},
		{
			name: "two_gpu_different_type",
			a: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-k80",
							Count: 2,
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
			b: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-v100",
							Count: 1,
						},
					},
					EphemeralStorageBytes: 1024,
				},
			},
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-k80",
							Count: 2,
						},
					},
					EphemeralStorageBytes: 2048,
				},
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-v100",
							Count: 1,
						},
					},
					EphemeralStorageBytes: 1024,
				},
			},
		},
		{
			name: "one_tpu",
			a: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_TpuResources{
						TpuResources: &essmsgpb.TpuResources{
							Type:     "tpu-v4-pod",
							Topology: "2x2x2",
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
			b: nil,
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_TpuResources{
						TpuResources: &essmsgpb.TpuResources{
							Type:     "tpu-v4-pod",
							Topology: "2x2x2",
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
		},
		{
			name: "two_tpu_different_type_and_topology",
			a: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_TpuResources{
						TpuResources: &essmsgpb.TpuResources{
							Type:     "tpu-v4-pod",
							Topology: "2x2x4",
						},
					},
					EphemeralStorageBytes: 2048,
				},
				{
					Resources: &essmsgpb.ResourceSet_TpuResources{
						TpuResources: &essmsgpb.TpuResources{
							Type:     "tpu-v4-pod",
							Topology: "2x2x2",
						},
					},
					EphemeralStorageBytes: 1024,
				},
			},
			b: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_TpuResources{
						TpuResources: &essmsgpb.TpuResources{
							Type:     "tpu-v3-pod",
							Topology: "2x2x2",
						},
					},
					EphemeralStorageBytes: 1024,
				},
			},
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_TpuResources{
						TpuResources: &essmsgpb.TpuResources{
							Type:     "tpu-v3-pod",
							Topology: "2x2x2",
						},
					},
					EphemeralStorageBytes: 1024,
				},
				{
					Resources: &essmsgpb.ResourceSet_TpuResources{
						TpuResources: &essmsgpb.TpuResources{
							Type:     "tpu-v4-pod",
							Topology: "2x2x2",
						},
					},
					EphemeralStorageBytes: 1024,
				},
				{
					Resources: &essmsgpb.ResourceSet_TpuResources{
						TpuResources: &essmsgpb.TpuResources{
							Type:     "tpu-v4-pod",
							Topology: "2x2x4",
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
		},
		{
			name: "cpu_gpu_tpu",
			a: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: 1000,
							MemoryBytes:   1024,
						},
					},
					EphemeralStorageBytes: 2048,
				},
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-v100",
							Count: 2,
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
			b: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_TpuResources{
						TpuResources: &essmsgpb.TpuResources{
							Type:     "tpu-v4-pod",
							Topology: "2x2x2",
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
			want: []*essmsgpb.ResourceSet{
				{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: 1000,
							MemoryBytes:   1024,
						},
					},
					EphemeralStorageBytes: 2048,
				},
				{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  "nvidia-tesla-v100",
							Count: 2,
						},
					},
					EphemeralStorageBytes: 2048,
				},
				{
					Resources: &essmsgpb.ResourceSet_TpuResources{
						TpuResources: &essmsgpb.TpuResources{
							Type:     "tpu-v4-pod",
							Topology: "2x2x2",
						},
					},
					EphemeralStorageBytes: 2048,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeResourceSets(tc.a, tc.b)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("mergeResourceSets(%v, %v) returned diff (-want +got):\n%s", tc.a, tc.b, diff)
			}
		})
	}
}
