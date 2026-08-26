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

// Package resourceaccumulator provides shared utilities to accumulate and aggregate
// compute resources (CPU, GPU, TPU, Memory) across Executables and ResourceSets.
package resourceaccumulator

import (
	"math"
	"strings"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/common/tputopology"
	"xmanager_cloud/internal/domain/workunit"
	"xmanager_cloud/kubernetes/common/constants"
)

const (
	// ResourceTypeCPU represents the standard CPU resource identifier.
	ResourceTypeCPU = constants.ResourceTypeCPU
	// ResourceTypeMemory represents the standard Memory resource identifier.
	ResourceTypeMemory = constants.ResourceTypeMemory
	// ResourceTypeGPU represents the standard GPU resource identifier.
	ResourceTypeGPU = constants.ResourceTypeGPU
	// ResourceTypeTPU represents the standard TPU resource identifier.
	ResourceTypeTPU = constants.ResourceTypeTPU
)

// Accumulator aggregates resource counts with lowercase key normalization.
type Accumulator struct {
	totals map[string]float64
	order  []string
}

// New creates a new initialized Accumulator instance.
func New() *Accumulator {
	return &Accumulator{
		totals: make(map[string]float64),
		order:  make([]string, 0),
	}
}

// Add adds count units for the given resType, converting all keys to lowercase.
func (a *Accumulator) Add(resType string, count float64) {
	if a == nil || count <= 0 || resType == "" {
		return
	}
	if a.totals == nil {
		a.totals = make(map[string]float64)
	}
	key := strings.ToLower(resType)
	if _, exists := a.totals[key]; !exists {
		a.order = append(a.order, key)
	}
	a.totals[key] += count
	a.Normalize()
}

// AccumulateResourceSet extracts and adds CPU, GPU, and TPU resources from a ResourceSet.
func (a *Accumulator) AccumulateResourceSet(rs *essmsgpb.ResourceSet) {
	if a == nil || rs == nil {
		return
	}
	if cpu := rs.GetCpuResources(); cpu != nil {
		a.accumulateCPU(cpu)
		return
	}
	if gpu := rs.GetGpuResources(); gpu != nil {
		a.accumulateGPU(gpu)
		return
	}
	if tpu := rs.GetTpuResources(); tpu != nil {
		a.accumulateTPU(tpu)
		return
	}
}

func (a *Accumulator) accumulateCPU(cpu *essmsgpb.CpuResources) {
	if millicores := cpu.GetCpuMillicores(); millicores > 0 {
		a.Add(ResourceTypeCPU, float64(millicores)/1000.0)
	}
	if memoryBytes := cpu.GetMemoryBytes(); memoryBytes > 0 {
		a.Add(ResourceTypeMemory, float64(memoryBytes))
	}
}

func (a *Accumulator) accumulateGPU(gpu *essmsgpb.GpuResources) {
	resType := gpu.GetType()
	if resType == "" {
		resType = ResourceTypeGPU
	}
	a.Add(resType, float64(gpu.GetCount()))
}

func (a *Accumulator) accumulateTPU(tpu *essmsgpb.TpuResources) {
	resType := tpu.GetType()
	if resType == "" {
		resType = ResourceTypeTPU
	}
	topo := tpu.GetTopology()
	if topo == "" {
		topo = tpu.GetType()
	}
	chips := tputopology.ParseTotalChips(topo)
	a.Add(resType, float64(chips))
}

// AccumulateExecutable extracts domain ResourceSets from an Executable via workunit.GetWorkUnitResources and accumulates them.
func (a *Accumulator) AccumulateExecutable(exec *essmsgpb.Executable) {
	if a == nil || exec == nil {
		return
	}
	resourceSets := workunit.GetWorkUnitResources(&essmsgpb.WorkUnit{Executable: exec})
	for _, rs := range resourceSets {
		a.AccumulateResourceSet(rs)
	}
	a.Normalize()
}

// Normalize merges generic GPU ("nvidia.com/gpu", "gpu") or TPU ("google.com/tpu", "tpu") counts
// into specific accelerator types if specific accelerator types exist in the accumulator.
func (a *Accumulator) Normalize() {
	if a == nil || len(a.totals) == 0 {
		return
	}
	var specificGPU, specificTPU string
	for _, k := range a.order {
		if specificGPU == "" && IsSpecificGPUKey(k) {
			specificGPU = k
		}
		if specificTPU == "" && IsSpecificTPUKey(k) {
			specificTPU = k
		}
	}
	mergeGeneric := func(target string, genericKeys ...string) {
		if target == "" {
			return
		}
		for _, k := range genericKeys {
			if count, exists := a.totals[k]; exists {
				a.totals[target] += count
				delete(a.totals, k)
			}
		}
	}
	mergeGeneric(specificGPU, "nvidia.com/gpu", "gpu")
	mergeGeneric(specificTPU, "google.com/tpu", "tpu")
	// Single-pass in-place compaction (0 allocations)
	n := 0
	for _, k := range a.order {
		if _, exists := a.totals[k]; exists {
			a.order[n] = k
			n++
		}
	}
	a.order = a.order[:n]
}

// Totals returns the accumulated float64 resource counts map (with CPU in Cores).
func (a *Accumulator) Totals() map[string]float64 {
	if a == nil {
		return make(map[string]float64)
	}
	result := make(map[string]float64, len(a.totals))
	for k, v := range a.totals {
		result[k] = v
	}
	return result
}

// IntTotals returns the accumulated int64 resource counts map.
// Note: Totals()["cpu"] returns CPU in Cores (float64, e.g. 4.0), whereas IntTotals()["cpu"]
// converts CPU Cores to int64 Millicores (e.g. 4000) for integer storage requirements.
// All other resource types (memory, gpu, tpu) retain identical units across both methods.
func (a *Accumulator) IntTotals() map[string]int64 {
	if a == nil {
		return make(map[string]int64)
	}
	result := make(map[string]int64, len(a.totals))
	for k, v := range a.totals {
		if k == ResourceTypeCPU {
			result[ResourceTypeCPU] = int64(math.Round(v * 1000.0))
		} else {
			result[k] = int64(math.Round(v))
		}
	}
	return result
}

// Order returns the slice of resource type keys in insertion order.
// Returns a non-nil empty slice []string{} when no keys have been added.
func (a *Accumulator) Order() []string {
	if a == nil {
		return []string{}
	}
	return append([]string{}, a.order...)
}
