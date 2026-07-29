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

// Package workunit provides domain-level operations for WorkUnit lifecycle management.
package workunit

import (
	"context"
	"fmt"
	"time"

	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/validator/validator"
)

// Store defines the storage operations required by the WorkUnit domain service.
// Satisfied by storage.Storage, Spanner repositories, or lightweight test stubs.
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
		return nil, fmt.Errorf("invalid work unit name: %w", err)
	}

	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Domain: CreateWorkUnit", "work_unit", wu)

	// 1. Create WorkUnit in storage
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
			CurrentState:   nil,
			NewState:       newExec.GetStatus().GetState().Enum(),
			CurrentMessage: nil,
			NewMessage:     newExec.GetStatus().GetSystemMessage(),
		})
	}

	exp, err = state.DenormalizeWorkUnitStatus(ctx, exp, deltas)
	if err != nil {
		return nil, fmt.Errorf("failed to denormalize experiment status after restart: %w", err)
	}

	now := time.Now()
	exp.UpdateTime = timestamppb.New(now)
	if exp.Status != nil {
		exp.Status.LastChildStatusUpdateTime = timestamppb.New(now)
		exp.Status.UpdateTime = timestamppb.New(now)
	}

	// 3. Update experiment row in storage
	_, _, err = s.storage.UpdateExperiment(ctx, &storage.ExperimentUpdate{
		XID:        xid,
		Experiment: exp,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update experiment after restarting work units: %w", err)
	}

	return newExecutions, nil
}

func getWorkUnitResources(wu *essmsgpb.WorkUnit) []*essmsgpb.ResourceSet {
	if wu == nil || wu.GetExecutable() == nil || wu.GetExecutable().GetJobset() == nil {
		return nil
	}
	var resources []*essmsgpb.ResourceSet
	for _, job := range wu.GetExecutable().GetJobset().GetJobs() {
		if job == nil || job.GetSpec() == nil || job.GetSpec().GetResources() == nil {
			continue
		}
		resources = append(resources, job.GetSpec().GetResources())
	}
	return resources
}

func mergeResourceSets(dst, src []*essmsgpb.ResourceSet) []*essmsgpb.ResourceSet {
	var merged []*essmsgpb.ResourceSet
	merged = append(merged, dst...)
	merged = append(merged, src...)
	return merged
}
