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
)

// ActiveResourceSpec describes the requested resources of a work unit in active resource tests.
type ActiveResourceSpec struct {
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
		Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{Jobs: jobs}},
	}
}

func resourceJob(rs *essmsgpb.ResourceSet) *essmsgpb.KubernetesJob {
	return &essmsgpb.KubernetesJob{Spec: &essmsgpb.ExecutableSpec{Resources: rs}}
}
