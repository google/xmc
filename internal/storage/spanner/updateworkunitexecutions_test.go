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
package spanner

import (
	"context"
	"testing"
	"time"

	"go/log"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/storage/storage"

	fieldmaskpb "protobuf/field_mask_go_proto"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestUpdateWorkUnitExecutions_EmptyUpdates(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	got, err := storageClient.UpdateWorkUnitExecutions(ctx, 123, []*storage.WorkUnitExecutionUpdate{})
	if err != nil {
		t.Errorf("UpdateWorkUnitExecutions with empty updates failed: %v", err)
	}
	if got != nil {
		t.Errorf("UpdateWorkUnitExecutions with empty updates returned non-nil result: %v", got)
	}
}

func TestUpdateWorkUnitExecutions(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	createBlankExperiment(ctx, t, storageClient, 123)

	createBlankWorkUnit(ctx, t, storageClient, 123, 1)
	createBlankWorkUnit(ctx, t, storageClient, 123, 2)

	initialExperiment, err := storageClient.FetchExperiment(ctx, 123)
	if err != nil {
		t.Fatalf("FetchExperiment(123) failed: %v", err)
	}
	if childStateCountInState(t, initialExperiment, essmsgpb.WorkUnitStatus_INITIALIZING) != 2 {
		t.Errorf("Initial experiment state count for INITIALIZING is %d, want 2", childStateCountInState(t, initialExperiment, essmsgpb.WorkUnitStatus_INITIALIZING))
	}

	updates := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
		{
			WID: 2,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Metadata: &essmsgpb.WorkUnitExecutionMetadata{
					ResourceName: "new-resource-name",
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.resource_name"}},
		},
	}

	_, err = storageClient.UpdateWorkUnitExecutions(ctx, 123, updates)
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions failed: %v", err)
	}

	gotWu1, err := storageClient.FetchWorkUnit(ctx, 123, 1)
	log.Infof("gotWu1: %v", gotWu1)
	if err != nil {
		t.Fatalf("After UpdateWorkUnitExecutions, FetchWorkUnit(1) failed: %v", err)
	}
	if gotWu1.CurrentWorkUnitExecution.Status.State != essmsgpb.WorkUnitStatus_COMPLETED {
		t.Errorf("After UpdateWorkUnitExecutions, work unit 1 status state is %v, want %v", gotWu1.CurrentWorkUnitExecution.Status.State, essmsgpb.WorkUnitStatus_COMPLETED)
	}
	if gotWu1.CurrentWorkUnitExecution.UpdateTime.AsTime().Equal(gotWu1.CurrentWorkUnitExecution.CreateTime.AsTime()) {
		t.Errorf("After UpdateWorkUnitExecutions, work unit 1 UpdateTime was not updated")
	}
	if gotWu1.CurrentWorkUnitExecution.Status.UpdateTime.AsTime().Equal(gotWu1.CurrentWorkUnitExecution.CreateTime.AsTime()) {
		t.Errorf("After UpdateWorkUnitExecutions, work unit 1 Status.UpdateTime was not updated")
	}

	gotWu2, err := storageClient.FetchWorkUnit(ctx, 123, 2)
	log.Infof("gotWu2: %v", gotWu2)
	if err != nil {
		t.Fatalf("After UpdateWorkUnitExecutions, FetchWorkUnit(2) failed: %v", err)
	}
	if gotWu2.CurrentWorkUnitExecution.Metadata.ResourceName != "new-resource-name" {
		t.Errorf("After UpdateWorkUnitExecutions, work unit 2 metadata resource name is %v, want %v", gotWu2.CurrentWorkUnitExecution.Metadata.ResourceName, "new-resource-name")
	}
	if gotWu2.CurrentWorkUnitExecution.UpdateTime.AsTime().Equal(gotWu2.CurrentWorkUnitExecution.CreateTime.AsTime()) {
		t.Errorf("After UpdateWorkUnitExecutions, work unit 2 UpdateTime was not updated")
	}

	// Check experiment status counts.
	exp, err := storageClient.FetchExperiment(ctx, 123)
	if err != nil {
		t.Fatalf("After UpdateWorkUnitExecutions, FetchExperiment(123) failed: %v", err)
	}
	if childStateCountInState(t, exp, essmsgpb.WorkUnitStatus_INITIALIZING) != 1 {
		t.Errorf("After UpdateWorkUnitExecutions, experiment state count for INITIALIZING is %d, want 1", childStateCountInState(t, exp, essmsgpb.WorkUnitStatus_INITIALIZING))
	}
	if childStateCountInState(t, exp, essmsgpb.WorkUnitStatus_COMPLETED) != 1 {
		t.Errorf("After UpdateWorkUnitExecutions, experiment state count for COMPLETED is %d, want 1", childStateCountInState(t, exp, essmsgpb.WorkUnitStatus_COMPLETED))
	}
}

