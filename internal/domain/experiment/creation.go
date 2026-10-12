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
	"slices"
	"time"

	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/domain/workunit"
	"xmanager_cloud/internal/storage/state"
)

// WorkUnitCreationResult contains the updated Experiment domain object and mutation metadata.
type WorkUnitCreationResult struct {
	Experiment       *essmsgpb.Experiment
	ResourcesChanged bool
}

// PrepareWorkUnitCreation calculates the updated Experiment state and resources when a new WorkUnit is created.
// This is a pure domain function with no side effects and no database coupling.
func PrepareWorkUnitCreation(ctx context.Context, exp *essmsgpb.Experiment, wu *essmsgpb.WorkUnit, now time.Time) (WorkUnitCreationResult, error) {
	if exp == nil {
		return WorkUnitCreationResult{}, fmt.Errorf("experiment must not be nil")
	}
	if wu == nil || wu.GetCurrentWorkUnitExecution() == nil || wu.GetCurrentWorkUnitExecution().GetStatus() == nil {
		return WorkUnitCreationResult{}, fmt.Errorf("work unit, current execution, and status must not be nil")
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
		return WorkUnitCreationResult{}, fmt.Errorf("failed to denormalize experiment status: %w", err)
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

	return WorkUnitCreationResult{
		Experiment:       updatedExp,
		ResourcesChanged: resourcesChanged,
	}, nil
}

func equalResourceSets(a, b []*essmsgpb.ResourceSet) bool {
	return slices.EqualFunc(a, b, func(x, y *essmsgpb.ResourceSet) bool {
		return proto.Equal(x, y)
	})
}
