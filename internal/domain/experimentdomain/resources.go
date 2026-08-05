// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package experimentdomain contains pure domain logic for experiments,
// including creation preparation, status denormalization, and resource merging.
package experimentdomain

import (
	"sort"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

// GetWorkUnitResources returns the resources required by the work unit, in a minimal merged set
// of resources. For example, if two work units require 1 V100 each, this will return 2 V100s,
// not 2 x 1 V100s.
func GetWorkUnitResources(wu *essmsgpb.WorkUnit) []*essmsgpb.ResourceSet {
	var resources []*essmsgpb.ResourceSet
	for _, job := range wu.GetExecutable().GetJobset().GetJobs() {
		replicas := job.GetReplicas()
		if replicas == 0 {
			replicas = 1
		}
		specs := []*essmsgpb.ExecutableSpec{job.GetSpec()}
		specs = append(specs, job.GetSidecars()...)
		specs = append(specs, job.GetInitContainers()...)
		for _, spec := range specs {
			if spec.GetResources() == nil {
				continue
			}
			for i := 0; i < int(replicas); i++ {
				resources = append(resources, spec.GetResources())
			}
		}
	}

	if len(resources) == 0 {
		return nil
	}

	return MergeResourceSets(nil, resources)
}

// MergeResourceSets merges two sets of resources into a single set. If the same resource type is
// present in both sets, the total amount of that resource type is the sum of the two sets.
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
		switch {
		case rs.GetCpuResources() != nil:
			res := rs.GetCpuResources()
			if cpu == nil {
				cpu = &essmsgpb.CpuResources{}
			}
			cpu.CpuMillicores += res.GetCpuMillicores()
			cpu.MemoryBytes += res.GetMemoryBytes()
			cpuEphemeral += rs.GetEphemeralStorageBytes()
		case rs.GetGpuResources() != nil:
			gpuRes := rs.GetGpuResources()
			if _, ok := gpus[gpuRes.GetType()]; !ok {
				gpus[gpuRes.GetType()] = &essmsgpb.GpuResources{Type: gpuRes.GetType()}
			}
			gpus[gpuRes.GetType()].Count += gpuRes.GetCount()
			gpuEphemeral[gpuRes.GetType()] += rs.GetEphemeralStorageBytes()
		case rs.GetTpuResources() != nil:
			tpus = append(tpus, rs)
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