func TestUpdateWorkUnitExecutions_StateTransitions(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	xid := int64(456)
	createBlankExperiment(ctx, t, storageClient, xid)
	workUnitsToCreate := []struct {
		wid   int64
		state essmsgpb.WorkUnitStatus_WorkUnitState
	}{
		{1, essmsgpb.WorkUnitStatus_INITIALIZING},
		{2, essmsgpb.WorkUnitStatus_RUNNING},
		{3, essmsgpb.WorkUnitStatus_RUNNING},
		{4, essmsgpb.WorkUnitStatus_INITIALIZING},
		{5, essmsgpb.WorkUnitStatus_FAILED},
	}
	for _, wc := range workUnitsToCreate {
		createBlankWorkUnitWithState(ctx, t, storageClient, xid, wc.wid, wc.state)
	}

	initialExperiment, err := storageClient.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperiment(%d) failed: %v", xid, err)
	}
	if got, want := childStateCountInState(t, initialExperiment, essmsgpb.WorkUnitStatus_INITIALIZING), int64(2); got != want {
		t.Errorf("Initial experiment state count for INITIALIZING is %d, want %d", got, want)
	}
	if got, want := childStateCountInState(t, initialExperiment, essmsgpb.WorkUnitStatus_RUNNING), int64(2); got != want {
		t.Errorf("Initial experiment state count for RUNNING is %d, want %d", got, want)
	}
	if got, want := childStateCountInState(t, initialExperiment, essmsgpb.WorkUnitStatus_FAILED), int64(1); got != want {
		t.Errorf("Initial experiment state count for FAILED is %d, want %d", got, want)
	}

	updates := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
		{
			WID: 2,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
		{
			WID: 3,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_FAILED,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
		{
			WID: 4,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_STOPPED,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
		{
			WID: 5,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
	}

	_, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates)
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions failed: %v", err)
	}

	expectedStates := map[int64]essmsgpb.WorkUnitStatus_WorkUnitState{
		1: essmsgpb.WorkUnitStatus_RUNNING,
		2: essmsgpb.WorkUnitStatus_COMPLETED,
		3: essmsgpb.WorkUnitStatus_FAILED,
		4: essmsgpb.WorkUnitStatus_STOPPED,
		5: essmsgpb.WorkUnitStatus_FAILED,
	}
	for wid, expectedState := range expectedStates {
		gotWu, err := storageClient.FetchWorkUnit(ctx, xid, wid)
		if err != nil {
			t.Fatalf("After UpdateWorkUnitExecutions, FetchWorkUnit(%d) failed: %v", wid, err)
		}
		if gotWu.CurrentWorkUnitExecution.Status.State != expectedState {
			t.Errorf("After UpdateWorkUnitExecutions, work unit %d status state is %v, want %v", wid, gotWu.CurrentWorkUnitExecution.Status.State, expectedState)
		}
	}

	exp, err := storageClient.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("After UpdateWorkUnitExecutions, FetchExperiment(%d) failed: %v", xid, err)
	}
	if got, want := childStateCountInState(t, exp, essmsgpb.WorkUnitStatus_INITIALIZING), int64(0); got != want {
		t.Errorf("After UpdateWorkUnitExecutions, experiment state count for INITIALIZING is %d, want %d", got, want)
	}
	if got, want := childStateCountInState(t, exp, essmsgpb.WorkUnitStatus_RUNNING), int64(1); got != want {
		t.Errorf("After UpdateWorkUnitExecutions, experiment state count for RUNNING is %d, want %d", got, want)
	}
	if got, want := childStateCountInState(t, exp, essmsgpb.WorkUnitStatus_COMPLETED), int64(1); got != want {
		t.Errorf("After UpdateWorkUnitExecutions, experiment state count for COMPLETED is %d, want %d", got, want)
	}
	if got, want := childStateCountInState(t, exp, essmsgpb.WorkUnitStatus_FAILED), int64(2); got != want {
		t.Errorf("After UpdateWorkUnitExecutions, experiment state count for FAILED is %d, want %d", got, want)
	}
	if got, want := childStateCountInState(t, exp, essmsgpb.WorkUnitStatus_STOPPED), int64(1); got != want {
		t.Errorf("After UpdateWorkUnitExecutions, experiment state count for STOPPED is %d, want %d", got, want)
	}
}

func TestUpdateWorkUnitExecutions_NoStateChange(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	createBlankExperiment(ctx, t, storageClient, 234)

	createdWu := createBlankWorkUnitWithState(ctx, t, storageClient, 234, 1, essmsgpb.WorkUnitStatus_RUNNING)

	updates := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING, // same state
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
	}

	_, err = storageClient.UpdateWorkUnitExecutions(ctx, 234, updates)
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions failed: %v", err)
	}

	gotWu1, err := storageClient.FetchWorkUnit(ctx, 234, 1)
	if err != nil {
		t.Fatalf("After UpdateWorkUnitExecutions, FetchWorkUnit(1) failed: %v", err)
	}
	// The update time should not change if nothing changed.
	if !gotWu1.CurrentWorkUnitExecution.UpdateTime.AsTime().Equal(createdWu.CurrentWorkUnitExecution.UpdateTime.AsTime()) {
		t.Errorf("After UpdateWorkUnitExecutions, work unit 1 UpdateTime was updated, but it should not have been")
	}
}

func TestUpdateWorkUnitExecutions_InvalidStateTransitions(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	xid := int64(789)
	createBlankExperiment(ctx, t, storageClient, xid)
	tests := []struct {
		wid          int64
		initialState essmsgpb.WorkUnitStatus_WorkUnitState
		updateTo     essmsgpb.WorkUnitStatus_WorkUnitState
		desc         string
	}{
		{1, essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.WorkUnitStatus_INITIALIZING, "RUNNING->INITIALIZING"},
		{2, essmsgpb.WorkUnitStatus_COMPLETED, essmsgpb.WorkUnitStatus_RUNNING, "COMPLETED->RUNNING"},
		{3, essmsgpb.WorkUnitStatus_STOPPED, essmsgpb.WorkUnitStatus_RUNNING, "STOPPED->RUNNING"},
		{4, essmsgpb.WorkUnitStatus_FAILED, essmsgpb.WorkUnitStatus_RUNNING, "FAILED->RUNNING"},
	}

	for _, tc := range tests {
		createBlankWorkUnitWithState(ctx, t, storageClient, xid, tc.wid, tc.initialState)
	}

	// These updates should be ignored because they are invalid transitions.
	var updates []*storage.WorkUnitExecutionUpdate
	for _, tc := range tests {
		updates = append(updates, &storage.WorkUnitExecutionUpdate{
			WID: tc.wid,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           tc.updateTo,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		})
	}

	_, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates)
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions failed: %v", err)
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			gotWu, err := storageClient.FetchWorkUnit(ctx, xid, tc.wid)
			if err != nil {
				t.Fatalf("After UpdateWorkUnitExecutions, FetchWorkUnit(%d) failed: %v", tc.wid, err)
			}
			if got, want := gotWu.CurrentWorkUnitExecution.Status.State, tc.initialState; got != want {
				t.Errorf("FetchWorkUnit(%d) state after UpdateWorkUnitExecutions = %v, want %v", tc.wid, got, want)
			}
		})
	}
}

func TestUpdateWorkUnitExecutions_StaleUpdate(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	xid := int64(890)
	createBlankExperiment(ctx, t, storageClient, xid)
	createBlankWorkUnit(ctx, t, storageClient, xid, 1)

	// Update to RUNNING to set StateUpdateTime
	updates1 := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
	}
	_, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates1)
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions to RUNNING failed: %v", err)
	}

	gotWu, err := storageClient.FetchWorkUnit(ctx, xid, 1)
	if err != nil {
		t.Fatalf("After first UpdateWorkUnitExecutions, FetchWorkUnit(1) failed: %v", err)
	}
	if gotWu.CurrentWorkUnitExecution.Status.State != essmsgpb.WorkUnitStatus_RUNNING {
		t.Fatalf("After first UpdateWorkUnitExecutions, work unit 1 status state is %v, want %v", gotWu.CurrentWorkUnitExecution.Status.State, essmsgpb.WorkUnitStatus_RUNNING)
	}
	if gotWu.CurrentWorkUnitExecution.Status.StateUpdateTime == nil {
		t.Fatalf("After first UpdateWorkUnitExecutions, work unit 1 StateUpdateTime is nil, want non-nil")
	}

	// Attempt a stale update to COMPLETED with old StateUpdateTime.
	staleTime := gotWu.CurrentWorkUnitExecution.Status.StateUpdateTime.AsTime().Add(-time.Minute)
	updates2 := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: timestamppb.New(staleTime),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
	}
	_, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates2)
	if err != nil {
		t.Fatalf("Stale UpdateWorkUnitExecutions failed: %v", err)
	}

	// Verify state is still RUNNING.
	gotWu, err = storageClient.FetchWorkUnit(ctx, xid, 1)
	if err != nil {
		t.Fatalf("After stale UpdateWorkUnitExecutions, FetchWorkUnit(1) failed: %v", err)
	}
	if gotWu.CurrentWorkUnitExecution.Status.State != essmsgpb.WorkUnitStatus_RUNNING {
		t.Errorf("After stale UpdateWorkUnitExecutions, work unit 1 status state is %v, want %v because of stale update", gotWu.CurrentWorkUnitExecution.Status.State, essmsgpb.WorkUnitStatus_RUNNING)
	}
}

