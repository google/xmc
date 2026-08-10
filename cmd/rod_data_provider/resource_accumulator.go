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
	"strconv"
	"strings"

	spb "xmanager_cloud/api/protos/rod"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/domain/workunit"
)

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

func accumulateResourceTotals(resourceSets []*essmsgpb.ResourceSet) (map[string]float64, []string) {
	totals := make(map[string]float64, len(resourceSets))
	var order []string

	addResource := func(resType string, count float64) {
		if count <= 0 {
			return
		}
		if _, exists := totals[resType]; !exists {
			order = append(order, resType)
		}
		totals[resType] += count
	}

	for _, rs := range resourceSets {
		accumulateResource(rs, addResource)
	}
	return totals, order
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

func accumulateResource(rs *essmsgpb.ResourceSet, addResource func(string, float64)) {
	if rs == nil {
		return
	}
	if cpu := rs.GetCpuResources(); cpu != nil {
		accumulateCpuResource(cpu, addResource)
	} else if gpu := rs.GetGpuResources(); gpu != nil {
		accumulateGpuResource(gpu, addResource)
	} else if tpu := rs.GetTpuResources(); tpu != nil {
		accumulateTpuResource(tpu, addResource)
	}
}

func accumulateCpuResource(cpu *essmsgpb.CpuResources, addResource func(string, float64)) {
	if millicores := cpu.GetCpuMillicores(); millicores > 0 {
		addResource("cpu", float64(millicores)/1000.0)
	}
	if memoryBytes := cpu.GetMemoryBytes(); memoryBytes > 0 {
		addResource("memory", float64(memoryBytes))
	}
}

func accumulateGpuResource(gpu *essmsgpb.GpuResources, addResource func(string, float64)) {
	resType := gpu.GetType()
	if resType == "" {
		resType = "GPU"
	}
	addResource(resType, float64(gpu.GetCount()))
}

func accumulateTpuResource(tpu *essmsgpb.TpuResources, addResource func(string, float64)) {
	resType := tpu.GetType()
	if resType == "" {
		resType = "TPU"
	}
	chips := parseTotalChips(tpu.GetTopology())
	addResource(resType, float64(chips))
}

func parseTotalChips(topology string) int64 {
	if topology == "" {
		return 1
	}
	cleaned := strings.TrimSpace(strings.ToLower(topology))
	dims := strings.Split(cleaned, "x")
	totalChips := int64(1)
	for _, d := range dims {
		v, err := strconv.ParseInt(d, 10, 64)
		if err != nil || v <= 0 {
			return 1
		}
		totalChips *= v
	}
	return totalChips
}
