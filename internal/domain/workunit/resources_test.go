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

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
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

func TestMergeResourceSets_GPU(t *testing.T) {
	setA := []*essmsgpb.ResourceSet{
		{
			Resources: &essmsgpb.ResourceSet_GpuResources{
				GpuResources: &essmsgpb.GpuResources{
					Type:  "NVIDIA-A100-80GB",
					Count: 2,
				},
			},
			EphemeralStorageBytes: 100,
		},
	}
	setB := []*essmsgpb.ResourceSet{
		{
			Resources: &essmsgpb.ResourceSet_GpuResources{
				GpuResources: &essmsgpb.GpuResources{
					Type:  "NVIDIA-A100-80GB",
					Count: 4,
				},
			},
			EphemeralStorageBytes: 200,
		},
	}

	merged := MergeResourceSets(setA, setB)
	if len(merged) != 1 {
		t.Fatalf("MergeResourceSets returned %d sets, want 1", len(merged))
	}

	want := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_GpuResources{
			GpuResources: &essmsgpb.GpuResources{
				Type:  "NVIDIA-A100-80GB",
				Count: 6,
			},
		},
		EphemeralStorageBytes: 300,
	}

	if diff := cmp.Diff(want, merged[0], protocmp.Transform()); diff != "" {
		t.Errorf("MergeResourceSets() diff (-want +got):\n%s", diff)
	}
}