func TestUpdateWorkUnitExecutions_StructuredMessageUpdate(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	tests := []struct {
		name       string
		path       string
		setMessage func(*essmsgpb.WorkUnitStatus, *essmsgpb.StructuredMessage)
		getMessage func(*essmsgpb.WorkUnitStatus) *essmsgpb.StructuredMessage
	}{
		{
			name: "SystemMessage",
			path: "status.system_message",
			setMessage: func(s *essmsgpb.WorkUnitStatus, m *essmsgpb.StructuredMessage) {
				s.SystemMessage = m
			},
			getMessage: func(s *essmsgpb.WorkUnitStatus) *essmsgpb.StructuredMessage {
				return s.SystemMessage
			},
		},
		{
			name: "UserMessage",
			path: "status.user_message",
			setMessage: func(s *essmsgpb.WorkUnitStatus, m *essmsgpb.StructuredMessage) {
				s.UserMessage = m
			},
			getMessage: func(s *essmsgpb.WorkUnitStatus) *essmsgpb.StructuredMessage {
				return s.UserMessage
			},
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			xid := int64(101 + i)
			createBlankExperiment(ctx, t, storageClient, xid)
			createBlankWorkUnit(ctx, t, storageClient, xid, 1)

			// Set initial message to ERROR
			initialMsg := &essmsgpb.StructuredMessage{
				UserFacingText: "Initial error message",
				Severity:       essmsgpb.StructuredMessage_ERROR,
				CreateTime:     timestamppb.New(time.Now()),
			}
			status := &essmsgpb.WorkUnitStatus{}
			tc.setMessage(status, initialMsg)
			updates1 := []*storage.WorkUnitExecutionUpdate{
				{
					WID: 1,
					EID: 1,
					WorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Status: status,
					},
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{tc.path}},
				},
			}
			if _, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates1); err != nil {
				t.Fatalf("UpdateWorkUnitExecutions failed setting initial message: %v", err)
			}

			// Attempt to update with INFO message without force - should be ignored
			infoMsgTime := time.Now().Add(time.Second)
			infoMsg := &essmsgpb.StructuredMessage{
				UserFacingText: "Info message - should be ignored",
				Severity:       essmsgpb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(infoMsgTime),
			}
			status2 := &essmsgpb.WorkUnitStatus{}
			tc.setMessage(status2, infoMsg)
			updates2 := []*storage.WorkUnitExecutionUpdate{
				{
					WID: 1,
					EID: 1,
					WorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Status: status2,
					},
					UpdateMask:               &fieldmaskpb.FieldMask{Paths: []string{tc.path}},
					ForceStatusMessageUpdate: false,
				},
			}
			if _, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates2); err != nil {
				t.Fatalf("UpdateWorkUnitExecutions failed for non-forced update: %v", err)
			}
			wu, err := storageClient.FetchWorkUnit(ctx, xid, 1)
			if err != nil {
				t.Fatalf("After non-forced UpdateWorkUnitExecutions, FetchWorkUnit(1) failed: %v", err)
			}
			if got, want := tc.getMessage(wu.CurrentWorkUnitExecution.Status).GetUserFacingText(), "Initial error message"; got != want {
				t.Errorf("After non-forced UpdateWorkUnitExecutions, status message updated to %q, want %q", got, want)
			}

			// Attempt to update with INFO message with force - should succeed
			forcedInfoMsgTime := time.Now().Add(2 * time.Second)
			forcedInfoMsg := &essmsgpb.StructuredMessage{
				UserFacingText: "Forced info message - should succeed",
				Severity:       essmsgpb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(forcedInfoMsgTime),
			}
			status3 := &essmsgpb.WorkUnitStatus{}
			tc.setMessage(status3, forcedInfoMsg)
			updates3 := []*storage.WorkUnitExecutionUpdate{
				{
					WID: 1,
					EID: 1,
					WorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Status: status3,
					},
					UpdateMask:               &fieldmaskpb.FieldMask{Paths: []string{tc.path}},
					ForceStatusMessageUpdate: true,
				},
			}
			if _, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates3); err != nil {
				t.Fatalf("UpdateWorkUnitExecutions failed for forced update: %v", err)
			}
			wu, err = storageClient.FetchWorkUnit(ctx, xid, 1)
			if err != nil {
				t.Fatalf("After forced UpdateWorkUnitExecutions, FetchWorkUnit(1) failed: %v", err)
			}
			if got, want := tc.getMessage(wu.CurrentWorkUnitExecution.Status).GetUserFacingText(), "Forced info message - should succeed"; got != want {
				t.Errorf("After forced UpdateWorkUnitExecutions, status message is %q, want %q", got, want)
			}
		})
	}
}

