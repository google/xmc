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
	"cmp"
	"slices"

	"github.com/google/go-cmp/cmp/cmpopts"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/common/resourceaccumulator"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/state"
)

// ActiveResourceQueue is the queue that holds the active work units seeded by ActiveResourceSpecs.
const ActiveResourceQueue = "gpu-queue"

// ActiveResourceSpec describes the requested resources of a work unit in active resource tests.
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
// plus terminal and other-queue work units that storage must not return.
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

// WantActiveWorkUnitResources returns what storage must return for ActiveResourceQueue after
// seeding specs, with creators keyed by experiment XID: every non-terminal spec in that queue.
// Compare with ActiveWorkUnitResourcesOrder and protocmp.Transform().
func WantActiveWorkUnitResources(specs []ActiveResourceSpec, creators map[int64]string) []storage.ActiveWorkUnitResources {
	var want []storage.ActiveWorkUnitResources
	for _, s := range specs {
		if s.Queue != ActiveResourceQueue || slices.Contains(state.WorkUnitTerminalStates, s.State) {
			continue
		}
		want = append(want, storage.ActiveWorkUnitResources{Creator: creators[s.XID], Executable: s.Executable(), ExecutionState: s.State})
	}
	return want
}

// ActiveWorkUnitResourcesOrder makes cmp.Diff ignore the order of ActiveWorkUnitResources rows,
// which are unique per (Creator, ExecutionState) in ActiveResourceSpecs.
var ActiveWorkUnitResourcesOrder = cmpopts.SortSlices(func(a, b storage.ActiveWorkUnitResources) bool {
	return cmp.Or(cmp.Compare(a.Creator, b.Creator), cmp.Compare(a.ExecutionState, b.ExecutionState)) < 0
})
