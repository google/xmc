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

package experiment

import (
	"context"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/godebug/diff/diff"
	"golang/protobuf/v2/encoding/prototext/prototext"
	"golang/protobuf/v2/proto/proto"
	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/validator/validator"
)

// UpdateExperimentLaunchState updates the launch state of an experiment.
func (r *Repository) UpdateExperimentLaunchState(ctx context.Context, xid int64, launchState essmsgpb.ExperimentStatus_LaunchState) (*essmsgpb.Experiment, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Update experiment launch state", "launchState", launchState)

	if launchState != essmsgpb.ExperimentStatus_COMPLETED && launchState != essmsgpb.ExperimentStatus_FAILED {
		return nil, fmt.Errorf("invalid target launch state %v, must be COMPLETED or FAILED: %w", launchState, status.ErrInvalidArgument)
	}

	var currentExp *essmsgpb.Experiment
	var updatedExp *essmsgpb.Experiment
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		var err error

		updatedExp, err = r.FetchExperimentTransaction(ctx, xid, txn)
		if err != nil {
			return err
		}

		currentExp = proto.Clone(updatedExp).(*essmsgpb.Experiment)

		if updatedExp.GetStatus().GetLaunchState() != essmsgpb.ExperimentStatus_LAUNCHING {
			return fmt.Errorf("experiment launch state is %v, want LAUNCHING: %w", updatedExp.GetStatus().GetLaunchState(), status.ErrFailedPrecondition)
		}

		updatedExp.Status.LaunchState = launchState
		updatedExp.Status.UpdateTime = timestamppb.New(time.Now())
		updatedExp.UpdateTime = updatedExp.Status.UpdateTime

		updatedExp, err = state.UpdateExperimentActiveState(updatedExp)
		if err != nil {
			logging.Error(ctx, "Failed to update experiment active state", "error", err)
			return err
		}

		var newStructuredMessage *essmsgpb.StructuredMessage
		if updatedExp.GetStatus().GetLaunchState() == essmsgpb.ExperimentStatus_FAILED {
			newStructuredMessage = experimentMessageOnLaunchFailure(updatedExp)
		} else {
			newStructuredMessage = experimentMessageOnLaunchSuccess(updatedExp)
		}

		if state.ShouldStatusMessageSupercede(ctx, updatedExp.Status.StructuredStatusMessage, newStructuredMessage) {
			updatedExp.Status.StructuredStatusMessage = newStructuredMessage
		}

		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, r.statusMessageHistoryEnabled, xid, nil, nil, currentExp.GetStatus().GetStructuredStatusMessage(), updatedExp.GetStatus().GetStructuredStatusMessage(), txn)

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