func TestUpdateWorkUnitExecutions_StatusMessageUpdate_DeferToExistingMessage(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	tests := []struct {
		name              string
		xid               int64
		initialMsgTime    time.Time
		updateMsgTime     time.Time
		updateMsgText     string
		deferToExisting   bool
		wantMsgText       string
		wantUpdateApplied bool
	}{
		{
			name:              "Older update with DeferToExistingMessage=true",
			xid:               1011,
			initialMsgTime:    time.Now(),
			updateMsgTime:     time.Now().Add(-time.Second),
			updateMsgText:     "Older info message - should not overwrite",
			deferToExisting:   true,
			wantMsgText:       "Initial info message",
			wantUpdateApplied: false,
		},
		{
			name:              "Newer update with DeferToExistingMessage=true",
			xid:               1012,
			initialMsgTime:    time.Now(),
			updateMsgTime:     time.Now().Add(time.Second),
			updateMsgText:     "Newer info message - should not overwrite due to defer",
			deferToExisting:   true,
			wantMsgText:       "Initial info message",
			wantUpdateApplied: false,
		},
		{
			name:              "Newer update with DeferToExistingMessage=false",
			xid:               1013,
			initialMsgTime:    time.Now(),
			updateMsgTime:     time.Now().Add(time.Second),
			updateMsgText:     "Newer info message - should overwrite",
			deferToExisting:   false,
			wantMsgText:       "Newer info message - should overwrite",
			wantUpdateApplied: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			createBlankExperiment(ctx, t, storageClient, tc.xid)
			createBlankWorkUnit(ctx, t, storageClient, tc.xid, 1)

			// Set initial message to INFO
			initialMsg := &essmsgpb.StructuredMessage{
				UserFacingText: "Initial info message",
				Severity:       essmsgpb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(tc.initialMsgTime),
			}
			updates1 := []*storage.WorkUnitExecutionUpdate{
				{
					WID: 1,
					EID: 1,
					WorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Status: &essmsgpb.WorkUnitStatus{SystemMessage: initialMsg},
					},
					UpdateMask:               &fieldmaskpb.FieldMask{Paths: []string{"status.system_message"}},
					ForceStatusMessageUpdate: true,
				},
			}
			if _, err = storageClient.UpdateWorkUnitExecutions(ctx, tc.xid, updates1); err != nil {
				t.Fatalf("UpdateWorkUnitExecutions failed setting initial message: %v", err)
			}

			// Attempt the test update.
			updateMsg := &essmsgpb.StructuredMessage{
				UserFacingText: tc.updateMsgText,
				Severity:       essmsgpb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(tc.updateMsgTime),
			}
			updates2 := []*storage.WorkUnitExecutionUpdate{
				{
					WID: 1,
					EID: 1,
					WorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Status: &essmsgpb.WorkUnitStatus{SystemMessage: updateMsg},
					},
					UpdateMask:             &fieldmaskpb.FieldMask{Paths: []string{"status.system_message"}},
					DeferToExistingMessage: tc.deferToExisting,
				},
			}
			if _, err = storageClient.UpdateWorkUnitExecutions(ctx, tc.xid, updates2); err != nil {
				t.Fatalf("UpdateWorkUnitExecutions failed for test update: %v", err)
			}

			wu, err := storageClient.FetchWorkUnit(ctx, tc.xid, 1)
			if err != nil {
				t.Fatalf("After test UpdateWorkUnitExecutions, FetchWorkUnit(1) failed: %v", err)
			}
			if got, want := wu.CurrentWorkUnitExecution.Status.SystemMessage.GetUserFacingText(), tc.wantMsgText; got != want {
				t.Errorf("UpdateWorkUnitExecutions: status message got %q, want %q", got, want)
			}
		})
	}
}

func TestUpdateWorkUnitExecutions_NonExistentWorkUnit(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	createBlankExperiment(ctx, t, storageClient, 345)
	updates := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 999, // non-existent
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
	}

	_, err = storageClient.UpdateWorkUnitExecutions(ctx, 345, updates)
	if err == nil {
		t.Errorf("UpdateWorkUnitExecutions for non-existent work unit succeeded, want error")
	}
}

func TestUpdateWorkUnitExecutions_ExperimentStatusMessageUpdate(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	xid := int64(102)
	createBlankExperiment(ctx, t, storageClient, xid)
	createBlankWorkUnit(ctx, t, storageClient, xid, 1)
	createBlankWorkUnit(ctx, t, storageClient, xid, 2)

	initialExperiment, err := storageClient.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperiment(%d) failed: %v", xid, err)
	}
	if got, want := messageSeverityCountInState(t, initialExperiment, essmsgpb.StructuredMessage_INFO), int64(2); got != want {
		t.Errorf("Initial experiment message severity count for INFO is %d, want %d", got, want)
	}

	msg1 := &essmsgpb.StructuredMessage{
		UserFacingText: "work unit 1 message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		CreateTime:     timestamppb.New(time.Now()),
	}
	msg2 := &essmsgpb.StructuredMessage{
		UserFacingText: "work unit 2 message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		CreateTime:     timestamppb.New(time.Now()),
	}

	// 1. Update WU 1 with msg1.
	updates1 := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{SystemMessage: msg1},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.system_message"}},
		},
	}
	if _, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates1); err != nil {
		t.Fatalf("UpdateWorkUnitExecutions failed for updates1: %v", err)
	}

	wu1, err := storageClient.FetchWorkUnit(ctx, xid, 1)
	if err != nil {
		t.Fatalf("After updates1, FetchWorkUnit(1) failed: %v", err)
	}
	if got, want := wu1.CurrentWorkUnitExecution.Status.SystemMessage.GetUserFacingText(), "work unit 1 message"; got != want {
		t.Errorf("After updates1, work unit 1 status message is %q, want %q", got, want)
	}
	if got, want := wu1.CurrentWorkUnitExecution.Status.SystemMessage.GetParent(), "experiments/102/workUnits/1"; got != want {
		t.Errorf("After updates1, work unit 1 status message parent is %q, want %q", got, want)
	}

	exp1, err := storageClient.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperiment(%d) failed: %v", xid, err)
	}
	if got, want := messageSeverityCountInState(t, exp1, essmsgpb.StructuredMessage_INFO), int64(2); got != want {
		t.Errorf("After updates1, experiment message severity count for INFO is %d, want %d", got, want)
	}

	// 2. Update WU 2 with msg2.
	updates2 := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 2,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{SystemMessage: msg2},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.system_message"}},
		},
	}
	if _, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates2); err != nil {
		t.Fatalf("UpdateWorkUnitExecutions failed for updates2: %v", err)
	}

	wu2, err := storageClient.FetchWorkUnit(ctx, xid, 2)
	if err != nil {
		t.Fatalf("After updates2, FetchWorkUnit(2) failed: %v", err)
	}
	if got, want := wu2.CurrentWorkUnitExecution.Status.SystemMessage.GetUserFacingText(), "work unit 2 message"; got != want {
		t.Errorf("After updates2, work unit 2 status message is %q, want %q", got, want)
	}
	if got, want := wu2.CurrentWorkUnitExecution.Status.SystemMessage.GetParent(), "experiments/102/workUnits/2"; got != want {
		t.Errorf("After updates2, work unit 2 status message parent is %q, want %q", got, want)
	}

	exp2, err := storageClient.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperiment(%d) failed: %v", xid, err)
	}
	if got, want := messageSeverityCountInState(t, exp2, essmsgpb.StructuredMessage_INFO), int64(2); got != want {
		t.Errorf("After updates2, experiment message severity count for INFO is %d, want %d", got, want)
	}
}

