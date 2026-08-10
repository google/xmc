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
		{"invalid", 1},
	}

	for _, tt := range tests {
		t.Run(tt.topology, func(t *testing.T) {
			if got := parseTotalChips(tt.topology); got != tt.want {
				t.Errorf("parseTotalChips(%q) = %d, want %d", tt.topology, got, tt.want)
			}
		})
	}
}
