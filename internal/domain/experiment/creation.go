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

// Package experiment provides pure domain logic for Experiment aggregate root lifecycle operations.
package experiment

import (
	"context"
	"fmt"
	"time"

	"golang/protobuf/v2/proto/proto"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/domain/workunit"
	"xmanager_cloud/storage/state/state"
)

// PrepareWorkUnitCreation calculates the updated Experiment state and resources when a new WorkUnit is created.
// This is a pure domain function with no side effects and no database coupling.
// Returns the updated Experiment proto, a boolean indicating if resources changed, and an error if any.
func PrepareWorkUnitCreation(ctx context.Context, exp *essmsgpb.Experiment, wu *essmsgpb.WorkUnit, now time.Time) (*essmsgpb.Experiment, bool, error) {
	if exp == nil {
		return nil, false, fmt.Errorf("experiment must not be nil")
	}
	if wu == nil || wu.GetCurrentWorkUnitExecution() == nil || wu.GetCurrentWorkUnitExecution().GetStatus() == nil {
		return nil, false, fmt.Errorf("work unit, current execution, and status must not be nil")
	}

	clonedExp := proto.Clone(exp).(*essmsgpb.Experiment)

	if clonedExp.Status == nil {
		clonedExp.Status = &essmsgpb.ExperimentStatus{}
	}

	// 1. Calculate state deltas and denormalize status
	var deltas []*state.WorkUnitStateDelta
	deltas = append(deltas, &state.WorkUnitStateDelta{
		CurrentState:   nil,
		NewState:       wu.GetCurrentWorkUnitExecution().GetStatus().GetState().Enum(),
		CurrentMessage: nil,
		NewMessage:     wu.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage(),
	})

	updatedExp, err := state.DenormalizeWorkUnitStatus(ctx, clonedExp, deltas)
	if err != nil {
		return nil, false, fmt.Errorf("failed to denormalize experiment status: %w", err)
	}

	// 2. Merge resources required by WorkUnit into Experiment
	wuResources := workunit.GetWorkUnitResources(wu)
	resourcesChanged := false
	if len(wuResources) > 0 {
		oldResources := updatedExp.Resources
		updatedExp.Resources = workunit.MergeResourceSets(oldResources, wuResources)
		if !equalResourceSets(oldResources, updatedExp.Resources) {
			resourcesChanged = true
		}
	}

	// 3. Update timestamps
	updatedExp.UpdateTime = timestamppb.New(now)
	if updatedExp.Status != nil {
		updatedExp.Status.LastChildStatusUpdateTime = timestamppb.New(now)
		updatedExp.Status.UpdateTime = timestamppb.New(now)
	}

	return updatedExp, resourcesChanged, nil
}

func equalResourceSets(a, b []*essmsgpb.ResourceSet) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
