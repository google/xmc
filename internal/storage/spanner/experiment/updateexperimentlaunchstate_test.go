// Copyright 2025 Google LLC
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
package experiment_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/validator/validator"
)

func TestUpdateExperimentLaunchState_Success(t *testing.T) {
	testCases := []struct {
		name          string
		targetState   essmsgpb.ExperimentStatus_LaunchState
		wantStateCode essmsgpb.ExperimentStatus_LaunchState
	}{
		{
			name:          "update_to_completed",
			targetState:   essmsgpb.ExperimentStatus_COMPLETED,
			wantStateCode: essmsgpb.ExperimentStatus_COMPLETED,
		},
		{
			name:          "update_to_failed",
			targetState:   essmsgpb.ExperimentStatus_FAILED,
			wantStateCode: essmsgpb.ExperimentStatus_FAILED,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			_, essClient := emulator.Setup(ctx, t)
			storage, _ := spanner.NewStorage(essClient)

			exp := spanner.CreateBlankExperimentWithLaunchState(ctx, t, storage, 1, essmsgpb.ExperimentStatus_LAUNCHING)
			spanner.CreateBlankWorkUnit(ctx, t, storage, 1, 1)

			xid, err := validator.ValidateExperimentName(exp.GetName())
			if err != nil {
				t.Fatalf("ValidateExperimentName failed: %v", err)
			}

			updatedExp, err := storage.UpdateExperimentLaunchState(ctx, xid, tc.targetState)
			if err != nil {
				t.Fatalf("UpdateExperimentLaunchState failed: %v", err)
			}

			if updatedExp.GetStatus().GetLaunchState() != tc.wantStateCode {
				t.Errorf("UpdateExperimentLaunchState returned experiment with launch state %v, want %v", updatedExp.GetStatus().GetLaunchState(), tc.wantStateCode)
			}

			if !updatedExp.GetStatus().GetUpdateTime().AsTime().After(exp.GetStatus().GetUpdateTime().AsTime()) {
				t.Errorf("UpdateExperimentLaunchState did not update Status.UpdateTime: got %v, want > %v", updatedExp.GetStatus().GetUpdateTime(), exp.GetStatus().GetUpdateTime())
			}

			switch tc.targetState {
			case essmsgpb.ExperimentStatus_FAILED:
				sm := updatedExp.GetStatus().GetStructuredStatusMessage()
				if sm.GetSeverity() != essmsgpb.StructuredMessage_FATAL {
					t.Errorf("UpdateExperimentLaunchState(%v, %v) status message severity is %v, want %v", xid, tc.targetState, sm.GetSeverity(), essmsgpb.StructuredMessage_FATAL)
				}
				if sm.GetUserFacingText() != "Experiment failed to launch." {
					t.Errorf("UpdateExperimentLaunchState(%v, %v) status message text is %q, want %q", xid, tc.targetState, sm.GetUserFacingText(), "Experiment failed to launch.")
				}
			case essmsgpb.ExperimentStatus_COMPLETED:
				sm := updatedExp.GetStatus().GetStructuredStatusMessage()
				if sm.GetSeverity() != essmsgpb.StructuredMessage_DEBUG {
					t.Errorf("UpdateExperimentLaunchState(%v, %v) status message severity is %v, want %v", xid, tc.targetState, sm.GetSeverity(), essmsgpb.StructuredMessage_DEBUG)
				}
				if sm.GetUserFacingText() != "Experiment launched successfully." {
					t.Errorf("UpdateExperimentLaunchState(%v, %v) status message text is %q, want %q", xid, tc.targetState, sm.GetUserFacingText(), "Experiment launched successfully.")
				}
			}

			// Verify with FetchExperiment.
			gotExp, err := storage.FetchExperiment(ctx, xid)
			if err != nil {
				t.Fatalf("FetchExperiment failed: %v", err)
			}
			if diff := cmp.Diff(updatedExp, gotExp, protocmp.Transform()); diff != "" {
				t.Errorf("FetchExperiment returned diff after update (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUpdateExperimentLaunchState_StatusMessageUpdate(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	exp := &essmsgpb.Experiment{
		Name:      validator.ConstructExperimentName(999),
		Title:     "test-title",
		Creator:   "test-creator",
		Resources: []*essmsgpb.ResourceSet{},
		Urls:      []*essmsgpb.Url{},
		Tags:      []string{},
		Status: &essmsgpb.ExperimentStatus{
			LaunchState: essmsgpb.ExperimentStatus_LAUNCHING,
			StructuredStatusMessage: &essmsgpb.StructuredMessage{
				Severity:       essmsgpb.StructuredMessage_FATAL,
				UserFacingText: "Initial fatal message",
				CreateTime:     timestamppb.New(time.Now()),
				Parent:         validator.ConstructExperimentName(999),
			},
		},
	}
	_, err := storage.CreateExperiment(ctx, exp)
	if err != nil {
		t.Fatalf("CreateExperiment failed: %v", err)
	}

	// This should attempt to overwrite FATAL with INFO message for launch success,
	// but ShouldStatusMessageSupercede should prevent it because FATAL > INFO.
	_, err = storage.UpdateExperimentLaunchState(ctx, 999, essmsgpb.ExperimentStatus_COMPLETED)
	if err != nil {
		t.Fatalf("UpdateExperimentLaunchState failed: %v", err)
	}

	gotExp, err := storage.FetchExperiment(ctx, 999)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}

	if got, want := gotExp.GetStatus().GetStructuredStatusMessage().GetSeverity(), essmsgpb.StructuredMessage_FATAL; got != want {
		t.Errorf("UpdateExperimentLaunchState(%v, %v) status message severity is %v, want %v", 999, essmsgpb.ExperimentStatus_COMPLETED, got, want)
	}
	if got, want := gotExp.GetStatus().GetStructuredStatusMessage().GetUserFacingText(), "Initial fatal message"; got != want {
		t.Errorf("UpdateExperimentLaunchState(%v, %v) status message text is %q, want %q", 999, essmsgpb.ExperimentStatus_COMPLETED, got, want)
	}
}

func TestUpdateExperimentLaunchState_InvalidTargetState(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	exp := &essmsgpb.Experiment{
		Name:      "experiments/123",
		Title:     "test-title",
		Creator:   "test-creator",
		Resources: []*essmsgpb.ResourceSet{},
		Urls:      []*essmsgpb.Url{},
		Tags:      []string{},
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
			LaunchState: essmsgpb.ExperimentStatus_LAUNCHING,
		},
	}

	_, err := storage.CreateExperiment(ctx, exp)
	if err != nil {
		t.Fatalf("CreateExperiment failed: %v", err)
	}

	_, err = storage.UpdateExperimentLaunchState(ctx, 123, essmsgpb.ExperimentStatus_LAUNCHING)
	if !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("UpdateExperimentLaunchState with invalid target state returned error %v, want %v", err, status.ErrInvalidArgument)
	}
}

