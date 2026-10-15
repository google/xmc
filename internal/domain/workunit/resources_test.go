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

package workunit

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
)

func TestGetWorkUnitResources_Nil(t *testing.T) {
	if res := GetWorkUnitResources(nil); res != nil {
		t.Errorf("GetWorkUnitResources(nil) = %v, want nil", res)
	}
	if res := GetWorkUnitResources(&essmsgpb.WorkUnit{}); res != nil {
		t.Errorf("GetWorkUnitResources(empty wu) = %v, want nil", res)
	}
}

func TestGetWorkUnitResources_CPU(t *testing.T) {
	wu := &essmsgpb.WorkUnit{
		Executable: &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
						{
							Name:     "test-job",
							Replicas: 2,
							Spec: &essmsgpb.ExecutableSpec{
								Resources: &essmsgpb.ResourceSet{
									Resources: &essmsgpb.ResourceSet_CpuResources{
										CpuResources: &essmsgpb.CpuResources{
											CpuMillicores: 1000,
											MemoryBytes:   2048,
										},
									},
									EphemeralStorageBytes: 500,
								},
							},
						},
					},
				},
			},
		},
	}

	res := GetWorkUnitResources(wu)
	if len(res) != 1 {
		t.Fatalf("GetWorkUnitResources returned %d sets, want 1", len(res))
	}

	want := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_CpuResources{
			CpuResources: &essmsgpb.CpuResources{
				CpuMillicores: 2000,
				MemoryBytes:   4096,
			},
		},
		EphemeralStorageBytes: 1000,
	}

	if diff := cmp.Diff(want, res[0], protocmp.Transform()); diff != "" {
		t.Errorf("GetWorkUnitResources() diff (-want +got):\n%s", diff)
	}
}

func newGpuResourceSet(count int64, storage int64) *essmsgpb.ResourceSet {
	return &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_GpuResources{
			GpuResources: &essmsgpb.GpuResources{
				Type:  "NVIDIA-A100-80GB",
				Count: count,
			},
		},
		EphemeralStorageBytes: storage,
	}
}

func TestMergeResourceSets_GPU(t *testing.T) {
	setA := []*essmsgpb.ResourceSet{newGpuResourceSet(2, 100)}
	setB := []*essmsgpb.ResourceSet{newGpuResourceSet(4, 200)}

	merged := MergeResourceSets(setA, setB)
	if len(merged) != 1 {
		t.Fatalf("MergeResourceSets returned %d sets, want 1", len(merged))
	}

	want := newGpuResourceSet(6, 300)

	if diff := cmp.Diff(want, merged[0], protocmp.Transform()); diff != "" {
		t.Errorf("MergeResourceSets() diff (-want +got):\n%s", diff)
	}
}

