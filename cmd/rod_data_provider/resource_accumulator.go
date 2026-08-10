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
// into presentation DTOs ([]*spb.Resource) required by the ROD API.
func extractRequestedResources(wu *essmsgpb.WorkUnit) []*spb.Resource {
	resourceSets := workunit.GetWorkUnitResources(wu)
	if len(resourceSets) == 0 {
		return nil
	}

	var resources []*spb.Resource
	for _, rs := range resourceSets {
		if rs == nil {
			continue
		}
		if cpu := rs.GetCpuResources(); cpu != nil {
			resources = append(resources, extractCpuResources(cpu)...)
		} else if gpu := rs.GetGpuResources(); gpu != nil {
			resources = append(resources, extractGpuResource(gpu))
		} else if tpu := rs.GetTpuResources(); tpu != nil {
			resources = append(resources, extractTpuResource(tpu))
		}
	}
	return resources
}

func extractCpuResources(cpu *essmsgpb.CpuResources) []*spb.Resource {
	var res []*spb.Resource
	if millicores := cpu.GetCpuMillicores(); millicores > 0 {
		res = append(res, &spb.Resource{
			ResourceType: "cpu",
			Count:        float64(millicores) / 1000.0,
		})
	}
	if memoryBytes := cpu.GetMemoryBytes(); memoryBytes > 0 {
		res = append(res, &spb.Resource{
			ResourceType: "memory",
			Count:        float64(memoryBytes),
		})
	}
	return res
}

func extractGpuResource(gpu *essmsgpb.GpuResources) *spb.Resource {
	resType := gpu.GetType()
	if resType == "" {
		resType = "GPU"
	}
	return &spb.Resource{
		ResourceType: resType,
		Count:        float64(gpu.GetCount()),
	}
}

func extractTpuResource(tpu *essmsgpb.TpuResources) *spb.Resource {
	resType := tpu.GetType()
	if resType == "" {
		resType = "TPU"
	}
	chips := parseTotalChips(tpu.GetTopology())
	return &spb.Resource{
		ResourceType: resType,
		Count:        float64(chips),
	}
}

func parseTotalChips(topology string) int64 {
	if topology == "" {
		return 1
	}
	dims := strings.Split(topology, "x")
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
