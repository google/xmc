// Copyright 2025 Google LLC
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
package spanner

import (
	"context"
	"fmt"
	"time"

	timestamppb "protobuf/timestamp_go_proto"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/godebug/diff/diff"
	"golang/protobuf/v2/encoding/prototext/prototext"
	"golang/protobuf/v2/proto/proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/validator/validator"
	"task/go/status"
)

// UpdateExperimentLaunchState updates the launch state of an experiment.
func (s *Storage) UpdateExperimentLaunchState(ctx context.Context, xid int64, launchState essmsgpb.ExperimentStatus_LaunchState) (*essmsgpb.Experiment, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Update experiment launch state", "launchState", launchState)

	if launchState != essmsgpb.ExperimentStatus_COMPLETED && launchState != essmsgpb.ExperimentStatus_FAILED {
		return nil, fmt.Errorf("invalid target launch state %v, must be COMPLETED or FAILED: %w", launchState, status.ErrInvalidArgument)
	}

	var currentExp *essmsgpb.Experiment
	var updatedExp *essmsgpb.Experiment
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		var err error

		// 1. Read the existing ExperimentStatus from storage.
		updatedExp, err = s.fetchExperimentTransaction(ctx, xid, txn)
		if err != nil {
			return err
		}

		currentExp = proto.Clone(updatedExp).(*essmsgpb.Experiment)

		if updatedExp.GetStatus().GetLaunchState() != essmsgpb.ExperimentStatus_LAUNCHING {
			return fmt.Errorf("experiment launch state is %v, want LAUNCHING: %w", updatedExp.GetStatus().GetLaunchState(), status.ErrFailedPrecondition)
		}

		// 2. Update the experiment status and timestamps.
		updatedExp.Status.LaunchState = launchState
		updatedExp.Status.UpdateTime = timestamppb.New(time.Now())
		updatedExp.UpdateTime = updatedExp.Status.UpdateTime

		// 3. Figure out what the new ACTIVE state should be.
		updatedExp, err = state.UpdateExperimentActiveState(updatedExp)
		if err != nil {
			logging.Error(ctx, "Failed to update experiment active state", "error", err)
			return err
		}

		// 4. Add a status message based on the launch state.
		var newStructuredMessage *essmsgpb.StructuredMessage
		if updatedExp.GetStatus().GetLaunchState() == essmsgpb.ExperimentStatus_FAILED {
			newStructuredMessage = experimentMessageOnLaunchFailure(updatedExp)
		} else {
			newStructuredMessage = experimentMessageOnLaunchSuccess(updatedExp)
		}

		if state.ShouldStatusMessageSupercede(ctx, updatedExp.Status.StructuredStatusMessage, newStructuredMessage) {
			updatedExp.Status.StructuredStatusMessage = newStructuredMessage
		}

		s.saveStatusMessageHistoryIfChanged(ctx, xid, nil, nil, currentExp.GetStatus().GetStructuredStatusMessage(), updatedExp.GetStatus().GetStructuredStatusMessage(), txn)

		// 5. Buffer a write mutation for the experiment row.
		mutation, err := spannerdata.ExperimentProtoToStatusUpdateMutation(updatedExp)
		if err != nil {
			return fmt.Errorf("failed to create mutation for experiment status update: %w", err)
		}
		return txn.BufferWrite([]*spanner.Mutation{mutation})
	})
	if err != nil {
		logging.Error(ctx, "Failed to update experiment launch state", "error", err)
		return nil, err
	}

	protoDiff := diff.Diff(prototext.Format(currentExp), prototext.Format(updatedExp))
	logging.Info(ctx, fmt.Sprintf("Updated Experiment launch state: %s", protoDiff), "updatedExperiment", updatedExp)

	return updatedExp, nil
}

func experimentMessageOnLaunchFailure(exp *essmsgpb.Experiment) *essmsgpb.StructuredMessage {
	return &essmsgpb.StructuredMessage{
		Parent:         exp.GetName(),
		Severity:       essmsgpb.StructuredMessage_FATAL,
		Source:         essmsgpb.StructuredMessage_XMANAGER_CLIENT,
		UserFacingText: "Experiment failed to launch.",
		CreateTime:     exp.Status.UpdateTime,
	}
}

func experimentMessageOnLaunchSuccess(exp *essmsgpb.Experiment) *essmsgpb.StructuredMessage {
	return &essmsgpb.StructuredMessage{
		Parent:         exp.GetName(),
		Severity:       essmsgpb.StructuredMessage_DEBUG,
		Source:         essmsgpb.StructuredMessage_XMANAGER_CLIENT,
		UserFacingText: "Experiment launched successfully.",
		CreateTime:     exp.Status.UpdateTime,
	}
}
