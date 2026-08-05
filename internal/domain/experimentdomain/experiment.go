// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package experimentdomain

import (
	"context"
	"fmt"
	"time"

	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/validator/validator"
)

// PrepareExperimentCreation validates an experiment proto, assigns initial creation and status
// timestamps, and returns the extracted experiment ID (XID).
func PrepareExperimentCreation(ctx context.Context, exp *essmsgpb.Experiment, now time.Time) (int64, error) {
	if exp == nil {
		return 0, fmt.Errorf("experiment must not be nil: %w", status.ErrInvalidArgument)
	}

	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return 0, fmt.Errorf("failed to validate experiment name: %w", err)
	}

	if exp.Status == nil {
		exp.Status = &essmsgpb.ExperimentStatus{}
	}

	nowProto := timestamppb.New(now)
	exp.CreateTime = nowProto
	exp.UpdateTime = nowProto
	exp.Status.LastChildStatusUpdateTime = nowProto
	exp.Status.UpdateTime = nowProto

	return xid, nil
}

// PrepareWorkUnitCreation applies domain rules to an experiment when a child work unit is created.
// It denormalizes the work unit status into the experiment status, updates experiment timestamps,
// and merges the required work unit resources into the experiment resource sets.
// It returns the updated experiment and a boolean indicating whether resources changed.
func PrepareWorkUnitCreation(ctx context.Context, exp *essmsgpb.Experiment, wu *essmsgpb.WorkUnit, now time.Time) (*essmsgpb.Experiment, bool, error) {
	if exp == nil || wu == nil {
		return nil, false, fmt.Errorf("experiment and work unit must not be nil: %w", status.ErrInvalidArgument)
	}

	var executionDeltas []*state.WorkUnitStateDelta
	executionDeltas = append(executionDeltas, &state.WorkUnitStateDelta{
		CurrentState:   nil,
		NewState:       wu.GetCurrentWorkUnitExecution().GetStatus().GetState().Enum(),
		CurrentMessage: nil,
		NewMessage:     wu.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage(),
	})

	if exp.Status == nil {
		exp.Status = &essmsgpb.ExperimentStatus{}
	}

	exp, err := state.DenormalizeWorkUnitStatus(ctx, exp, executionDeltas)
	if err != nil {
		return nil, false, fmt.Errorf("failed to update experiment status: %w", err)
	}

	nowProto := timestamppb.New(now)
	exp.UpdateTime = nowProto
	exp.Status.LastChildStatusUpdateTime = nowProto
	exp.Status.UpdateTime = nowProto

	wuResources := GetWorkUnitResources(wu)
	if wuResources != nil {
		exp.Resources = MergeResourceSets(exp.Resources, wuResources)
		return exp, true, nil
	}

	return exp, false, nil
}
