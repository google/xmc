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

package statushistory

import (
	"testing"
	"time"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestExtractAllResourcesFromExecutable(t *testing.T) {
	exec := &essmsgpb.Executable{
		Type: &essmsgpb.Executable_Jobset{
			Jobset: &essmsgpb.JobSetExecutable{
				Jobs: []*essmsgpb.KubernetesJob{
					{
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_GpuResources{
									GpuResources: &essmsgpb.GpuResources{
										Type:  "nvidia-tesla-h100",
										Count: 8,
									},
								},
							},
						},
					},
					{
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_CpuResources{
									CpuResources: &essmsgpb.CpuResources{
										CpuMillicores: 16000,
									},
								},
							},
						},
					},
					{
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_TpuResources{
									TpuResources: &essmsgpb.TpuResources{
										Type:     "tpu-v5p",
										Topology: "2x2x4",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	got := ExtractAllResourcesFromExecutable(exec)
	if got["nvidia-tesla-h100"] != 8 {
		t.Errorf("nvidia-tesla-h100 = %d, want 8", got["nvidia-tesla-h100"])
	}
	if got["CPU"] != 16 {
		t.Errorf("CPU = %d, want 16", got["CPU"])
	}
	if got["tpu-v5p"] != 16 {
		t.Errorf("tpu-v5p = %d, want 16 (2x2x4 topology)", got["tpu-v5p"])
	}
}

func TestExtractAllResourcesFromExecutable_NoFallbacks(t *testing.T) {
	// Nil executable should return empty map, NOT default CPU: 1
	gotNil := ExtractAllResourcesFromExecutable(nil)
	if len(gotNil) != 0 {
		t.Errorf("ExtractAllResourcesFromExecutable(nil) = %+v, want empty map", gotNil)
	}

	// Executable with empty GPU type should not add GPU H100 default
	execEmptyGPU := &essmsgpb.Executable{
		Type: &essmsgpb.Executable_Jobset{
			Jobset: &essmsgpb.JobSetExecutable{
				Jobs: []*essmsgpb.KubernetesJob{
					{
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_GpuResources{
									GpuResources: &essmsgpb.GpuResources{
										Type:  "",
										Count: 4,
									},
								},
							},
						},
					},
				},
			},
		},
	}
	gotEmptyGPU := ExtractAllResourcesFromExecutable(execEmptyGPU)
	if len(gotEmptyGPU) != 0 {
		t.Errorf("ExtractAllResourcesFromExecutable with empty GPU type = %+v, want empty map", gotEmptyGPU)
	}

	// Executable with empty TPU type should not add TPU default
	execEmptyTPU := &essmsgpb.Executable{
		Type: &essmsgpb.Executable_Jobset{
			Jobset: &essmsgpb.JobSetExecutable{
				Jobs: []*essmsgpb.KubernetesJob{
					{
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_TpuResources{
									TpuResources: &essmsgpb.TpuResources{
										Type:     "",
										Topology: "2x2",
									},
								},
							},
						},
					},
				},
			},
		},
	}
	gotEmptyTPU := ExtractAllResourcesFromExecutable(execEmptyTPU)
	if len(gotEmptyTPU) != 0 {
		t.Errorf("ExtractAllResourcesFromExecutable with empty TPU type = %+v, want empty map", gotEmptyTPU)
	}

	// Executable with invalid TPU topology should not assume 1 chip
	execInvalidTPU := &essmsgpb.Executable{
		Type: &essmsgpb.Executable_Jobset{
			Jobset: &essmsgpb.JobSetExecutable{
				Jobs: []*essmsgpb.KubernetesJob{
					{
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_TpuResources{
									TpuResources: &essmsgpb.TpuResources{
										Type:     "tpu-v4",
										Topology: "invalid",
									},
								},
							},
						},
					},
				},
			},
		},
	}
	gotInvalidTPU := ExtractAllResourcesFromExecutable(execInvalidTPU)
	if len(gotInvalidTPU) != 0 {
		t.Errorf("ExtractAllResourcesFromExecutable with invalid TPU topology = %+v, want empty map", gotInvalidTPU)
	}
}

func TestQueueVelocityResult(t *testing.T) {
	res := QueueVelocityResult{
		AdmittedJobsCount: 12,
		ResourceUnits:     96,
		ResourceType:      "GPU H100",
	}
	window := 1 * time.Hour
	rate := float64(res.AdmittedJobsCount) / window.Hours()

	if rate != 12.0 {
		t.Errorf("rate = %f, want 12.0", rate)
	}
}

func TestFetchBatchQueueMetrics_NilClient(t *testing.T) {
	repo := NewRepository(nil)
	batchVel, err := repo.FetchBatchQueueVelocity(nil, []string{"cq-gpu"}, 1*time.Hour)
	if err != nil {
		t.Fatalf("expected no error for nil client batch velocity, got %v", err)
	}
	if len(batchVel) != 0 {
		t.Errorf("expected empty batch map for nil client, got %+v", batchVel)
	}

	batchPending, err := repo.FetchBatchPendingQueueMetrics(nil, []string{"cq-gpu"})
	if err != nil {
		t.Fatalf("expected no error for nil client batch pending, got %v", err)
	}
	if len(batchPending) != 0 {
		t.Errorf("expected empty batch map for nil client, got %+v", batchPending)
	}
}

func TestExtractAllResourcesFromExecutable_BothCpuAndGpu(t *testing.T) {
	exec := &essmsgpb.Executable{
		Type: &essmsgpb.Executable_Jobset{
			Jobset: &essmsgpb.JobSetExecutable{
				Jobs: []*essmsgpb.KubernetesJob{
					{
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_CpuResources{
									CpuResources: &essmsgpb.CpuResources{
										CpuMillicores: 16000,
									},
								},
							},
						},
					},
					{
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_GpuResources{
									GpuResources: &essmsgpb.GpuResources{
										Type:  "nvidia-tesla-h100",
										Count: 8,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	got := ExtractAllResourcesFromExecutable(exec)
	if got["CPU"] != 16 {
		t.Errorf("CPU = %d, want 16", got["CPU"])
	}
	if got["nvidia-tesla-h100"] != 8 {
		t.Errorf("nvidia-tesla-h100 = %d, want 8", got["nvidia-tesla-h100"])
	}
}