func TestUpdateWorkUnitExecutions_CompletionTime(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	xid := int64(678)
	createBlankExperiment(ctx, t, storageClient, xid)
	createBlankWorkUnitWithState(ctx, t, storageClient, xid, 1, essmsgpb.WorkUnitStatus_INITIALIZING)
	createBlankWorkUnitWithState(ctx, t, storageClient, xid, 2, essmsgpb.WorkUnitStatus_RUNNING)

	exp, err := storageClient.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperiment(%d) failed: %v", xid, err)
	}
	if exp.CompleteTime != nil {
		t.Errorf("Experiment CompleteTime is not nil, want nil")
	}

	wu1, err := storageClient.FetchWorkUnit(ctx, xid, 1)
	if err != nil {
		t.Fatalf("FetchWorkUnit(1) failed: %v", err)
	}
	if wu1.CurrentWorkUnitExecution.CompleteTime != nil {
		t.Errorf("Work unit 1 CompleteTime is not nil, want nil")
	}

	wu2, err := storageClient.FetchWorkUnit(ctx, xid, 2)
	if err != nil {
		t.Fatalf("FetchWorkUnit(2) failed: %v", err)
	}
	if wu2.CurrentWorkUnitExecution.CompleteTime != nil {
		t.Errorf("Work unit 2 CompleteTime is not nil, want nil")
	}

	// Transition WU1 to COMPLETED.
	updates1 := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
	}
	_, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates1)
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions failed for updates1: %v", err)
	}

	wu1, err = storageClient.FetchWorkUnit(ctx, xid, 1)
	if err != nil {
		t.Fatalf("After UpdateWorkUnitExecutions(1), FetchWorkUnit(1) failed: %v", err)
	}
	if wu1.CurrentWorkUnitExecution.CompleteTime == nil {
		t.Errorf("After UpdateWorkUnitExecutions(1), work unit 1 CompleteTime is nil, want not nil")
	}
	if !wu1.CurrentWorkUnitExecution.CompleteTime.AsTime().After(wu1.CurrentWorkUnitExecution.CreateTime.AsTime()) {
		t.Errorf("After UpdateWorkUnitExecutions(1), work unit 1 CompleteTime was not updated")
	}

	exp, err = storageClient.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("After UpdateWorkUnitExecutions(1), FetchExperiment(%d) failed: %v", xid, err)
	}
	if exp.CompleteTime != nil {
		t.Errorf("After UpdateWorkUnitExecutions(1), experiment CompleteTime is not nil, want nil")
	}

	// Transition WU2 to FAILED.
	updates2 := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 2,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_FAILED,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
	}
	_, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates2)
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions failed for updates2: %v", err)
	}

	wu2, err = storageClient.FetchWorkUnit(ctx, xid, 2)
	if err != nil {
		t.Fatalf("After UpdateWorkUnitExecutions(2), FetchWorkUnit(2) failed: %v", err)
	}
	if wu2.CurrentWorkUnitExecution.CompleteTime == nil {
		t.Errorf("After UpdateWorkUnitExecutions(2), work unit 2 CompleteTime is nil, want not nil")
	}
	if !wu2.CurrentWorkUnitExecution.CompleteTime.AsTime().After(wu2.CurrentWorkUnitExecution.CreateTime.AsTime()) {
		t.Errorf("After UpdateWorkUnitExecutions(2), work unit 2 CompleteTime was not updated")
	}

	exp, err = storageClient.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("After UpdateWorkUnitExecutions(2), FetchExperiment(%d) failed: %v", xid, err)
	}
	if exp.CompleteTime == nil {
		t.Errorf("After UpdateWorkUnitExecutions(2), experiment CompleteTime is nil, want not nil")
	}
	if !exp.CompleteTime.AsTime().After(exp.CreateTime.AsTime()) {
		t.Errorf("After UpdateWorkUnitExecutions(2), experiment CompleteTime was not updated")
	}
}

func TestUpdateWorkUnitExecutions_ReturnValue(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	const xid = 124
	createBlankExperiment(ctx, t, storageClient, xid)

	createBlankWorkUnit(ctx, t, storageClient, xid, 1)
	createBlankWorkUnit(ctx, t, storageClient, xid, 2)

	updates := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
		{
			WID: 2,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Metadata: &essmsgpb.WorkUnitExecutionMetadata{
					ResourceName: "new-resource-name",
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.resource_name"}},
		},
	}

	updatedWUs, err := storageClient.UpdateWorkUnitExecutions(ctx, xid, updates)
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions failed: %v", err)
	}

	if len(updatedWUs) != 2 {
		t.Errorf("UpdateWorkUnitExecutions returned %d work units, want 2", len(updatedWUs))
	}

	if got, want := updatedWUs[1].GetStatus().GetState(), essmsgpb.WorkUnitStatus_COMPLETED; got != want {
		t.Errorf("UpdateWorkUnitExecutions returned work unit 1 with state %v, want %v", got, want)
	}
	if got, want := updatedWUs[2].GetMetadata().GetResourceName(), "new-resource-name"; got != want {
		t.Errorf("UpdateWorkUnitExecutions returned work unit 2 with metadata resource name %v, want %v", got, want)
	}
}

func TestUpdateWorkUnitExecutions_MultipleUpdatesPerWorkUnit(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	const xid = 125
	createBlankExperiment(ctx, t, storageClient, xid)
	createBlankWorkUnit(ctx, t, storageClient, xid, 1)

	updates := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Metadata: &essmsgpb.WorkUnitExecutionMetadata{
					ResourceName: "new-resource-name-125",
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.resource_name"}},
		},
	}

	updatedWUs, err := storageClient.UpdateWorkUnitExecutions(ctx, xid, updates)
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions failed: %v", err)
	}

	if len(updatedWUs) != 1 {
		t.Errorf("UpdateWorkUnitExecutions returned %d work units, want 1", len(updatedWUs))
	}

	// Check return value.
	if got, want := updatedWUs[1].GetStatus().GetState(), essmsgpb.WorkUnitStatus_RUNNING; got != want {
		t.Errorf("Returned work unit 1 has state %v, want %v", got, want)
	}
	if got, want := updatedWUs[1].GetMetadata().GetResourceName(), "new-resource-name-125"; got != want {
		t.Errorf("Returned work unit 1 has metadata resource name %v, want %v", got, want)
	}

	// Check persisted value.
	gotWU1, err := storageClient.FetchWorkUnit(ctx, xid, 1)
	if err != nil {
		t.Fatalf("After UpdateWorkUnitExecutions, FetchWorkUnit(1) failed: %v", err)
	}
	if got, want := gotWU1.CurrentWorkUnitExecution.Status.State, essmsgpb.WorkUnitStatus_RUNNING; got != want {
		t.Errorf("Persisted work unit 1 has state %v, want %v", got, want)
	}
	if got, want := gotWU1.CurrentWorkUnitExecution.Metadata.ResourceName, "new-resource-name-125"; got != want {
		t.Errorf("Persisted work unit 1 has metadata resource name %q, want %q", got, want)
	}
}

