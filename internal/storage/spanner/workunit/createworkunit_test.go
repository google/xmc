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
package workunit_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	domainworkunit "xmanager_cloud/internal/domain/workunit"
)

func workUnitWithResource(r *essmsgpb.ResourceSet) *essmsgpb.WorkUnit {
	return &essmsgpb.WorkUnit{
		Executable: &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
						{
							Spec: &essmsgpb.ExecutableSpec{
								Resources: r,
							},
						},
					},
				},
			},
		},
	}
}

func TestGetWorkUnitResources(t *testing.T) {
	cpuRes := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_CpuResources{
			CpuResources: &essmsgpb.CpuResources{
				CpuMillicores: 1000,
				MemoryBytes:   1024,
			},
		},
		EphemeralStorageBytes: 2048,
	}

	gpuRes := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_GpuResources{
			GpuResources: &essmsgpb.GpuResources{
				Type:  "nvidia-tesla-v100",
				Count: 2,
			},
		},
		EphemeralStorageBytes: 2048,
	}

	tpuRes := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_TpuResources{
			TpuResources: &essmsgpb.TpuResources{
				Type:     "tpu-v4-pod",
				Topology: "2x2x2",
			},
		},
		EphemeralStorageBytes: 2048,
	}

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
			wu:   workUnitWithResource(cpuRes),
			want: []*essmsgpb.ResourceSet{cpuRes},
		},
		{
			name: "gpu_resources",
			wu:   workUnitWithResource(gpuRes),
			want: []*essmsgpb.ResourceSet{gpuRes},
		},
		{
			name: "tpu_resources",
			wu:   workUnitWithResource(tpuRes),
			want: []*essmsgpb.ResourceSet{tpuRes},
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
							CpuMillicores: 1200,
							MemoryBytes:   1280,
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := domainworkunit.GetWorkUnitResources(tc.wu)
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
			got := domainworkunit.MergeResourceSets(tc.a, tc.b)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("mergeResourceSets(%v, %v) returned diff (-want +got):\n%s", tc.a, tc.b, diff)
			}
		})
	}
}
