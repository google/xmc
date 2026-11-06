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
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
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
// experiments are created by alice@example.com and bob@example.com. Compare with
// AuthorAllocationOrder, since storage returns rows in no particular order.
func WantActiveResourceAllocations() []storage.AuthorResourceAllocation {
	const alice, bob = "alice@example.com", "bob@example.com"
	return []storage.AuthorResourceAllocation{
		{Author: alice, ResourceType: "cpu", RunningUnits: 6, PendingUnits: 1},
		{Author: alice, ResourceType: "memory", RunningUnits: 12, PendingUnits: 2},
		{Author: alice, ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", RunningUnits: 6, PendingUnits: 8},
		{Author: bob, ResourceType: "cpu", RunningUnits: 8, PendingUnits: 4},
		{Author: bob, ResourceType: "google.com/tpu: tpu-v5-lite-podslice", PendingUnits: 4},
		{Author: bob, ResourceType: "memory", RunningUnits: 16, PendingUnits: 2},
		{Author: bob, ResourceType: "nvidia.com/gpu: nvidia-h100-80gb", RunningUnits: 2, PendingUnits: 4},
	}
}

// AuthorAllocationOrder makes cmp.Diff ignore the order of AuthorResourceAllocation rows.
var AuthorAllocationOrder = cmpopts.SortSlices(func(a, b storage.AuthorResourceAllocation) bool {
	if a.Author != b.Author {
		return a.Author < b.Author
	}
	return a.ResourceType < b.ResourceType
})

// WorkUnitSnapshotter opens read-only work unit snapshots (implemented by the Spanner and
// Postgres work unit stores).
type WorkUnitSnapshotter interface {
	WithReadOnlyWorkUnitSnapshot(ctx context.Context, fn func(ctx context.Context, repo storage.WorkUnitSnapshotRepository) error) error
}

// readActiveResourcesSnapshot reads the first jobs page of pageSize and the per-author
// allocations of queues from one snapshot.
func readActiveResourcesSnapshot(ctx context.Context, s WorkUnitSnapshotter, queues []string, pageSize int32) (*storage.ActiveWorkUnitsResult, []storage.AuthorResourceAllocation, error) {
	var page *storage.ActiveWorkUnitsResult
	var allocations []storage.AuthorResourceAllocation
	err := s.WithReadOnlyWorkUnitSnapshot(ctx, func(txCtx context.Context, repo storage.WorkUnitSnapshotRepository) error {
		var err error
		if page, err = repo.FetchActiveWorkUnitsByQueues(txCtx, queues, pageSize, ""); err != nil {
			return err
		}
		allocations, err = repo.AggregateActiveResourcesByAuthor(txCtx, queues)
		return err
	})
	return page, allocations, err
}

// VerifyActiveResourcesSnapshot checks that one snapshot of a store seeded with
// ActiveResourceSpecs returns a two-job first page and the allocations of every active job.
func VerifyActiveResourcesSnapshot(ctx context.Context, t testing.TB, s WorkUnitSnapshotter) {
	t.Helper()
	page, allocations, err := readActiveResourcesSnapshot(ctx, s, []string{ActiveResourceQueue}, 2)
	if err != nil {
		t.Fatalf("readActiveResourcesSnapshot failed: %v", err)
	}
	if len(page.WorkUnits) != 2 || page.TotalQueueSize != ActiveResourceSpecsActiveCount || page.NextPageToken == "" {
		t.Fatalf("page = (len=%d, total=%d, next=%q), want (2, %d, non-empty)", len(page.WorkUnits), page.TotalQueueSize, page.NextPageToken, ActiveResourceSpecsActiveCount)
	}
	if diff := cmp.Diff(WantActiveResourceAllocations(), allocations, AuthorAllocationOrder); diff != "" {
		t.Errorf("AggregateActiveResourcesByAuthor() mismatch (-want +got):\n%s", diff)
	}
}

// VerifyEmptyActiveResourcesSnapshot checks that a snapshot read without queues returns no rows.
func VerifyEmptyActiveResourcesSnapshot(ctx context.Context, t testing.TB, s WorkUnitSnapshotter) {
	t.Helper()
	page, allocations, err := readActiveResourcesSnapshot(ctx, s, nil, 10)
	if err != nil || len(page.WorkUnits) != 0 || len(allocations) != 0 {
		t.Errorf("readActiveResourcesSnapshot(nil queues) = (%v, %v, %v), want empty", page, allocations, err)
	}
}
