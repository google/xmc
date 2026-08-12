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

package statushistory

import (
	"testing"
	"time"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestExtractAllResourcesFromExecutable(t *testing.T) {
	exec := &essmsgpb.Executable{
		Type: &essmsgpb.Executable_Jobset{
			Jobset: &essmsgpb.JobSetExecutable{
				Jobs: []*essmsgpb.KubernetesJob{
					{
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_GpuResources{
									GpuResources: &essmsgpb.GpuResources{
										Type:  "nvidia-tesla-h100",
										Count: 8,
									},
								},
							},
						},
					},
					{
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_CpuResources{
									CpuResources: &essmsgpb.CpuResources{
										CpuMillicores: 16000,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	got := ExtractAllResourcesFromExecutable(exec)
	if got["nvidia-tesla-h100"] != 8 {
		t.Errorf("nvidia-tesla-h100 = %d, want 8", got["nvidia-tesla-h100"])
	}
	if got["CPU"] != 16 {
		t.Errorf("CPU = %d, want 16", got["CPU"])
	}
}

func TestQueueVelocityResult(t *testing.T) {
	res := QueueVelocityResult{
		AdmittedJobsCount: 12,
		ResourceUnits:     96,
		ResourceType:      "GPU H100",
	}
	window := 1 * time.Hour
	rate := float64(res.AdmittedJobsCount) / window.Hours()

	if rate != 12.0 {
		t.Errorf("rate = %f, want 12.0", rate)
	}
}