func TestUpdateWorkUnitExecutions_MultipleUpdatesSameField(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	const xid = 126
	createBlankExperiment(ctx, t, storageClient, xid)

	tOutdated := time.Now()
	tLatest := tOutdated.Add(time.Second)

	tests := []struct {
		name    string
		wid     int64
		updates []*storage.WorkUnitExecutionUpdate
	}{
		{
			name: "outdated_then_latest",
			wid:  1,
			updates: []*storage.WorkUnitExecutionUpdate{
				{
					WID: 1,
					EID: 1,
					WorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Status: &essmsgpb.WorkUnitStatus{
							State:           essmsgpb.WorkUnitStatus_RUNNING,
							StateUpdateTime: timestamppb.New(tOutdated),
						},
					},
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				},
				{
					WID: 1,
					EID: 1,
					WorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Status: &essmsgpb.WorkUnitStatus{
							State:           essmsgpb.WorkUnitStatus_COMPLETED,
							StateUpdateTime: timestamppb.New(tLatest),
						},
					},
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				},
			},
		},
		{
			name: "latest_then_outdated",
			wid:  2,
			updates: []*storage.WorkUnitExecutionUpdate{
				{
					WID: 2,
					EID: 1,
					WorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Status: &essmsgpb.WorkUnitStatus{
							State:           essmsgpb.WorkUnitStatus_COMPLETED,
							StateUpdateTime: timestamppb.New(tLatest),
						},
					},
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				},
				{
					WID: 2,
					EID: 1,
					WorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Status: &essmsgpb.WorkUnitStatus{
							State:           essmsgpb.WorkUnitStatus_RUNNING,
							StateUpdateTime: timestamppb.New(tOutdated),
						},
					},
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			createBlankWorkUnit(ctx, t, storageClient, xid, tc.wid)

			updatedWUs, err := storageClient.UpdateWorkUnitExecutions(ctx, xid, tc.updates)
			if err != nil {
				t.Fatalf("UpdateWorkUnitExecutions failed: %v", err)
			}

			if len(updatedWUs) != 1 {
				t.Errorf("UpdateWorkUnitExecutions returned %d work units, want 1", len(updatedWUs))
			}

			// Check return value - should reflect the state from the update with the latest timestamp.
			if got, want := updatedWUs[tc.wid].GetStatus().GetState(), essmsgpb.WorkUnitStatus_COMPLETED; got != want {
				t.Errorf("Returned work unit %d has state %v, want %v", tc.wid, got, want)
			}

			// Check persisted value.
			gotWU, err := storageClient.FetchWorkUnit(ctx, xid, tc.wid)
			if err != nil {
				t.Fatalf("After UpdateWorkUnitExecutions, FetchWorkUnit(%d) failed: %v", tc.wid, err)
			}
			if got, want := gotWU.CurrentWorkUnitExecution.Status.State, essmsgpb.WorkUnitStatus_COMPLETED; got != want {
				t.Errorf("Persisted work unit %d has state %v, want %v", tc.wid, got, want)
			}
		})
	}
}

func TestUpdateWorkUnitExecutions_MismatchedEID(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	const xid = 127
	createBlankExperiment(ctx, t, storageClient, xid)
	createdWu := createBlankWorkUnitWithState(ctx, t, storageClient, xid, 1, essmsgpb.WorkUnitStatus_RUNNING)

	updates := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 1,
			EID: 2, // Mismatched EID, current is 1.
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: timestamppb.New(time.Now()),
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		},
	}

	_, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates)
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions failed: %v", err)
	}

	gotWu, err := storageClient.FetchWorkUnit(ctx, xid, 1)
	if err != nil {
		t.Fatalf("After UpdateWorkUnitExecutions, FetchWorkUnit(1) failed: %v", err)
	}
	// The work unit should not be updated because of mismatched EID.
	if got, want := gotWu.CurrentWorkUnitExecution.Status.State, essmsgpb.WorkUnitStatus_RUNNING; got != want {
		t.Errorf("After UpdateWorkUnitExecutions, work unit 1 state is %v, want %v", got, want)
	}
	if !gotWu.CurrentWorkUnitExecution.UpdateTime.AsTime().Equal(createdWu.CurrentWorkUnitExecution.UpdateTime.AsTime()) {
		t.Errorf("After UpdateWorkUnitExecutions, work unit 1 UpdateTime was updated, but it should not have been")
	}
}

