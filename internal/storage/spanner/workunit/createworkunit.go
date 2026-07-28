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

package workunit

import (
	"context"
	"fmt"
	"sort"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
	"xmanager_cloud/logging/logging"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/validator/validator"
)

// CreateWorkUnit creates a new work unit in Spanner.
// TODO(b/517479143): Refactor CreateWorkUnit to separate validation, status denormalization, status message saving, and resource set merging into a domain service layer above the storage repository.
func (r *Repository) CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	ctx = logging.WithWorkUnitExecutionName(ctx, wu.GetCurrentWorkUnitExecution().GetName())
	logging.Info(ctx, "Write work unit to storage", "work_unit", wu)

	exp := &essmsgpb.Experiment{}
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		now := time.Now().Truncate(time.Microsecond)

		// 1. Extract the XID and WID from the work unit name.
		xid, wid, err := validator.ValidateWorkUnitNameAllowZeroWID(wu.GetName())
		if err != nil {
			return fmt.Errorf("failed to validate work unit name: %w", err)
		}

		// 1.1. If the work unit ID is 0, fetch the next available ID and update the names.
		initialEID := int64(1)
		if wid == 0 {
			wid, err = r.FetchNextWorkUnitIDTransaction(ctx, xid, tx)
			if err != nil {
				return fmt.Errorf("failed to fetch next work unit ID: %w", err)
			}
			wu.Name = validator.ConstructWorkUnitName(xid, wid)
			wu.CurrentWorkUnitExecution.Name = validator.ConstructWorkUnitExecutionName(xid, wid, initialEID)
			wu.CurrentWorkUnitExecution.Status.SystemMessage.Parent = wu.GetName()
		}

		// 1.2. Set the initial timestamps to the current time.
		wu.CreateTime = timestamppb.New(now)
		wu.UpdateTime = timestamppb.New(now)
		wu.CurrentWorkUnitExecution.CreateTime = timestamppb.New(now)
		wu.CurrentWorkUnitExecution.UpdateTime = timestamppb.New(now)
		wu.CurrentWorkUnitExecution.Status.UpdateTime = timestamppb.New(now)
		wu.CurrentWorkUnitExecution.Status.StateUpdateTime = timestamppb.New(now)

		// 2. Construct the Spanner mutation for the create work unit.
		mutation, err := spannerdata.WorkUnitProtoToInsertMutation(wu)
		if err != nil {
			return err
		}

		// 3. Buffer the create work unit mutation.
		err = tx.BufferWrite([]*spanner.Mutation{mutation})
		if err != nil {
			logging.Error(ctx, "failed to buffer write mutation for create work unit", "error", err)
			return err
		}

		// 4. Read the Experiment row.
		if r.experimentRepo == nil {
			return fmt.Errorf("experiment repository is not initialized")
		}
		exp, err = r.experimentRepo.FetchExperimentTransaction(ctx, xid, tx)
		if err != nil {
			return fmt.Errorf("failed to fetch experiment: %w", err)
		}
		if exp == nil {
			return fmt.Errorf("experiment %d not found", xid)
		}

		// 5. Update the denormalized status fields in the Experiment row.
		var executionDeltas []*state.WorkUnitStateDelta
		executionDeltas = append(executionDeltas, &state.WorkUnitStateDelta{
			CurrentState:   nil,
			NewState:       wu.GetCurrentWorkUnitExecution().GetStatus().GetState().Enum(),
			CurrentMessage: nil,
			NewMessage:     wu.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage(),
		})

		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, r.statusMessageHistoryEnabled, xid, &wid, &initialEID, nil, wu.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage(), tx)

		exp, err = state.DenormalizeWorkUnitStatus(ctx, exp, executionDeltas)
		if err != nil {
			return fmt.Errorf("failed to update experiment status: %w", err)
		}

		// 6. Update the Experiment row.
		exp.UpdateTime = timestamppb.New(now)
		exp.Status.LastChildStatusUpdateTime = timestamppb.New(now)
		exp.Status.UpdateTime = timestamppb.New(now)
		expMutation, err := spannerdata.ExperimentProtoToStatusUpdateMutation(exp)
		if err != nil {
			logging.Error(ctx, "failed to create mutation for experiment", "experiment", exp, "error", err)
			return err
		}
		tx.BufferWrite([]*spanner.Mutation{expMutation})

		// 7.1. Get the resources required by the work unit.
		wuResources := GetWorkUnitResources(wu)
		// 7.2. Merge the resources required by the work unit with the resources in the experiment.
		if wuResources != nil {
			exp.Resources = MergeResourceSets(exp.Resources, wuResources)

			// 7.3. Update the Experiment's resources.
			expResourcesMutation, err := spannerdata.ExperimentProtoToResourcesUpdateMutation(exp)
			if err != nil {
				logging.Error(ctx, "failed to create mutation for experiment", "experiment", exp, "error", err)
				return err
			}
			tx.BufferWrite([]*spanner.Mutation{expResourcesMutation})
		}

		return nil
	})
	if err != nil {
		logging.Error(ctx, "failed to create work unit", "work_unit", wu, "error", err)
		return nil, err
	}

	logging.Info(ctx, "Written work unit to storage", "work_unit", wu)
	logging.Info(ctx, "Updated experiment successfully", "experiment", exp)

	return wu, nil
}

// getWorkUnitResources returns the resources required by the work unit, in a minimal merged set
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

// mergeResourceSets merges two sets of resources into a single set. If the same resource type is
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
