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

package main

import (
	"strings"

	spb "xmanager_cloud/api/protos/rod"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/common/tputopology"
	"xmanager_cloud/internal/domain/workunit"
)

const (
	resourceTypeCPU    = "cpu"
	resourceTypeMemory = "memory"
	resourceTypeGPU    = "GPU"
	resourceTypeTPU    = "TPU"
)

type resourceAccumulator struct {
	totals map[string]float64
	order  []string
}

func (a *resourceAccumulator) add(resType string, count float64) {
	if count <= 0 || resType == "" {
		return
	}
	matchedKey := resType
	for existingKey := range a.totals {
		if strings.EqualFold(existingKey, resType) {
			matchedKey = existingKey
			break
		}
	}
	if _, exists := a.totals[matchedKey]; !exists {
		a.order = append(a.order, matchedKey)
	}
	a.totals[matchedKey] += count
}

// extractRequestedResources converts domain ResourceSets from workunit.GetWorkUnitResources
// into presentation DTOs ([]*spb.Resource) required by the ROD API, aggregating identical resource types.
func extractRequestedResources(wu *essmsgpb.WorkUnit) []*spb.Resource {
	resourceSets := workunit.GetWorkUnitResources(wu)
	if len(resourceSets) == 0 {
		return nil
	}
	totals, order := accumulateResourceTotals(resourceSets)
	return buildResourceDTOs(totals, order)
}

func accumulateResourceTotals(resourceSets []*essmsgpb.ResourceSet) (totals map[string]float64, order []string) {
	acc := &resourceAccumulator{totals: make(map[string]float64, len(resourceSets))}
	for _, rs := range resourceSets {
		accumulateResource(rs, acc)
	}
	return acc.totals, acc.order
}

func buildResourceDTOs(totals map[string]float64, order []string) []*spb.Resource {
	resources := make([]*spb.Resource, 0, len(order))
	for _, resType := range order {
		resources = append(resources, &spb.Resource{
			ResourceType: resType,
			Count:        totals[resType],
		})
	}
	return resources
}

func accumulateResource(rs *essmsgpb.ResourceSet, acc *resourceAccumulator) {
	if rs == nil || acc == nil {
		return
	}
	if cpu := rs.GetCpuResources(); cpu != nil {
		accumulateCpuResource(cpu, acc)
	} else if gpu := rs.GetGpuResources(); gpu != nil {
		accumulateGpuResource(gpu, acc)
	} else if tpu := rs.GetTpuResources(); tpu != nil {
		accumulateTpuResource(tpu, acc)
	}
}

func accumulateCpuResource(cpu *essmsgpb.CpuResources, acc *resourceAccumulator) {
	if millicores := cpu.GetCpuMillicores(); millicores > 0 {
		acc.add(resourceTypeCPU, float64(millicores)/1000.0)
	}
	if memoryBytes := cpu.GetMemoryBytes(); memoryBytes > 0 {
		acc.add(resourceTypeMemory, float64(memoryBytes))
	}
}

func accumulateGpuResource(gpu *essmsgpb.GpuResources, acc *resourceAccumulator) {
	resType := gpu.GetType()
	if resType == "" || strings.EqualFold(resType, resourceTypeGPU) {
		resType = resourceTypeGPU
	}
	acc.add(resType, float64(gpu.GetCount()))
}

func accumulateTpuResource(tpu *essmsgpb.TpuResources, acc *resourceAccumulator) {
	resType := tpu.GetType()
	if resType == "" || strings.EqualFold(resType, resourceTypeTPU) {
		resType = resourceTypeTPU
	}
	topo := tpu.GetTopology()
	if topo == "" {
		topo = tpu.GetType()
	}
	chips := tputopology.ParseTotalChips(topo)
	acc.add(resType, float64(chips))
}