func TestUpdateWorkUnitExecutions_ExperimentStatusMessagePropagation(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	type updateSpec struct {
		wid       int64
		text      string
		severity  essmsgpb.StructuredMessage_Severity
		force     bool
		offset    time.Duration
		isUserMsg bool
	}

	u1Warn := updateSpec{wid: 1, text: "warning message 1", severity: essmsgpb.StructuredMessage_WARNING, force: false, offset: 1 * time.Millisecond}
	u2Err := updateSpec{wid: 2, text: "error short", severity: essmsgpb.StructuredMessage_ERROR, force: false, offset: 2 * time.Millisecond}
	u3Err := updateSpec{wid: 3, text: "error long message", severity: essmsgpb.StructuredMessage_ERROR, force: false, offset: 3 * time.Millisecond}
	u4Fatal := updateSpec{wid: 4, text: "fatal message", severity: essmsgpb.StructuredMessage_FATAL, force: false, offset: 4 * time.Millisecond}
	u4Info := updateSpec{wid: 4, text: "info message", severity: essmsgpb.StructuredMessage_INFO, force: true, offset: 5 * time.Millisecond}
	u2Info := updateSpec{wid: 2, text: "info message", severity: essmsgpb.StructuredMessage_INFO, force: true, offset: 6 * time.Millisecond}
	u3Info := updateSpec{wid: 3, text: "info message", severity: essmsgpb.StructuredMessage_INFO, force: true, offset: 6 * time.Millisecond}
	u1Info := updateSpec{wid: 1, text: "info message", severity: essmsgpb.StructuredMessage_INFO, force: true, offset: 7 * time.Millisecond}
	u1UserErr := updateSpec{wid: 1, text: "user error", severity: essmsgpb.StructuredMessage_ERROR, force: false, offset: 8 * time.Millisecond, isUserMsg: true}

	tests := []struct {
		name         string
		setupUpdates []updateSpec
		testUpdates  []updateSpec
		wantMsgText  string
		wantNil      bool
	}{
		{
			name:         "WU1 to WARNING -> Exp message should be WU1 message",
			setupUpdates: nil,
			testUpdates:  []updateSpec{u1Warn},
			wantMsgText:  "warning message 1",
		},
		{
			name:         "WU2 to ERROR (short) -> Exp message should be WU2 message",
			setupUpdates: []updateSpec{u1Warn},
			testUpdates:  []updateSpec{u2Err},
			wantMsgText:  "error short",
		},
		{
			name:         "WU3 to ERROR (long) -> Exp message should be WU3 message",
			setupUpdates: []updateSpec{u1Warn, u2Err},
			testUpdates:  []updateSpec{u3Err},
			wantMsgText:  "error long message",
		},
		{
			name:         "WU4 to FATAL -> Exp message should be WU4 message",
			setupUpdates: []updateSpec{u1Warn, u2Err, u3Err},
			testUpdates:  []updateSpec{u4Fatal},
			wantMsgText:  "fatal message",
		},
		{
			name:         "WU4 to INFO -> Exp message should be WU3 message (long error)",
			setupUpdates: []updateSpec{u1Warn, u2Err, u3Err, u4Fatal},
			testUpdates:  []updateSpec{u4Info},
			wantMsgText:  "error long message",
		},
		{
			name:         "WU2 and WU3 to INFO -> Exp message should be WU1 message (warning)",
			setupUpdates: []updateSpec{u1Warn, u2Err, u3Err, u4Fatal, u4Info},
			testUpdates: []updateSpec{
				u2Info,
				u3Info,
			},
			wantMsgText: "warning message 1",
		},
		{
			name:         "WU1 to INFO -> Exp message should be nil",
			setupUpdates: []updateSpec{u1Warn, u2Err, u3Err, u4Fatal, u4Info, u2Info, u3Info},
			testUpdates:  []updateSpec{u1Info},
			wantNil:      true,
		},
		{
			name:         "User message with stack trace takes precedence over system message",
			setupUpdates: []updateSpec{u1Warn, u2Err},
			testUpdates:  []updateSpec{u1UserErr},
			wantMsgText:  "user error",
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			xid := int64(1000 + i)
			createBlankExperiment(ctx, t, storageClient, xid)
			for j := int64(1); j <= 4; j++ {
				createBlankWorkUnit(ctx, t, storageClient, xid, j)
			}

			baseTime := time.Now().Add(time.Hour)
			makeUpdate := func(spec updateSpec) *storage.WorkUnitExecutionUpdate {
				msg := &essmsgpb.StructuredMessage{
					UserFacingText: spec.text,
					Severity:       spec.severity,
					CreateTime:     timestamppb.New(baseTime.Add(spec.offset)),
				}
				status := &essmsgpb.WorkUnitStatus{}
				path := "status.system_message"
				if spec.isUserMsg {
					msg.AuxiliaryInfo = &essmsgpb.AuxiliaryInfo{
						Detail: &essmsgpb.AuxiliaryInfo_StackTrace{
							StackTrace: "test-stack-trace",
						},
					}
					status.UserMessage = msg
					path = "status.user_message"
				} else {
					status.SystemMessage = msg
				}
				return &storage.WorkUnitExecutionUpdate{
					WID: spec.wid,
					EID: 1,
					WorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Status: status,
					},
					UpdateMask:               &fieldmaskpb.FieldMask{Paths: []string{path}},
					ForceStatusMessageUpdate: spec.force,
				}
			}

			// Apply setup updates
			for _, uSpec := range tc.setupUpdates {
				u := makeUpdate(uSpec)
				if _, err := storageClient.UpdateWorkUnitExecutions(ctx, xid, []*storage.WorkUnitExecutionUpdate{u}); err != nil {
					t.Fatalf("Setup UpdateWorkUnitExecutions failed: %v", err)
				}
			}

			var testUpdates []*storage.WorkUnitExecutionUpdate
			for _, uSpec := range tc.testUpdates {
				testUpdates = append(testUpdates, makeUpdate(uSpec))
			}

			if _, err := storageClient.UpdateWorkUnitExecutions(ctx, xid, testUpdates); err != nil {
				t.Fatalf("UpdateWorkUnitExecutions failed: %v", err)
			}
			exp, err := storageClient.FetchExperiment(ctx, xid)
			if err != nil {
				t.Fatalf("FetchExperiment(%d) failed: %v", xid, err)
			}
			if tc.wantNil {
				if exp.GetStatus().GetStructuredStatusMessage() != nil {
					t.Errorf("Experiment status message should be nil, got %v", exp.GetStatus().GetStructuredStatusMessage())
				}
				return
			}

			if got, want := exp.GetStatus().GetStructuredStatusMessage().GetUserFacingText(), tc.wantMsgText; got != want {
				t.Errorf("Experiment status message is %q, want %q", got, want)
			}
			wantStackTrace := ""
			if tc.wantMsgText == "user error" {
				wantStackTrace = "test-stack-trace"
			}
			if got, want := exp.GetStatus().GetStructuredStatusMessage().GetAuxiliaryInfo().GetStackTrace(), wantStackTrace; got != want {
				t.Errorf("Experiment status message stack trace is %q, want %q", got, want)
			}
		})
	}
}

