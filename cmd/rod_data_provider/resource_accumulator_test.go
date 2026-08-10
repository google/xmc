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
	"context"
	"testing"
	"time"

	"golang/protobuf/v2/proto/proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestMapWorkUnitToDashboardJob_RequestedResources(t *testing.T) {
	now := time.Now()
	wu := proto.Modify(baseWorkUnit, func(w *essmsgpb.WorkUnit) {
		w.Executable = &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
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
					},
				},
			},
		}
	})

	job := mapWorkUnitToDashboardJob(context.Background(), wu, now)
	if job == nil {
		t.Fatalf("mapWorkUnitToDashboardJob returned nil")
	}
	if len(job.GetRequestedResources()) != 1 {
		t.Fatalf("RequestedResources len = %d, want 1", len(job.GetRequestedResources()))
	}
	res := job.GetRequestedResources()[0]
	if res.GetResourceType() != "nvidia-tesla-h100" || res.GetCount() != 6.0 {
		t.Errorf("RequestedResource = %v %v, want nvidia-tesla-h100 6.0", res.GetResourceType(), res.GetCount())
	}
}

func TestResourceAccumulator(t *testing.T) {
	t.Run("nil safety", func(t *testing.T) {
		var nilAcc *resourceAccumulator
		nilAcc.add("cpu", 10)
		if nilAcc.toResources() != nil {
			t.Errorf("toResources() on nil accumulator = %v, want nil", nilAcc.toResources())
		}
	})

	t.Run("aggregation and order", func(t *testing.T) {
		acc := newResourceAccumulator()
		acc.add("cpu", 2.5)
		acc.add("memory", 1024)
		acc.add("cpu", 1.5)
		acc.add("gpu", 0)  // ignore zero count
		acc.add("", 5.0)   // ignore empty resType
		acc.add("gpu", -1) // ignore negative count

		res := acc.toResources()
		if len(res) != 2 {
			t.Fatalf("toResources() len = %d, want 2", len(res))
		}
		if res[0].GetResourceType() != "cpu" || res[0].GetCount() != 4.0 {
			t.Errorf("res[0] = %v %v, want cpu 4.0", res[0].GetResourceType(), res[0].GetCount())
		}
		if res[1].GetResourceType() != "memory" || res[1].GetCount() != 1024.0 {
			t.Errorf("res[1] = %v %v, want memory 1024.0", res[1].GetResourceType(), res[1].GetCount())
		}
	})

	t.Run("addCpu addGpu addTpu helpers", func(t *testing.T) {
		acc := newResourceAccumulator()
		acc.addCpu(&essmsgpb.CpuResources{CpuMillicores: 4000, MemoryBytes: 8192})
		acc.addGpu(&essmsgpb.GpuResources{Type: "", Count: 2})        // default "GPU"
		acc.addTpu(&essmsgpb.TpuResources{Type: "", Topology: "2x2"}) // default "TPU", count=1
		acc.addCpu(nil)
		acc.addGpu(nil)
		acc.addTpu(nil)

		res := acc.toResources()
		if len(res) != 4 {
			t.Fatalf("toResources() len = %d, want 4", len(res))
		}
		if res[0].GetResourceType() != "cpu" || res[0].GetCount() != 4.0 {
			t.Errorf("res[0] = %v %v, want cpu 4.0", res[0].GetResourceType(), res[0].GetCount())
		}
		if res[1].GetResourceType() != "memory" || res[1].GetCount() != 8192.0 {
			t.Errorf("res[1] = %v %v, want memory 8192.0", res[1].GetResourceType(), res[1].GetCount())
		}
		if res[2].GetResourceType() != "GPU" || res[2].GetCount() != 2.0 {
			t.Errorf("res[2] = %v %v, want GPU 2.0", res[2].GetResourceType(), res[2].GetCount())
		}
		if res[3].GetResourceType() != "TPU" || res[3].GetCount() != 1.0 {
			t.Errorf("res[3] = %v %v, want TPU 1.0", res[3].GetResourceType(), res[3].GetCount())
		}
	})
}
