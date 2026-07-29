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

// Package workunit provides domain service logic for managing WorkUnits and WorkUnitExecutions.
package workunit

import (
	"context"
	"fmt"
	"sort"
	"time"

	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/validator/validator"
)

// Store defines the storage operations required by the WorkUnit domain service.
type Store interface {
	storage.WorkUnitStore
	FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error)
	UpdateExperiment(ctx context.Context, update *storage.ExperimentUpdate) (*essmsgpb.Experiment, *essmsgpb.Experiment, error)
}

// Service provides domain-level operations for WorkUnit lifecycle management.
type Service struct {
	storage Store
}

// NewService creates a new WorkUnit domain service instance.
func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("storage must not be nil")
	}
	return &Service{storage: store}, nil
}

// CreateWorkUnit validates, creates, and propagates status for a new WorkUnit.
func (s *Service) CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	if wu == nil {
		return nil, fmt.Errorf("work unit must not be nil")
	}
	if wu.GetCurrentWorkUnitExecution() == nil {
		return nil, fmt.Errorf("current work unit execution must not be nil")
	}

	xid, _, err := validator.ValidateWorkUnitNameAllowZeroWID(wu.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate work unit name: %w (%v)", status.ErrInvalidArgument, err)
	}

	ctx = logging.WithWorkUnitExecutionName(ctx, wu.GetCurrentWorkUnitExecution().GetName())
	logging.Info(ctx, "Domain: CreateWorkUnit", "work_unit", wu)

	// 1. Persist the WorkUnit in storage
	createdWu, err := s.storage.CreateWorkUnit(ctx, wu)
	if err != nil {
		return nil, fmt.Errorf("failed to create work unit: %w", err)
	}
	if createdWu == nil {
		return nil, fmt.Errorf("storage returned nil work unit")
	}
	if createdWu.GetCurrentWorkUnitExecution() == nil {
		return nil, fmt.Errorf("created work unit has no current execution")
	}

	// 2. Fetch the Experiment to update denormalized status and resources
	exp, err := s.storage.FetchExperiment(ctx, xid)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch experiment %d: %w", xid, err)
	}
	if exp == nil {
		return nil, fmt.Errorf("experiment %d not found", xid)
	}
	if exp.Status == nil {
		exp.Status = &essmsgpb.ExperimentStatus{}
	}

	// 3. Calculate state deltas and denormalize status
	var deltas []*state.WorkUnitStateDelta
	deltas = append(deltas, &state.WorkUnitStateDelta{
		CurrentState:   nil,
		NewState:       createdWu.GetCurrentWorkUnitExecution().GetStatus().GetState().Enum(),
		CurrentMessage: nil,
		NewMessage:     createdWu.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage(),
	})

	exp, err = state.DenormalizeWorkUnitStatus(ctx, exp, deltas)
	if err != nil {
		return nil, fmt.Errorf("failed to denormalize experiment status: %w", err)
	}

	// 4. Merge resources required by WorkUnit into Experiment
	wuResources := getWorkUnitResources(createdWu)
	if wuResources != nil {
		exp.Resources = mergeResourceSets(exp.Resources, wuResources)
	}

	now := time.Now()
	exp.UpdateTime = timestamppb.New(now)
	if exp.Status != nil {
		exp.Status.LastChildStatusUpdateTime = timestamppb.New(now)
		exp.Status.UpdateTime = timestamppb.New(now)
	}

	// 5. Update Experiment row in storage
	_, _, err = s.storage.UpdateExperiment(ctx, &storage.ExperimentUpdate{
		XID:        xid,
		Experiment: exp,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update experiment after work unit creation: %w", err)
	}

	return createdWu, nil
}

// RestartWorkUnitExecutions restarts executions for specified work unit IDs and updates experiment status.
func (s *Service) RestartWorkUnitExecutions(ctx context.Context, requester string, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Domain: Restarting work unit executions", "wids", wids)

	// 1. Restart executions in storage layer
	newExecutions, err := s.storage.RestartWorkUnitExecutions(ctx, requester, xid, wids)
	if err != nil {
		return nil, fmt.Errorf("failed to restart work unit executions: %w", err)
	}

	// 2. Fetch experiment and update denormalized status
	exp, err := s.storage.FetchExperiment(ctx, xid)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch experiment %d: %w", xid, err)
	}
	if exp == nil {
		return nil, fmt.Errorf("experiment %d not found", xid)
	}
	if exp.Status == nil {
		exp.Status = &essmsgpb.ExperimentStatus{}
	}

	var deltas []*state.WorkUnitStateDelta
	for _, newExec := range newExecutions {
		if newExec == nil {
			continue
		}
		deltas = append(deltas, &state.WorkUnitStateDelta{
			NewState:   newExec.GetStatus().GetState().Enum(),
			NewMessage: newExec.GetStatus().GetSystemMessage(),
		})
	}

	exp, err = state.DenormalizeWorkUnitStatus(ctx, exp, deltas)
	if err != nil {
		return nil, fmt.Errorf("failed to denormalize experiment status: %w", err)
	}

	now := time.Now()
	exp.UpdateTime = timestamppb.New(now)
	if exp.Status != nil {
		exp.Status.LastChildStatusUpdateTime = timestamppb.New(now)
		exp.Status.UpdateTime = timestamppb.New(now)
	}

	_, _, err = s.storage.UpdateExperiment(ctx, &storage.ExperimentUpdate{
		XID:        xid,
		Experiment: exp,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update experiment after work unit restart: %w", err)
	}

	return newExecutions, nil
}

func getWorkUnitResources(wu *essmsgpb.WorkUnit) []*essmsgpb.ResourceSet {
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

	return mergeResourceSets(nil, resources)
}

func mergeResourceSets(a, b []*essmsgpb.ResourceSet) []*essmsgpb.ResourceSet {
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
			if cpu == nil {
				cpu = &essmsgpb.CpuResources{}
			}
			cpu.CpuMillicores += res.GetCpuMillicores()
			cpu.MemoryBytes += res.GetMemoryBytes()
			cpuEphemeral += rs.GetEphemeralStorageBytes()
		} else if res := rs.GetGpuResources(); res != nil {
			gpuRes := res
			if _, ok := gpus[gpuRes.GetType()]; !ok {
				gpus[gpuRes.GetType()] = &essmsgpb.GpuResources{Type: gpuRes.GetType()}
			}
			gpus[gpuRes.GetType()].Count += gpuRes.GetCount()
			gpuEphemeral[gpuRes.GetType()] += rs.GetEphemeralStorageBytes()
		} else if res := rs.GetTpuResources(); res != nil {
			tpus = append(tpus, rs)
		}
	}

	if cpu != nil {
		merged = append(merged, &essmsgpb.ResourceSet{
			Resources:             &essmsgpb.ResourceSet_CpuResources{CpuResources: cpu},
			EphemeralStorageBytes: cpuEphemeral,
		})
	}

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
