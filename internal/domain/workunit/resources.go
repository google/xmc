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

// Package workunit provides pure domain utilities for WorkUnits and their resource calculations.
package workunit

import (
	"sort"

	"golang/protobuf/v2/proto/proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

// GetWorkUnitResources returns the resources required by the work unit in a minimal merged set.
func GetWorkUnitResources(wu *essmsgpb.WorkUnit) []*essmsgpb.ResourceSet {
	if wu == nil || wu.GetExecutable() == nil || wu.GetExecutable().GetJobset() == nil {
		return nil
	}
	var resources []*essmsgpb.ResourceSet
	for _, job := range wu.GetExecutable().GetJobset().GetJobs() {
		if job == nil {
			continue
		}
		replicas := job.GetReplicas()
		if replicas <= 0 {
			replicas = 1
		}
		specs := []*essmsgpb.ExecutableSpec{job.GetSpec()}
		specs = append(specs, job.GetSidecars()...)
		specs = append(specs, job.GetInitContainers()...)
		for _, spec := range specs {
			if spec == nil || spec.GetResources() == nil {
				continue
			}
			res := spec.GetResources()
			if cpu := res.GetCpuResources(); cpu != nil {
				resources = append(resources, &essmsgpb.ResourceSet{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: cpu.GetCpuMillicores() * int64(replicas),
							MemoryBytes:   cpu.GetMemoryBytes() * int64(replicas),
						},
					},
					EphemeralStorageBytes: res.GetEphemeralStorageBytes() * int64(replicas),
				})
			} else if gpu := res.GetGpuResources(); gpu != nil {
				resources = append(resources, &essmsgpb.ResourceSet{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  gpu.GetType(),
							Count: gpu.GetCount() * int64(replicas),
						},
					},
					EphemeralStorageBytes: res.GetEphemeralStorageBytes() * int64(replicas),
				})
			} else {
				for i := 0; i < int(replicas); i++ {
					resources = append(resources, res)
				}
			}
		}
	}

	if len(resources) == 0 {
		return nil
	}

	return MergeResourceSets(nil, resources)
}

// MergeResourceSets merges two sets of resources into a single set.
// If the same resource type is present in both sets, total amounts are summed.
func MergeResourceSets(a, b []*essmsgpb.ResourceSet) []*essmsgpb.ResourceSet {
	var allRes []*essmsgpb.ResourceSet
	allRes = append(allRes, a...)
	allRes = append(allRes, b...)
	if len(allRes) == 0 {
		return nil
	}

	var merged []*essmsgpb.ResourceSet
	var cpu *essmsgpb.CpuResources
	var cpuEphemeral int64
	gpus := make(map[string]*essmsgpb.GpuResources)
	gpuEphemeral := make(map[string]int64)
	var tpus []*essmsgpb.ResourceSet

	for _, rs := range allRes {
		if rs == nil {
			continue
		}
		if res := rs.GetCpuResources(); res != nil {
			cpu, cpuEphemeral = mergeCpuResource(cpu, cpuEphemeral, rs, res)
			continue
		}
		if res := rs.GetGpuResources(); res != nil {
			mergeGpuResource(gpus, gpuEphemeral, rs, res)
			continue
		}
		if rs.GetTpuResources() != nil {
			tpus = append(tpus, proto.Clone(rs).(*essmsgpb.ResourceSet))
		}
	}

	if cpu != nil {
		merged = append(merged, &essmsgpb.ResourceSet{
			Resources:             &essmsgpb.ResourceSet_CpuResources{CpuResources: cpu},
			EphemeralStorageBytes: cpuEphemeral,
		})
	}

	// Sort GPU types for deterministic order.
	gpuTypes := make([]string, 0, len(gpus))
	for gpuType := range gpus {
		gpuTypes = append(gpuTypes, gpuType)
	}
	sort.Strings(gpuTypes)

	for _, gpuType := range gpuTypes {
		gpu := gpus[gpuType]
		merged = append(merged, &essmsgpb.ResourceSet{
			Resources:             &essmsgpb.ResourceSet_GpuResources{GpuResources: gpu},
			EphemeralStorageBytes: gpuEphemeral[gpu.GetType()],
		})
	}

	// Sort TPU types for deterministic order.
	sort.Slice(tpus, func(i, j int) bool {
		resI := tpus[i].GetTpuResources()
		resJ := tpus[j].GetTpuResources()
		if resI.GetType() != resJ.GetType() {
			return resI.GetType() < resJ.GetType()
		}
		return resI.GetTopology() < resJ.GetTopology()
	})
	merged = append(merged, tpus...)

	return merged
}

func mergeCpuResource(cpu *essmsgpb.CpuResources, cpuEphemeral int64, rs *essmsgpb.ResourceSet, res *essmsgpb.CpuResources) (*essmsgpb.CpuResources, int64) {
	if cpu == nil {
		cpu = &essmsgpb.CpuResources{}
	}
	cpu.CpuMillicores += res.GetCpuMillicores()
	cpu.MemoryBytes += res.GetMemoryBytes()
	return cpu, cpuEphemeral + rs.GetEphemeralStorageBytes()
}

func mergeGpuResource(gpus map[string]*essmsgpb.GpuResources, gpuEphemeral map[string]int64, rs *essmsgpb.ResourceSet, res *essmsgpb.GpuResources) {
	gpuType := res.GetType()
	if _, ok := gpus[gpuType]; !ok {
		gpus[gpuType] = &essmsgpb.GpuResources{Type: gpuType}
	}
	gpus[gpuType].Count += res.GetCount()
	gpuEphemeral[gpuType] += rs.GetEphemeralStorageBytes()
}
