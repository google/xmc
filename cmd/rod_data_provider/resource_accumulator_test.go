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

package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"

	spb "xmanager_cloud/api/protos/rod"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/common/tputopology"
)

func TestExtractRequestedResources(t *testing.T) {
	tests := []struct {
		name string
		wu   *essmsgpb.WorkUnit
		want []*spb.Resource
	}{
		{
			name: "nil workunit",
			wu:   nil,
			want: nil,
		},
		{
			name: "empty workunit",
			wu:   &essmsgpb.WorkUnit{},
			want: nil,
		},
		{
			name: "cpu and memory aggregation",
			wu: buildWorkUnitWithJobSet([]*essmsgpb.KubernetesJob{
				{
					Replicas: 2,
					Spec: &essmsgpb.ExecutableSpec{
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_CpuResources{
								CpuResources: &essmsgpb.CpuResources{
									CpuMillicores: 4000,
									MemoryBytes:   8192,
								},
							},
						},
					},
				},
			}),
			want: []*spb.Resource{
				{ResourceType: "cpu", Count: 8.0},
				{ResourceType: "memory", Count: 16384.0},
			},
		},
		{
			name: "explicit GPU type and count",
			wu: buildWorkUnitWithJobSet([]*essmsgpb.KubernetesJob{
				{
					Replicas: 2,
					Spec: &essmsgpb.ExecutableSpec{
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_GpuResources{
								GpuResources: &essmsgpb.GpuResources{
									Type:  "nvidia-tesla-h100",
									Count: 4,
								},
							},
						},
					},
				},
				{
					Replicas: 1,
					Spec: &essmsgpb.ExecutableSpec{
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_GpuResources{
								GpuResources: &essmsgpb.GpuResources{
									Type:  "nvidia-tesla-h100",
									Count: 2,
								},
							},
						},
					},
				},
			}),
			want: []*spb.Resource{
				{ResourceType: "nvidia-tesla-h100", Count: 10.0},
			},
		},
		{
			name: "default GPU fallback",
			wu: buildWorkUnitWithJobSet([]*essmsgpb.KubernetesJob{
				{
					Replicas: 1,
					Spec: &essmsgpb.ExecutableSpec{
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_GpuResources{
								GpuResources: &essmsgpb.GpuResources{
									Type:  "",
									Count: 2,
								},
							},
						},
					},
				},
			}),
			want: []*spb.Resource{
				{ResourceType: "GPU", Count: 2.0},
			},
		},
		{
			name: "tpu topology chip calculation",
			wu: buildWorkUnitWithJobSet([]*essmsgpb.KubernetesJob{
				{
					Replicas: 2,
					Spec: &essmsgpb.ExecutableSpec{
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_TpuResources{
								TpuResources: &essmsgpb.TpuResources{
									Type:     "tpu-v4-podslice",
									Topology: "2x2x4",
								},
							},
						},
					},
				},
			}),
			want: []*spb.Resource{
				{ResourceType: "tpu-v4-podslice", Count: 32.0},
			},
		},
		{
			name: "default TPU fallback",
			wu: buildWorkUnitWithJobSet([]*essmsgpb.KubernetesJob{
				{
					Replicas: 1,
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
			}),
			want: []*spb.Resource{
				{ResourceType: "TPU", Count: 4.0},
			},
		},
		{
			name: "sidecars and initContainers aggregation",
			wu: buildWorkUnitWithJobSet([]*essmsgpb.KubernetesJob{
				{
					Replicas: 1,
					Spec: &essmsgpb.ExecutableSpec{
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_CpuResources{
								CpuResources: &essmsgpb.CpuResources{CpuMillicores: 2000, MemoryBytes: 4096},
							},
						},
					},
					InitContainers: []*essmsgpb.ExecutableSpec{
						{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_CpuResources{
									CpuResources: &essmsgpb.CpuResources{CpuMillicores: 1000, MemoryBytes: 2048},
								},
							},
						},
					},
					Sidecars: []*essmsgpb.ExecutableSpec{
						{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_GpuResources{
									GpuResources: &essmsgpb.GpuResources{Type: "nvidia-tesla-t4", Count: 1},
								},
							},
						},
					},
				},
			}),
			want: []*spb.Resource{
				{ResourceType: "cpu", Count: 3.0},
				{ResourceType: "memory", Count: 6144.0},
				{ResourceType: "nvidia-tesla-t4", Count: 1.0},
			},
		},
		{
			name: "combined CPU, Memory, GPU, TPU simultaneously",
			wu: buildWorkUnitWithJobSet([]*essmsgpb.KubernetesJob{
				{
					Replicas: 1,
					Spec: &essmsgpb.ExecutableSpec{
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_CpuResources{
								CpuResources: &essmsgpb.CpuResources{CpuMillicores: 8000, MemoryBytes: 16384},
							},
						},
					},
					Sidecars: []*essmsgpb.ExecutableSpec{
						{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_GpuResources{
									GpuResources: &essmsgpb.GpuResources{Type: "nvidia-a100", Count: 4},
								},
							},
						},
						{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_TpuResources{
									TpuResources: &essmsgpb.TpuResources{Type: "tpu-v5p", Topology: "2x2x4"},
								},
							},
						},
					},
				},
			}),
			want: []*spb.Resource{
				{ResourceType: "cpu", Count: 8.0},
				{ResourceType: "memory", Count: 16384.0},
				{ResourceType: "nvidia-a100", Count: 4.0},
				{ResourceType: "tpu-v5p", Count: 16.0},
			},
		},
		{
			name: "zero and negative resource amounts ignored",
			wu: buildWorkUnitWithJobSet([]*essmsgpb.KubernetesJob{
				{
					Replicas: 1,
					Spec: &essmsgpb.ExecutableSpec{
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_CpuResources{
								CpuResources: &essmsgpb.CpuResources{CpuMillicores: -1000, MemoryBytes: 0},
							},
						},
					},
					Sidecars: []*essmsgpb.ExecutableSpec{
						{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_GpuResources{
									GpuResources: &essmsgpb.GpuResources{Type: "gpu-zero", Count: 0},
								},
							},
						},
					},
				},
			}),
			want: []*spb.Resource{},
		},
		{
			name: "case insensitive GPU and TPU type matching",
			wu: buildWorkUnitWithJobSet([]*essmsgpb.KubernetesJob{
				{
					Replicas: 1,
					Spec: &essmsgpb.ExecutableSpec{
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_GpuResources{
								GpuResources: &essmsgpb.GpuResources{Type: "gpu", Count: 2},
							},
						},
					},
					Sidecars: []*essmsgpb.ExecutableSpec{
						{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_TpuResources{
									TpuResources: &essmsgpb.TpuResources{Type: "tpu", Topology: "2x2x2"},
								},
							},
						},
					},
				},
			}),
			want: []*spb.Resource{
				{ResourceType: "GPU", Count: 2.0},
				{ResourceType: "TPU", Count: 8.0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRequestedResources(tt.wu)
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("extractRequestedResources() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func buildWorkUnitWithJobSet(jobs []*essmsgpb.KubernetesJob) *essmsgpb.WorkUnit {
	return &essmsgpb.WorkUnit{
		Executable: &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: jobs,
				},
			},
		},
	}
}

func TestParseTotalChips(t *testing.T) {
	tests := []struct {
		topology string
		want     int64
	}{
		{"", 1},
		{"2x2", 4},
		{"2x2x4", 16},
		{"2X2X4", 16},
		{" 2x2x4 ", 16},
		{"  4X4  ", 16},
		{"8x8", 64},
		{"2x2xX", 1},
		{"2x-2x4", 1},
		{"invalid", 1},
	}

	for _, tt := range tests {
		t.Run(tt.topology, func(t *testing.T) {
			if got := tputopology.ParseTotalChips(tt.topology); got != tt.want {
				t.Errorf("tputopology.ParseTotalChips(%q) = %d, want %d", tt.topology, got, tt.want)
			}
		})
	}
}
