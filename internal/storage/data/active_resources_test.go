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

package data

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	storagetest "xmanager_cloud/internal/storage/testing"
)

func sample(creator string, wuState essmsgpb.WorkUnitStatus_WorkUnitState, spec storagetest.ActiveResourceSpec) ActiveWorkUnitResourceSample {
	return ActiveWorkUnitResourceSample{Creator: creator, Executable: spec.Executable(), ExecutionState: wuState}
}

func TestAggregateAuthorResourceSamples(t *testing.T) {
	const alice, bob = "alice@example.com", "bob@example.com"
	samples := []ActiveWorkUnitResourceSample{
		sample(bob, essmsgpb.WorkUnitStatus_SCHEDULING, storagetest.ActiveResourceSpec{CPUCores: 4, MemoryGiB: 8, GPUType: "nvidia-tesla-h100", GPUCount: 2}),
		sample(alice, essmsgpb.WorkUnitStatus_RUNNING, storagetest.ActiveResourceSpec{CPUCores: 2, MemoryGiB: 4, GPUType: "H100", GPUCount: 4}),
		sample(alice, essmsgpb.WorkUnitStatus_STARTING, storagetest.ActiveResourceSpec{CPUCores: 1, MemoryGiB: 2, GPUType: "nvidia-h100-80gb", GPUCount: 2}),
		sample(alice, essmsgpb.WorkUnitStatus_INITIALIZING, storagetest.ActiveResourceSpec{CPUCores: 0.5, MemoryGiB: 0.5}),
		sample(alice, essmsgpb.WorkUnitStatus_COMPLETED, storagetest.ActiveResourceSpec{CPUCores: 16, MemoryGiB: 32, GPUType: "H100", GPUCount: 8}),
		{Creator: alice, Executable: nil, ExecutionState: essmsgpb.WorkUnitStatus_RUNNING},
	}

	got := AggregateAuthorResourceSamples(samples)
	want := []storage.AuthorResourceAllocation{
		{Author: alice, ResourceType: "cpu", RunningUnits: 3, PendingUnits: 0.5},
		{Author: alice, ResourceType: "memory", RunningUnits: 6, PendingUnits: 0.5},
		{Author: alice, ResourceType: "nvidia.com/gpu: nvidia-h100-80gb", RunningUnits: 6},
		{Author: bob, ResourceType: "cpu", PendingUnits: 4},
		{Author: bob, ResourceType: "memory", PendingUnits: 8},
		{Author: bob, ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", PendingUnits: 2},
	}
	if diff := cmp.Diff(want, got, storagetest.AuthorAllocationOrder); diff != "" {
		t.Errorf("AggregateAuthorResourceSamples() mismatch (-want +got):\n%s", diff)
	}
}
