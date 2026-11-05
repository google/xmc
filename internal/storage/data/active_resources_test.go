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
	"xmanager_cloud/internal/common/resourceaccumulator"
	"xmanager_cloud/internal/storage"
)

func sampleExecutable(cpu float64, memBytes int64, gpuType string, gpuCount int64) *essmsgpb.Executable {
	jobs := []*essmsgpb.KubernetesJob{{
		Spec: &essmsgpb.ExecutableSpec{Resources: &essmsgpb.ResourceSet{
			Resources: &essmsgpb.ResourceSet_CpuResources{
				CpuResources: &essmsgpb.CpuResources{CpuMillicores: int64(cpu * 1000), MemoryBytes: memBytes},
			},
		}},
	}}
	if gpuCount > 0 {
		jobs = append(jobs, &essmsgpb.KubernetesJob{
			Spec: &essmsgpb.ExecutableSpec{Resources: &essmsgpb.ResourceSet{
				Resources: &essmsgpb.ResourceSet_GpuResources{
					GpuResources: &essmsgpb.GpuResources{Type: gpuType, Count: gpuCount},
				},
			}},
		})
	}
	return &essmsgpb.Executable{
		Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{Jobs: jobs}},
	}
}

func TestClassifyActiveResourceState(t *testing.T) {
	tests := []struct {
		name      string
		state     essmsgpb.WorkUnitStatus_WorkUnitState
		wantState storage.ActiveResourceState
		wantOK    bool
	}{
		{name: "running", state: essmsgpb.WorkUnitStatus_RUNNING, wantState: storage.ActiveResourceStateRunning, wantOK: true},
		{name: "starting counts as running", state: essmsgpb.WorkUnitStatus_STARTING, wantState: storage.ActiveResourceStateRunning, wantOK: true},
		{name: "scheduling counts as pending", state: essmsgpb.WorkUnitStatus_SCHEDULING, wantState: storage.ActiveResourceStatePending, wantOK: true},
		{name: "initializing counts as pending", state: essmsgpb.WorkUnitStatus_INITIALIZING, wantState: storage.ActiveResourceStatePending, wantOK: true},
		{name: "completed is inactive", state: essmsgpb.WorkUnitStatus_COMPLETED, wantState: "", wantOK: false},
		{name: "failed is inactive", state: essmsgpb.WorkUnitStatus_FAILED, wantState: "", wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotState, gotOK := ClassifyActiveResourceState(tc.state)
			if gotState != tc.wantState || gotOK != tc.wantOK {
				t.Errorf("ClassifyActiveResourceState(%v) = (%q, %t), want (%q, %t)", tc.state, gotState, gotOK, tc.wantState, tc.wantOK)
			}
		})
	}
}

func TestAggregateAuthorResourceSamples(t *testing.T) {
	samples := []ActiveWorkUnitResourceSample{
		{
			Creator:        "bob@example.com",
			Executable:     sampleExecutable(4, 8*resourceaccumulator.BytesPerGiB, "nvidia-tesla-h100", 2),
			ExecutionState: essmsgpb.WorkUnitStatus_SCHEDULING,
		},
		{
			Creator:        "alice@example.com",
			Executable:     sampleExecutable(2, 4*resourceaccumulator.BytesPerGiB, "H100", 4),
			ExecutionState: essmsgpb.WorkUnitStatus_RUNNING,
		},
		{
			Creator:        "alice@example.com",
			Executable:     sampleExecutable(1, 2*resourceaccumulator.BytesPerGiB, "nvidia-h100-80gb", 2),
			ExecutionState: essmsgpb.WorkUnitStatus_STARTING,
		},
		{
			Creator:        "alice@example.com",
			Executable:     sampleExecutable(0.5, resourceaccumulator.BytesPerGiB/2, "", 0),
			ExecutionState: essmsgpb.WorkUnitStatus_INITIALIZING,
		},
		{
			Creator:        "alice@example.com",
			Executable:     sampleExecutable(16, 32*resourceaccumulator.BytesPerGiB, "H100", 8),
			ExecutionState: essmsgpb.WorkUnitStatus_COMPLETED,
		},
		{
			Creator:        "alice@example.com",
			Executable:     nil,
			ExecutionState: essmsgpb.WorkUnitStatus_RUNNING,
		},
	}

	got := AggregateAuthorResourceSamples(samples)
	want := []storage.AuthorResourceAllocation{
		{Author: "alice@example.com", ResourceType: "cpu", State: storage.ActiveResourceStatePending, Units: 0.5},
		{Author: "alice@example.com", ResourceType: "cpu", State: storage.ActiveResourceStateRunning, Units: 3},
		{Author: "alice@example.com", ResourceType: "memory", State: storage.ActiveResourceStatePending, Units: 0.5},
		{Author: "alice@example.com", ResourceType: "memory", State: storage.ActiveResourceStateRunning, Units: 6},
		{Author: "alice@example.com", ResourceType: "nvidia.com/gpu: nvidia-h100-80gb", State: storage.ActiveResourceStateRunning, Units: 6},
		{Author: "bob@example.com", ResourceType: "cpu", State: storage.ActiveResourceStatePending, Units: 4},
		{Author: "bob@example.com", ResourceType: "memory", State: storage.ActiveResourceStatePending, Units: 8},
		{Author: "bob@example.com", ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", State: storage.ActiveResourceStatePending, Units: 2},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("AggregateAuthorResourceSamples() mismatch (-want +got):\n%s", diff)
	}
}