func TestGetWorkUnitResources_SidecarsAndInitContainers(t *testing.T) {
	wu := &essmsgpb.WorkUnit{
		Executable: &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
						{
							Name:     "job-with-containers",
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
							Sidecars: []*essmsgpb.ExecutableSpec{
								{
									Resources: &essmsgpb.ResourceSet{
										Resources: &essmsgpb.ResourceSet_CpuResources{
											CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 500,
												MemoryBytes:   512,
											},
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
	}

	res := GetWorkUnitResources(wu)
	if len(res) != 1 {
		t.Fatalf("GetWorkUnitResources returned %d sets, want 1", len(res))
	}

	want := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_CpuResources{
			CpuResources: &essmsgpb.CpuResources{
				CpuMillicores: 1700,
				MemoryBytes:   1792,
			},
		},
	}

	if diff := cmp.Diff(want, res[0], protocmp.Transform()); diff != "" {
		t.Errorf("GetWorkUnitResources(Sidecars/InitContainers) diff (-want +got):\n%s", diff)
	}
}

func TestGetWorkUnitResources_ZeroOrNegativeReplicas(t *testing.T) {
	wu := &essmsgpb.WorkUnit{
		Executable: &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
						{
							Name:     "job-zero-replicas",
							Replicas: 0,
							Spec: &essmsgpb.ExecutableSpec{
								Resources: &essmsgpb.ResourceSet{
									Resources: &essmsgpb.ResourceSet_CpuResources{
										CpuResources: &essmsgpb.CpuResources{
											CpuMillicores: 500,
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
	}

	res := GetWorkUnitResources(wu)
	if len(res) != 1 {
		t.Fatalf("GetWorkUnitResources returned %d sets, want 1", len(res))
	}

	want := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_CpuResources{
			CpuResources: &essmsgpb.CpuResources{
				CpuMillicores: 500,
				MemoryBytes:   1024,
			},
		},
	}

	if diff := cmp.Diff(want, res[0], protocmp.Transform()); diff != "" {
		t.Errorf("GetWorkUnitResources(Replicas: 0) diff (-want +got):\n%s", diff)
	}
}

func TestMergeResourceSets_MultiGpuSorting(t *testing.T) {
	setA := []*essmsgpb.ResourceSet{
		{
			Resources: &essmsgpb.ResourceSet_GpuResources{
				GpuResources: &essmsgpb.GpuResources{
					Type:  "NVIDIA-V100-16GB",
					Count: 1,
				},
			},
		},
		{
			Resources: &essmsgpb.ResourceSet_GpuResources{
				GpuResources: &essmsgpb.GpuResources{
					Type:  "NVIDIA-A100-80GB",
					Count: 2,
				},
			},
		},
	}

	merged := MergeResourceSets(setA, nil)
	if len(merged) != 2 {
		t.Fatalf("MergeResourceSets returned %d sets, want 2", len(merged))
	}

	if merged[0].GetGpuResources().GetType() != "NVIDIA-A100-80GB" {
		t.Errorf("First GPU type = %s, want NVIDIA-A100-80GB (alphabetical sorting)", merged[0].GetGpuResources().GetType())
	}
	if merged[1].GetGpuResources().GetType() != "NVIDIA-V100-16GB" {
		t.Errorf("Second GPU type = %s, want NVIDIA-V100-16GB (alphabetical sorting)", merged[1].GetGpuResources().GetType())
	}
}

func TestMergeResourceSets_TpuSortingAndCloning(t *testing.T) {
	tpu1 := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_TpuResources{
			TpuResources: &essmsgpb.TpuResources{
				Type:     "v4",
				Topology: "2x2x4",
			},
		},
	}
	tpu2 := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_TpuResources{
			TpuResources: &essmsgpb.TpuResources{
				Type:     "v4",
				Topology: "2x2x2",
			},
		},
	}

	merged := MergeResourceSets([]*essmsgpb.ResourceSet{tpu1, tpu2}, nil)
	if len(merged) != 2 {
		t.Fatalf("MergeResourceSets returned %d sets, want 2", len(merged))
	}

	if merged[0].GetTpuResources().GetTopology() != "2x2x2" {
		t.Errorf("First TPU topology = %s, want 2x2x2 (sorted)", merged[0].GetTpuResources().GetTopology())
	}
	if merged[1].GetTpuResources().GetTopology() != "2x2x4" {
		t.Errorf("Second TPU topology = %s, want 2x2x4 (sorted)", merged[1].GetTpuResources().GetTopology())
	}

	// Verify cloning (pointers should be distinct)
	if merged[0] == tpu2 || merged[1] == tpu1 {
		t.Errorf("MergeResourceSets failed to clone TPU ResourceSet pointers!")
	}
}

func TestMergeResourceSets_EphemeralOnly(t *testing.T) {
	set := []*essmsgpb.ResourceSet{
		{
			EphemeralStorageBytes: 1024,
		},
	}

	merged := MergeResourceSets(set, nil)
	if len(merged) != 1 {
		t.Fatalf("MergeResourceSets returned %d sets, want 1", len(merged))
	}

	if merged[0].GetEphemeralStorageBytes() != 1024 {
		t.Errorf("EphemeralStorageBytes = %d, want 1024", merged[0].GetEphemeralStorageBytes())
	}
	if merged[0].GetCpuResources() == nil {
		t.Errorf("CpuResources is nil, expected initialized CpuResources container for ephemeral storage")
	}
}

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

func TestGetWorkUnitResources_Table(t *testing.T) {
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
			name: "multiple_resources",
			wu: &essmsgpb.WorkUnit{
				Executable: &essmsgpb.Executable{
					Type: &essmsgpb.Executable_Jobset{
						Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Spec: &essmsgpb.ExecutableSpec{
										Resources: cpuRes,
									},
								},
								{
									Spec: &essmsgpb.ExecutableSpec{
										Resources: gpuRes,
									},
								},
							},
						},
					},
				},
			},
			want: []*essmsgpb.ResourceSet{cpuRes, gpuRes},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GetWorkUnitResources(tc.wu)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("GetWorkUnitResources(%v) returned diff (-want +got):\n%s", tc.wu, diff)
			}
		})
	}
}

func TestMergeResourceSets_Table(t *testing.T) {
	cpu1 := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_CpuResources{
			CpuResources: &essmsgpb.CpuResources{
				CpuMillicores: 1000,
				MemoryBytes:   1024,
			},
		},
		EphemeralStorageBytes: 2048,
	}
	cpu2 := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_CpuResources{
			CpuResources: &essmsgpb.CpuResources{
				CpuMillicores: 500,
				MemoryBytes:   512,
			},
		},
		EphemeralStorageBytes: 1024,
	}
	gpuV100 := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_GpuResources{
			GpuResources: &essmsgpb.GpuResources{
				Type:  "nvidia-tesla-v100",
				Count: 2,
			},
		},
		EphemeralStorageBytes: 2048,
	}
	tpuV4 := &essmsgpb.ResourceSet{
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
			a:    []*essmsgpb.ResourceSet{cpu1},
			b:    nil,
			want: []*essmsgpb.ResourceSet{cpu1},
		},
		{
			name: "two_cpu",
			a:    []*essmsgpb.ResourceSet{cpu1},
			b:    []*essmsgpb.ResourceSet{cpu2},
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
			name: "one_tpu",
			a:    []*essmsgpb.ResourceSet{tpuV4},
			b:    nil,
			want: []*essmsgpb.ResourceSet{tpuV4},
		},
		{
			name: "cpu_gpu_tpu",
			a:    []*essmsgpb.ResourceSet{cpu1, gpuV100},
			b:    []*essmsgpb.ResourceSet{tpuV4},
			want: []*essmsgpb.ResourceSet{cpu1, gpuV100, tpuV4},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeResourceSets(tc.a, tc.b)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("MergeResourceSets(%v, %v) returned diff (-want +got):\n%s", tc.a, tc.b, diff)
			}
		})
	}
}
