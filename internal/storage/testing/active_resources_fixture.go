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

package storagetest

import (
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/common/resourceaccumulator"
	"xmanager_cloud/internal/storage"
)

// ActiveResourceQueue is the queue that holds the active work units seeded by ActiveResourceSpecs.
const ActiveResourceQueue = "gpu-queue"

// ActiveResourceSpec describes a seeded work unit for active resource aggregation tests.
type ActiveResourceSpec struct {
	XID         int64
	WID         int64
	Queue       string
	State       essmsgpb.WorkUnitStatus_WorkUnitState
	CPUCores    float64
	MemoryGiB   float64
	GPUType     string
	GPUCount    int64
	TPUType     string
	TPUTopology string
}

// Executable builds a JobSet executable with one CPU job and optional GPU and TPU jobs.
func (s ActiveResourceSpec) Executable() *essmsgpb.Executable {
	jobs := []*essmsgpb.KubernetesJob{resourceJob(&essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
			CpuMillicores: int64(s.CPUCores * 1000),
			MemoryBytes:   int64(s.MemoryGiB * resourceaccumulator.BytesPerGiB),
		}},
	})}
	if s.GPUCount > 0 {
		jobs = append(jobs, resourceJob(&essmsgpb.ResourceSet{
			Resources: &essmsgpb.ResourceSet_GpuResources{GpuResources: &essmsgpb.GpuResources{Type: s.GPUType, Count: s.GPUCount}},
		}))
	}
	if s.TPUTopology != "" {
		jobs = append(jobs, resourceJob(&essmsgpb.ResourceSet{
			Resources: &essmsgpb.ResourceSet_TpuResources{TpuResources: &essmsgpb.TpuResources{Type: s.TPUType, Topology: s.TPUTopology}},
		}))
	}
	return &essmsgpb.Executable{
		SchedulingConstraints: &essmsgpb.SchedulingConstraints{QueueName: s.Queue},
		Type:                  &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{Jobs: jobs}},
	}
}

func resourceJob(rs *essmsgpb.ResourceSet) *essmsgpb.KubernetesJob {
	return &essmsgpb.KubernetesJob{Spec: &essmsgpb.ExecutableSpec{Resources: rs}}
}

// ActiveResourceSpecs returns six active work units in ActiveResourceQueue (three per experiment),
// plus terminal and other-queue work units that aggregation must ignore.
func ActiveResourceSpecs(aliceXID, bobXID int64) []ActiveResourceSpec {
	q := ActiveResourceQueue
	return []ActiveResourceSpec{
		{XID: aliceXID, WID: 1, Queue: q, State: essmsgpb.WorkUnitStatus_RUNNING, CPUCores: 4, MemoryGiB: 8, GPUType: "nvidia-tesla-h100", GPUCount: 4},
		{XID: aliceXID, WID: 2, Queue: q, State: essmsgpb.WorkUnitStatus_STARTING, CPUCores: 2, MemoryGiB: 4, GPUType: "nvidia-tesla-h100", GPUCount: 2},
		{XID: aliceXID, WID: 3, Queue: q, State: essmsgpb.WorkUnitStatus_SCHEDULING, CPUCores: 1, MemoryGiB: 2, GPUType: "nvidia-tesla-h100", GPUCount: 8},
		{XID: bobXID, WID: 1, Queue: q, State: essmsgpb.WorkUnitStatus_RUNNING, CPUCores: 8, MemoryGiB: 16, GPUType: "H100", GPUCount: 2},
		{XID: bobXID, WID: 2, Queue: q, State: essmsgpb.WorkUnitStatus_INITIALIZING, CPUCores: 2, MemoryGiB: 0.5, TPUType: "tpu-v5-lite-podslice", TPUTopology: "2x2"},
		{XID: bobXID, WID: 3, Queue: q, State: essmsgpb.WorkUnitStatus_SCHEDULING, CPUCores: 2, MemoryGiB: 1.5, GPUType: "H100", GPUCount: 4},
		{XID: aliceXID, WID: 4, Queue: q, State: essmsgpb.WorkUnitStatus_COMPLETED, CPUCores: 32, MemoryGiB: 64, GPUType: "nvidia-tesla-h100", GPUCount: 8},
		{XID: bobXID, WID: 4, Queue: q, State: essmsgpb.WorkUnitStatus_FAILED, CPUCores: 16, MemoryGiB: 32, GPUType: "H100", GPUCount: 8},
		{XID: aliceXID, WID: 5, Queue: "other-queue", State: essmsgpb.WorkUnitStatus_RUNNING, CPUCores: 64, MemoryGiB: 128, GPUType: "nvidia-tesla-h100", GPUCount: 16},
	}
}

// ActiveResourceSpecsActiveCount is the number of active work units in ActiveResourceQueue.
const ActiveResourceSpecsActiveCount = 6

// WantActiveResourceAllocations returns the expected aggregation of ActiveResourceSpecs when the
// experiments are created by alice@example.com and bob@example.com.
func WantActiveResourceAllocations() []storage.AuthorResourceAllocation {
	const alice, bob = "alice@example.com", "bob@example.com"
	running, pending := storage.ActiveResourceStateRunning, storage.ActiveResourceStatePending
	return []storage.AuthorResourceAllocation{
		{Author: alice, ResourceType: "cpu", State: pending, Units: 1},
		{Author: alice, ResourceType: "cpu", State: running, Units: 6},
		{Author: alice, ResourceType: "memory", State: pending, Units: 2},
		{Author: alice, ResourceType: "memory", State: running, Units: 12},
		{Author: alice, ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", State: pending, Units: 8},
		{Author: alice, ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", State: running, Units: 6},
		{Author: bob, ResourceType: "cpu", State: pending, Units: 4},
		{Author: bob, ResourceType: "cpu", State: running, Units: 8},
		{Author: bob, ResourceType: "google.com/tpu: tpu-v5-lite-podslice", State: pending, Units: 4},
		{Author: bob, ResourceType: "memory", State: pending, Units: 2},
		{Author: bob, ResourceType: "memory", State: running, Units: 16},
		{Author: bob, ResourceType: "nvidia.com/gpu: nvidia-h100-80gb", State: pending, Units: 4},
		{Author: bob, ResourceType: "nvidia.com/gpu: nvidia-h100-80gb", State: running, Units: 2},
	}
}
