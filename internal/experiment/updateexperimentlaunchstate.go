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

	"github.com/kylelemons/godebug/diff"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/state"
	"xmanager_cloud/internal/validator"
	"xmanager_cloud/internal/workunit"
)

// UpdateExperimentLaunchState updates the launch state of an experiment.
func UpdateExperimentLaunchState(ctx context.Context, txRunner storage.TransactionRunner, xid int64, launchState essmsgpb.ExperimentStatus_LaunchState) (*essmsgpb.Experiment, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Update experiment launch state", "launchState", launchState)

	if launchState != essmsgpb.ExperimentStatus_COMPLETED && launchState != essmsgpb.ExperimentStatus_FAILED {
		return nil, fmt.Errorf("invalid target launch state %v, must be COMPLETED or FAILED: %w", launchState, grpcerr.ErrInvalidArgument)
	}

	var currentExp *essmsgpb.Experiment
	var updatedExp *essmsgpb.Experiment
	err := txRunner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		var err error

		// 1. Read the existing ExperimentStatus from storage.
		updatedExp, err = tx.Experiments.FetchExperiment(ctx, xid)
		if err != nil {
			return err
		}

		currentExp = proto.Clone(updatedExp).(*essmsgpb.Experiment)

		if updatedExp.GetStatus().GetLaunchState() != essmsgpb.ExperimentStatus_LAUNCHING {
			return fmt.Errorf("experiment launch state is %v, want LAUNCHING: %w", updatedExp.GetStatus().GetLaunchState(), grpcerr.ErrFailedPrecondition)
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

		if err := workunit.RecordStatusMessageHistory(ctx, tx.StatusMessages, xid, nil, nil, currentExp.GetStatus().GetStructuredStatusMessage(), updatedExp.GetStatus().GetStructuredStatusMessage()); err != nil {
			return fmt.Errorf("failed to save status message history: %w", err)
		}

		// 5. Buffer a write mutation for the experiment row.
		return tx.Experiments.UpdateExperimentStatus(ctx, updatedExp)
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