func TestUpdateWorkUnitExecutions_OptimizationLogic(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	type updateSpec struct {
		wid      int64
		text     string
		severity essmsgpb.StructuredMessage_Severity
		force    bool
		offset   time.Duration
	}

	u1Err := updateSpec{wid: 1, text: "wu1-error", severity: essmsgpb.StructuredMessage_ERROR, force: true, offset: 1 * time.Millisecond}
	u2Info := updateSpec{wid: 2, text: "wu2-info", severity: essmsgpb.StructuredMessage_INFO, force: true, offset: 2 * time.Millisecond}
	u2Fatal := updateSpec{wid: 2, text: "wu2-fatal", severity: essmsgpb.StructuredMessage_FATAL, force: true, offset: 3 * time.Millisecond}
	u3Err := updateSpec{wid: 3, text: "wu3-error", severity: essmsgpb.StructuredMessage_ERROR, force: true, offset: 4 * time.Millisecond}
	u2InfoNew := updateSpec{wid: 2, text: "wu2-info-new", severity: essmsgpb.StructuredMessage_INFO, force: true, offset: 5 * time.Millisecond}
	u1Fatal := updateSpec{wid: 1, text: "wu1-fatal", severity: essmsgpb.StructuredMessage_FATAL, force: true, offset: 6 * time.Millisecond}
	u1FatalLater := updateSpec{wid: 1, text: "wu1-fatal", severity: essmsgpb.StructuredMessage_FATAL, force: true, offset: 7 * time.Millisecond}

	tests := []struct {
		name                   string
		setupUpdates           []updateSpec
		testUpdates            []updateSpec
		wantMsgText            string
		wantAlternativeMsgText string
	}{
		{
			name:         "Case 1: Initialize WU1 with ERROR -> Result: WU1",
			setupUpdates: nil,
			testUpdates:  []updateSpec{u1Err},
			wantMsgText:  "wu1-error",
		},
		{
			name:         "Case 2: Update WU2 with INFO (Worse) -> Result: WU1 (retain crown)",
			setupUpdates: []updateSpec{u1Err},
			testUpdates:  []updateSpec{u2Info},
			wantMsgText:  "wu1-error",
		},
		{
			name:         "Case 3: Update WU2 with FATAL (Better) -> Result: WU2 (take crown)",
			setupUpdates: []updateSpec{u1Err, u2Info},
			testUpdates:  []updateSpec{u2Fatal},
			wantMsgText:  "wu2-fatal",
		},
		{
			name:         "Case 4 Setup: Set WU3 to ERROR -> Result: WU2 (still champion)",
			setupUpdates: []updateSpec{u1Err, u2Info, u2Fatal},
			testUpdates:  []updateSpec{u3Err},
			wantMsgText:  "wu2-fatal",
		},
		{
			name:                   "Case 4: Dethrone WU2 (FATAL -> INFO) -> Result: WU1 or WU3 (ERROR)",
			setupUpdates:           []updateSpec{u1Err, u2Info, u2Fatal, u3Err},
			testUpdates:            []updateSpec{u2InfoNew},
			wantMsgText:            "wu1-error",
			wantAlternativeMsgText: "wu3-error",
		},
		{
			name:         "Case 5: Update WU1 to FATAL -> Result: WU1",
			setupUpdates: []updateSpec{u1Err, u2Info, u2Fatal, u3Err, u2InfoNew},
			testUpdates:  []updateSpec{u1Fatal},
			wantMsgText:  "wu1-fatal",
		},
		{
			name:         "Case 6: Update champion (WU1) again (timestamp update) -> Result: WU1",
			setupUpdates: []updateSpec{u1Err, u2Info, u2Fatal, u3Err, u2InfoNew, u1Fatal},
			testUpdates:  []updateSpec{u1FatalLater},
			wantMsgText:  "wu1-fatal",
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			xid := int64(2000 + i)
			createBlankExperiment(ctx, t, storageClient, xid)
			createBlankWorkUnit(ctx, t, storageClient, xid, 1) // Potential Champion
			createBlankWorkUnit(ctx, t, storageClient, xid, 2) // Challenger
			createBlankWorkUnit(ctx, t, storageClient, xid, 3) // Bystander (Runner up)

			baseTime := time.Now().Add(time.Hour)
			makeUpdate := func(spec updateSpec) *storage.WorkUnitExecutionUpdate {
				return &storage.WorkUnitExecutionUpdate{
					WID: spec.wid,
					EID: 1,
					WorkUnitExecution: &essmsgpb.WorkUnitExecution{
						Status: &essmsgpb.WorkUnitStatus{
							SystemMessage: &essmsgpb.StructuredMessage{
								UserFacingText: spec.text,
								Severity:       spec.severity,
								CreateTime:     timestamppb.New(baseTime.Add(spec.offset)),
							},
						},
					},
					UpdateMask:               &fieldmaskpb.FieldMask{Paths: []string{"status.system_message"}},
					ForceStatusMessageUpdate: spec.force,
				}
			}

			// Apply setup updates
			for _, uSpec := range tc.setupUpdates {
				u := makeUpdate(uSpec)
				if _, err := storageClient.UpdateWorkUnitExecutions(ctx, xid, []*storage.WorkUnitExecutionUpdate{u}); err != nil {
					t.Fatalf("Setup UpdateWorkUnitExecutions failed: %v", err)
				}
			}

			var testUpdates []*storage.WorkUnitExecutionUpdate
			for _, uSpec := range tc.testUpdates {
				testUpdates = append(testUpdates, makeUpdate(uSpec))
			}

			if _, err := storageClient.UpdateWorkUnitExecutions(ctx, xid, testUpdates); err != nil {
				t.Fatalf("UpdateWorkUnitExecutions failed: %v", err)
			}
			exp, err := storageClient.FetchExperiment(ctx, xid)
			if err != nil {
				t.Fatalf("FetchExperiment failed: %v", err)
			}
			got := exp.Status.StructuredStatusMessage.GetUserFacingText()
			if got != tc.wantMsgText {
				if tc.wantAlternativeMsgText != "" && got == tc.wantAlternativeMsgText {
					return
				}
				t.Errorf("Experiment status message got %s, want %s", got, tc.wantMsgText)
			}
		})
	}
}

func TestUpdateWorkUnitExecutions_SavesLatestMessage(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	xid := int64(9999)
	createBlankExperiment(ctx, t, storageClient, xid)

	// Create work unit with NO status message.
	wuProto := blankWorkUnitProto(xid, 1)
	wuProto.CurrentWorkUnitExecution.Status.SystemMessage = nil
	createAndStoreWorkUnit(ctx, t, storageClient, wuProto)

	tOld := time.Now().Add(-2 * time.Hour)
	tNew := tOld.Add(time.Minute)

	updates := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					SystemMessage: &essmsgpb.StructuredMessage{
						UserFacingText: "Old Message",
						Severity:       essmsgpb.StructuredMessage_INFO,
						CreateTime:     timestamppb.New(tOld),
					},
				},
			},
			UpdateMask:               &fieldmaskpb.FieldMask{Paths: []string{"status.system_message"}},
			ForceStatusMessageUpdate: false,
		},
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					SystemMessage: &essmsgpb.StructuredMessage{
						UserFacingText: "New Message",
						Severity:       essmsgpb.StructuredMessage_INFO,
						CreateTime:     timestamppb.New(tNew),
					},
				},
			},
			UpdateMask:               &fieldmaskpb.FieldMask{Paths: []string{"status.system_message"}},
			ForceStatusMessageUpdate: false,
		},
	}

	_, err = storageClient.UpdateWorkUnitExecutions(ctx, xid, updates)
	if err != nil {
		t.Fatalf("UpdateWorkUnitExecutions failed: %v", err)
	}

	wu, err := storageClient.FetchWorkUnit(ctx, xid, 1)
	if err != nil {
		t.Fatalf("FetchWorkUnit(1) failed: %v", err)
	}

	gotMsg := wu.CurrentWorkUnitExecution.Status.SystemMessage
	if gotMsg == nil {
		t.Fatal("SystemMessage is nil")
	}

	if gotMsg.GetUserFacingText() != "New Message" {
		t.Errorf("Message UserFacingText = %q, want %q", gotMsg.GetUserFacingText(), "New Message")
	}
}