func TestUpdateExperimentLaunchState_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	_, err := storage.UpdateExperimentLaunchState(ctx, 123, essmsgpb.ExperimentStatus_COMPLETED)
	if !errors.Is(err, status.ErrNotFound) {
		t.Errorf("UpdateExperimentLaunchState for non-existent XID returned error %v, want %v", err, status.ErrNotFound)
	}
}

func TestUpdateExperimentLaunchState_FailedPrecondition(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	exp := &essmsgpb.Experiment{
		Name:      "experiments/123",
		Title:     "test-title",
		Creator:   "test-creator",
		Resources: []*essmsgpb.ResourceSet{},
		Urls:      []*essmsgpb.Url{},
		Tags:      []string{},
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
			LaunchState: essmsgpb.ExperimentStatus_COMPLETED, // Not LAUNCHING
		},
	}

	_, err := storage.CreateExperiment(ctx, exp)
	if err != nil {
		t.Fatalf("CreateExperiment failed: %v", err)
	}

	_, err = storage.UpdateExperimentLaunchState(ctx, 123, essmsgpb.ExperimentStatus_FAILED)
	if !errors.Is(err, status.ErrFailedPrecondition) {
		t.Errorf("UpdateExperimentLaunchState with wrong initial state returned error %v, want %v", err, status.ErrFailedPrecondition)
	}
}
