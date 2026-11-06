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
	"testing"

	essmsgpb "xmanager_cloud/api/protos/experiment"
)

func TestActiveResourceSpec_Executable(t *testing.T) {
	tests := []struct {
		name     string
		spec     ActiveResourceSpec
		wantJobs int
	}{
		{name: "cpu only", spec: ActiveResourceSpec{Queue: "q", CPUCores: 1, MemoryGiB: 1}, wantJobs: 1},
		{name: "cpu and gpu", spec: ActiveResourceSpec{Queue: "q", CPUCores: 1, GPUType: "H100", GPUCount: 2}, wantJobs: 2},
		{name: "cpu and tpu", spec: ActiveResourceSpec{Queue: "q", CPUCores: 1, TPUType: "tpu-v5-lite-podslice", TPUTopology: "2x2"}, wantJobs: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exec := tc.spec.Executable()
			if got := exec.GetSchedulingConstraints().GetQueueName(); got != "q" {
				t.Errorf("QueueName = %q, want \"q\"", got)
			}
			if got := len(exec.GetJobset().GetJobs()); got != tc.wantJobs {
				t.Errorf("len(Jobs) = %d, want %d", got, tc.wantJobs)
			}
		})
	}
}

func TestActiveResourceSpecs(t *testing.T) {
	active := 0
	for _, spec := range ActiveResourceSpecs(1, 2) {
		if spec.Queue != ActiveResourceQueue {
			continue
		}
		switch spec.State {
		case essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.WorkUnitStatus_STARTING,
			essmsgpb.WorkUnitStatus_SCHEDULING, essmsgpb.WorkUnitStatus_INITIALIZING:
			active++
		}
	}
	if active != ActiveResourceSpecsActiveCount {
		t.Errorf("active specs = %d, want %d", active, ActiveResourceSpecsActiveCount)
	}
	if got := len(WantActiveResourceAllocations()); got == 0 {
		t.Error("WantActiveResourceAllocations() returned no rows")
	}
}
