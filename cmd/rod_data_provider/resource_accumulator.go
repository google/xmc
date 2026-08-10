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
	spb "xmanager_cloud/api/protos/rod"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

type resourceAccumulator struct {
	totals map[string]float64
	order  []string
}

func newResourceAccumulator() *resourceAccumulator {
	return &resourceAccumulator{
		totals: make(map[string]float64),
	}
}

func (a *resourceAccumulator) add(resType string, count float64) {
	if a == nil || count <= 0 || resType == "" {
		return
	}
	if _, exists := a.totals[resType]; !exists {
		a.order = append(a.order, resType)
	}
	a.totals[resType] += count
}

func (a *resourceAccumulator) addCpu(cpu *essmsgpb.CpuResources) {
	if cpu == nil {
		return
	}
	if cpu.GetCpuMillicores() > 0 {
		a.add("cpu", float64(cpu.GetCpuMillicores())/1000.0)
	}
	if cpu.GetMemoryBytes() > 0 {
		a.add("memory", float64(cpu.GetMemoryBytes()))
	}
}

func (a *resourceAccumulator) addGpu(gpu *essmsgpb.GpuResources) {
	if gpu == nil {
		return
	}
	resType := gpu.GetType()
	if resType == "" {
		resType = "GPU"
	}
	a.add(resType, float64(gpu.GetCount()))
}

func (a *resourceAccumulator) addTpu(tpu *essmsgpb.TpuResources) {
	if tpu == nil {
		return
	}
	resType := tpu.GetType()
	if resType == "" {
		resType = "TPU"
	}
	a.add(resType, 1)
}

func (a *resourceAccumulator) toResources() []*spb.Resource {
	if a == nil || len(a.order) == 0 {
		return nil
	}
	resources := make([]*spb.Resource, 0, len(a.order))
	for _, resType := range a.order {
		resources = append(resources, &spb.Resource{
			ResourceType: resType,
			Count:        a.totals[resType],
		})
	}
	return resources
}

func extractRequestedResources(wu *essmsgpb.WorkUnit) []*spb.Resource {
	if wu == nil || wu.GetExecutable() == nil || wu.GetExecutable().GetJobset() == nil {
		return nil
	}
	acc := newResourceAccumulator()
	for _, job := range wu.GetExecutable().GetJobset().GetJobs() {
		accumulateJobResources(acc, job)
	}
	return acc.toResources()
}

func accumulateJobResources(acc *resourceAccumulator, job *essmsgpb.KubernetesJob) {
	if acc == nil || job == nil || job.GetSpec() == nil || job.GetSpec().GetResources() == nil {
		return
	}
	switch r := job.GetSpec().GetResources().GetResources().(type) {
	case *essmsgpb.ResourceSet_CpuResources:
		acc.addCpu(r.CpuResources)
	case *essmsgpb.ResourceSet_GpuResources:
		acc.addGpu(r.GpuResources)
	case *essmsgpb.ResourceSet_TpuResources:
		acc.addTpu(r.TpuResources)
	}
}
