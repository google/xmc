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

package workunit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/workunit"
)

func TestWithDefaultParent(t *testing.T) {
	wid := int64(7)
	zeroWID := int64(0)

	tests := []struct {
		name string
		xid  int64
		wid  *int64
		msg  *essmsgpb.StructuredMessage
		want *essmsgpb.StructuredMessage
	}{
		{
			name: "nil message",
			xid:  123,
			wid:  &wid,
			msg:  nil,
			want: nil,
		},
		{
			name: "existing parent is kept",
			xid:  123,
			wid:  &wid,
			msg:  &essmsgpb.StructuredMessage{Parent: "experiments/1", UserFacingText: "msg"},
			want: &essmsgpb.StructuredMessage{Parent: "experiments/1", UserFacingText: "msg"},
		},
		{
			name: "non-positive xid leaves parent empty",
			xid:  0,
			wid:  &wid,
			msg:  &essmsgpb.StructuredMessage{UserFacingText: "msg"},
			want: &essmsgpb.StructuredMessage{UserFacingText: "msg"},
		},
		{
			name: "work unit parent",
			xid:  123,
			wid:  &wid,
			msg:  &essmsgpb.StructuredMessage{UserFacingText: "msg"},
			want: &essmsgpb.StructuredMessage{Parent: "experiments/123/workUnits/7", UserFacingText: "msg"},
		},
		{
			name: "experiment parent when wid is nil",
			xid:  123,
			wid:  nil,
			msg:  &essmsgpb.StructuredMessage{UserFacingText: "msg"},
			want: &essmsgpb.StructuredMessage{Parent: "experiments/123", UserFacingText: "msg"},
		},
		{
			name: "experiment parent when wid is zero",
			xid:  123,
			wid:  &zeroWID,
			msg:  &essmsgpb.StructuredMessage{UserFacingText: "msg"},
			want: &essmsgpb.StructuredMessage{Parent: "experiments/123", UserFacingText: "msg"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := proto.Clone(tc.msg)

			got := workunit.WithDefaultParent(tc.xid, tc.wid, tc.msg)

			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("WithDefaultParent() mismatch (-want +got):\n%s", diff)
			}
			if !proto.Equal(tc.msg, before) {
				t.Errorf("WithDefaultParent() mutated its input message")
			}
		})
	}
}

func TestStatusMessageChanged(t *testing.T) {
	baseMsg := &essmsgpb.StructuredMessage{
		Parent:         "experiments/123",
		UserFacingText: "running",
		Severity:       essmsgpb.StructuredMessage_INFO,
		CreateTime:     &timestamppb.Timestamp{Seconds: 100},
	}

	tests := []struct {
		name   string
		oldMsg *essmsgpb.StructuredMessage
		newMsg *essmsgpb.StructuredMessage
		want   bool
	}{
		{
			name:   "nil new message",
			oldMsg: baseMsg,
			newMsg: nil,
			want:   false,
		},
		{
			name:   "both nil",
			oldMsg: nil,
			newMsg: nil,
			want:   false,
		},
		{
			name:   "nil old message",
			oldMsg: nil,
			newMsg: baseMsg,
			want:   true,
		},
		{
			name:   "identical messages",
			oldMsg: baseMsg,
			newMsg: proto.Clone(baseMsg).(*essmsgpb.StructuredMessage),
			want:   false,
		},
		{
			name:   "only CreateTime differs",
			oldMsg: baseMsg,
			newMsg: &essmsgpb.StructuredMessage{
				Parent:         "experiments/123",
				UserFacingText: "running",
				Severity:       essmsgpb.StructuredMessage_INFO,
				CreateTime:     &timestamppb.Timestamp{Seconds: 200},
			},
			want: false,
		},
		{
			name:   "UserFacingText differs",
			oldMsg: baseMsg,
			newMsg: &essmsgpb.StructuredMessage{
				Parent:         "experiments/123",
				UserFacingText: "failed",
				Severity:       essmsgpb.StructuredMessage_INFO,
				CreateTime:     &timestamppb.Timestamp{Seconds: 100},
			},
			want: true,
		},
		{
			name:   "Severity differs",
			oldMsg: baseMsg,
			newMsg: &essmsgpb.StructuredMessage{
				Parent:         "experiments/123",
				UserFacingText: "running",
				Severity:       essmsgpb.StructuredMessage_ERROR,
				CreateTime:     &timestamppb.Timestamp{Seconds: 100},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldBefore := proto.Clone(tc.oldMsg)
			newBefore := proto.Clone(tc.newMsg)

			if got := workunit.StatusMessageChanged(tc.oldMsg, tc.newMsg); got != tc.want {
				t.Errorf("StatusMessageChanged() = %v, want %v", got, tc.want)
			}

			if !proto.Equal(tc.oldMsg, oldBefore) {
				t.Errorf("StatusMessageChanged() mutated oldMsg")
			}
			if !proto.Equal(tc.newMsg, newBefore) {
				t.Errorf("StatusMessageChanged() mutated newMsg")
			}
		})
	}
}

func TestPrepareStatusMessageHistory(t *testing.T) {
	const xid int64 = 123
	wid := int64(7)

	tests := []struct {
		name        string
		wid         *int64
		oldMsg      *essmsgpb.StructuredMessage
		newMsg      *essmsgpb.StructuredMessage
		wantOld     *essmsgpb.StructuredMessage
		wantNew     *essmsgpb.StructuredMessage
		wantChanged bool
	}{
		{
			name:        "nil new message is not recorded",
			wid:         &wid,
			oldMsg:      &essmsgpb.StructuredMessage{UserFacingText: "old"},
			newMsg:      nil,
			wantChanged: false,
		},
		{
			name:        "messages differing only by default parent are not recorded",
			wid:         &wid,
			oldMsg:      &essmsgpb.StructuredMessage{Parent: "experiments/123/workUnits/7", UserFacingText: "running"},
			newMsg:      &essmsgpb.StructuredMessage{UserFacingText: "running"},
			wantChanged: false,
		},
		{
			name:        "changed work unit messages get default parents",
			wid:         &wid,
			oldMsg:      &essmsgpb.StructuredMessage{UserFacingText: "old"},
			newMsg:      &essmsgpb.StructuredMessage{UserFacingText: "new"},
			wantOld:     &essmsgpb.StructuredMessage{Parent: "experiments/123/workUnits/7", UserFacingText: "old"},
			wantNew:     &essmsgpb.StructuredMessage{Parent: "experiments/123/workUnits/7", UserFacingText: "new"},
			wantChanged: true,
		},
		{
			name:        "first experiment message gets experiment parent",
			wid:         nil,
			oldMsg:      nil,
			newMsg:      &essmsgpb.StructuredMessage{UserFacingText: "new"},
			wantOld:     nil,
			wantNew:     &essmsgpb.StructuredMessage{Parent: "experiments/123", UserFacingText: "new"},
			wantChanged: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotOld, gotNew, gotChanged := workunit.PrepareStatusMessageHistory(xid, tc.wid, tc.oldMsg, tc.newMsg)

			if gotChanged != tc.wantChanged {
				t.Errorf("PrepareStatusMessageHistory() changed = %v, want %v", gotChanged, tc.wantChanged)
			}
			if diff := cmp.Diff(tc.wantOld, gotOld, protocmp.Transform()); diff != "" {
				t.Errorf("PrepareStatusMessageHistory() old message mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantNew, gotNew, protocmp.Transform()); diff != "" {
				t.Errorf("PrepareStatusMessageHistory() new message mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStateChanged(t *testing.T) {
	initializing := essmsgpb.WorkUnitStatus_INITIALIZING
	running := essmsgpb.WorkUnitStatus_RUNNING

	tests := []struct {
		name    string
		current *essmsgpb.WorkUnitStatus_WorkUnitState
		next    *essmsgpb.WorkUnitStatus_WorkUnitState
		want    bool
	}{
		{name: "nil new state", current: &initializing, next: nil, want: false},
		{name: "same state", current: &running, next: &running, want: false},
		{name: "nil current state", current: nil, next: &initializing, want: true},
		{name: "different state", current: &initializing, next: &running, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := workunit.StateChanged(tc.current, tc.next); got != tc.want {
				t.Errorf("StateChanged() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPrepareStatusHistory(t *testing.T) {
	initializing := essmsgpb.WorkUnitStatus_INITIALIZING
	running := essmsgpb.WorkUnitStatus_RUNNING
	fixedTime := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		current       *essmsgpb.WorkUnitStatus_WorkUnitState
		next          *essmsgpb.WorkUnitStatus_WorkUnitState
		resources     map[string]int64
		timestamp     time.Time
		wantResources map[string]int64
		wantTimestamp time.Time
		wantChanged   bool
	}{
		{
			name:          "unchanged state is not recorded",
			current:       &running,
			next:          &running,
			resources:     map[string]int64{"CPU": 4},
			timestamp:     fixedTime,
			wantResources: nil,
			wantTimestamp: time.Time{},
			wantChanged:   false,
		},
		{
			name:          "transition keeps resources and timestamp",
			current:       &initializing,
			next:          &running,
			resources:     map[string]int64{"CPU": 8, "GPU": 2},
			timestamp:     fixedTime,
			wantResources: map[string]int64{"CPU": 8, "GPU": 2},
			wantTimestamp: fixedTime,
			wantChanged:   true,
		},
		{
			name:          "empty resources default to a single empty resource",
			current:       nil,
			next:          &initializing,
			resources:     nil,
			timestamp:     fixedTime,
			wantResources: map[string]int64{"": 0},
			wantTimestamp: fixedTime,
			wantChanged:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotResources, gotTimestamp, gotChanged := workunit.PrepareStatusHistory(tc.current, tc.next, tc.resources, tc.timestamp)

			if gotChanged != tc.wantChanged {
				t.Errorf("PrepareStatusHistory() changed = %v, want %v", gotChanged, tc.wantChanged)
			}
			if diff := cmp.Diff(tc.wantResources, gotResources); diff != "" {
				t.Errorf("PrepareStatusHistory() resources mismatch (-want +got):\n%s", diff)
			}
			if !gotTimestamp.Equal(tc.wantTimestamp) {
				t.Errorf("PrepareStatusHistory() timestamp = %v, want %v", gotTimestamp, tc.wantTimestamp)
			}
		})
	}
}

func TestPrepareStatusHistory_ZeroTimestampDefaultsToNow(t *testing.T) {
	initializing := essmsgpb.WorkUnitStatus_INITIALIZING

	before := time.Now()
	_, gotTimestamp, gotChanged := workunit.PrepareStatusHistory(nil, &initializing, nil, time.Time{})
	after := time.Now()

	if !gotChanged {
		t.Fatalf("PrepareStatusHistory() changed = false, want true")
	}
	if gotTimestamp.Before(before) || gotTimestamp.After(after) {
		t.Errorf("PrepareStatusHistory() timestamp = %v, want between %v and %v", gotTimestamp, before, after)
	}
}

func TestRecordExecutionHistory_RecordsBothMessagesAndStatusHistory(t *testing.T) {
	ctx := context.Background()
	statusMsgRepo := &storage.MockStatusMessageTxRepository{}
	statusHistRepo := &storage.MockStatusHistoryTxRepository{}
	tx := &storage.Transaction{
		StatusMessages: statusMsgRepo,
		StatusHistory:  statusHistRepo,
	}

	oldSysMsg := &essmsgpb.StructuredMessage{UserFacingText: "old-sys"}
	oldUserMsg := &essmsgpb.StructuredMessage{UserFacingText: "old-user"}
	newSysMsg := &essmsgpb.StructuredMessage{UserFacingText: "new-sys"}
	newUserMsg := &essmsgpb.StructuredMessage{UserFacingText: "new-user"}
	updateTime := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)

	oldExec := &essmsgpb.WorkUnitExecution{
		Status: &essmsgpb.WorkUnitStatus{
			State:         essmsgpb.WorkUnitStatus_INITIALIZING,
			SystemMessage: oldSysMsg,
			UserMessage:   oldUserMsg,
		},
	}
	newExec := &essmsgpb.WorkUnitExecution{
		Status: &essmsgpb.WorkUnitStatus{
			State:           essmsgpb.WorkUnitStatus_RUNNING,
			UpdateTime:      timestamppb.New(updateTime),
			StateUpdateTime: timestamppb.New(updateTime.Add(-time.Hour)),
			SystemMessage:   newSysMsg,
			UserMessage:     newUserMsg,
		},
	}
	executable := &essmsgpb.Executable{
		SchedulingConstraints: &essmsgpb.SchedulingConstraints{
			QueueName: "queue-a",
		},
	}

	if err := workunit.RecordExecutionHistory(ctx, tx, 100, 200, 3, executable, oldExec, newExec); err != nil {
		t.Fatalf("RecordExecutionHistory() unexpected error: %v", err)
	}

	if len(statusMsgRepo.Calls) != 2 {
		t.Fatalf("StatusMessages calls = %d, want 2", len(statusMsgRepo.Calls))
	}
	if statusMsgRepo.Calls[0].OldMsg.GetUserFacingText() != "old-sys" || statusMsgRepo.Calls[0].NewMsg.GetUserFacingText() != "new-sys" {
		t.Errorf("first status message call = (%v, %v), want (old-sys, new-sys)", statusMsgRepo.Calls[0].OldMsg, statusMsgRepo.Calls[0].NewMsg)
	}
	if statusMsgRepo.Calls[0].NewMsg.GetParent() != "experiments/100/workUnits/200" {
		t.Errorf("first status message call NewMsg.Parent = %q, want %q", statusMsgRepo.Calls[0].NewMsg.GetParent(), "experiments/100/workUnits/200")
	}
	if statusMsgRepo.Calls[1].OldMsg.GetUserFacingText() != "old-user" || statusMsgRepo.Calls[1].NewMsg.GetUserFacingText() != "new-user" {
		t.Errorf("second status message call = (%v, %v), want (old-user, new-user)", statusMsgRepo.Calls[1].OldMsg, statusMsgRepo.Calls[1].NewMsg)
	}
	if statusMsgRepo.Calls[1].NewMsg.GetParent() != "experiments/100/workUnits/200" {
		t.Errorf("second status message call NewMsg.Parent = %q, want %q", statusMsgRepo.Calls[1].NewMsg.GetParent(), "experiments/100/workUnits/200")
	}

	if len(statusHistRepo.Calls) != 1 {
		t.Fatalf("StatusHistory calls = %d, want 1", len(statusHistRepo.Calls))
	}
	histCall := statusHistRepo.Calls[0]
	if histCall.XID != 100 || histCall.WID != 200 || histCall.EID == nil || *histCall.EID != 3 {
		t.Errorf("StatusHistory IDs = (%d, %d, %v), want (100, 200, 3)", histCall.XID, histCall.WID, histCall.EID)
	}
	if histCall.QueueName != "queue-a" {
		t.Errorf("StatusHistory QueueName = %q, want %q", histCall.QueueName, "queue-a")
	}
	if histCall.PrevState == nil || *histCall.PrevState != essmsgpb.WorkUnitStatus_INITIALIZING {
		t.Errorf("StatusHistory PrevState = %v, want INITIALIZING", histCall.PrevState)
	}
	if histCall.NewState == nil || *histCall.NewState != essmsgpb.WorkUnitStatus_RUNNING {
		t.Errorf("StatusHistory NewState = %v, want RUNNING", histCall.NewState)
	}
	if !histCall.Timestamp.Equal(updateTime) {
		t.Errorf("StatusHistory Timestamp = %v, want %v", histCall.Timestamp, updateTime)
	}
}

func TestRecordExecutionHistory_SkipsUnchangedMessagesAndState(t *testing.T) {
	ctx := context.Background()
	statusMsgRepo := &storage.MockStatusMessageTxRepository{}
	statusHistRepo := &storage.MockStatusHistoryTxRepository{}
	tx := &storage.Transaction{
		StatusMessages: statusMsgRepo,
		StatusHistory:  statusHistRepo,
	}

	exec := &essmsgpb.WorkUnitExecution{
		Status: &essmsgpb.WorkUnitStatus{
			State:         essmsgpb.WorkUnitStatus_RUNNING,
			SystemMessage: &essmsgpb.StructuredMessage{UserFacingText: "same-sys"},
			UserMessage:   &essmsgpb.StructuredMessage{UserFacingText: "same-user"},
		},
	}

	if err := workunit.RecordExecutionHistory(ctx, tx, 100, 200, 3, nil, exec, exec); err != nil {
		t.Fatalf("RecordExecutionHistory() unexpected error: %v", err)
	}

	if len(statusMsgRepo.Calls) != 0 {
		t.Errorf("StatusMessages calls = %d, want 0 when messages are unchanged", len(statusMsgRepo.Calls))
	}
	if len(statusHistRepo.Calls) != 0 {
		t.Errorf("StatusHistory calls = %d, want 0 when state is unchanged", len(statusHistRepo.Calls))
	}
}

func TestRecordExecutionHistory_NilOldExecution(t *testing.T) {
	ctx := context.Background()
	statusMsgRepo := &storage.MockStatusMessageTxRepository{}
	statusHistRepo := &storage.MockStatusHistoryTxRepository{}
	tx := &storage.Transaction{
		StatusMessages: statusMsgRepo,
		StatusHistory:  statusHistRepo,
	}

	newExec := &essmsgpb.WorkUnitExecution{
		Status: &essmsgpb.WorkUnitStatus{
			State:         essmsgpb.WorkUnitStatus_INITIALIZING,
			SystemMessage: &essmsgpb.StructuredMessage{UserFacingText: "created"},
		},
	}

	if err := workunit.RecordExecutionHistory(ctx, tx, 1, 2, 1, nil, nil, newExec); err != nil {
		t.Fatalf("RecordExecutionHistory() unexpected error: %v", err)
	}

	if len(statusMsgRepo.Calls) != 1 {
		t.Fatalf("StatusMessages calls = %d, want 1 (only non-nil system message)", len(statusMsgRepo.Calls))
	}
	if statusMsgRepo.Calls[0].OldMsg != nil {
		t.Errorf("OldMsg = %v, want nil", statusMsgRepo.Calls[0].OldMsg)
	}
	if len(statusHistRepo.Calls) != 1 {
		t.Fatalf("StatusHistory calls = %d, want 1", len(statusHistRepo.Calls))
	}
	if statusHistRepo.Calls[0].PrevState != nil {
		t.Errorf("PrevState = %v, want nil", statusHistRepo.Calls[0].PrevState)
	}
}

func TestRecordExecutionHistory_PropagatesErrors(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("storage failure")

	newExec := &essmsgpb.WorkUnitExecution{
		Status: &essmsgpb.WorkUnitStatus{
			State:         essmsgpb.WorkUnitStatus_RUNNING,
			SystemMessage: &essmsgpb.StructuredMessage{UserFacingText: "sys"},
			UserMessage:   &essmsgpb.StructuredMessage{UserFacingText: "user"},
		},
	}

	t.Run("system status message error", func(t *testing.T) {
		tx := &storage.Transaction{
			StatusMessages: &storage.MockStatusMessageTxRepository{SaveErr: wantErr},
			StatusHistory:  &storage.MockStatusHistoryTxRepository{},
		}
		err := workunit.RecordExecutionHistory(ctx, tx, 1, 2, 1, nil, nil, newExec)
		if !errors.Is(err, wantErr) {
			t.Errorf("RecordExecutionHistory() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("user status message error", func(t *testing.T) {
		calls := 0
		tx := &storage.Transaction{
			StatusMessages: &storage.MockStatusMessageTxRepository{
				SaveFunc: func(context.Context, int64, *int64, *int64, *essmsgpb.StructuredMessage, *essmsgpb.StructuredMessage) error {
					calls++
					if calls == 2 {
						return wantErr
					}
					return nil
				},
			},
			StatusHistory: &storage.MockStatusHistoryTxRepository{},
		}
		err := workunit.RecordExecutionHistory(ctx, tx, 1, 2, 1, nil, nil, newExec)
		if !errors.Is(err, wantErr) {
			t.Errorf("RecordExecutionHistory() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("status history error", func(t *testing.T) {
		tx := &storage.Transaction{
			StatusMessages: &storage.MockStatusMessageTxRepository{},
			StatusHistory:  &storage.MockStatusHistoryTxRepository{SaveErr: wantErr},
		}
		err := workunit.RecordExecutionHistory(ctx, tx, 1, 2, 1, nil, nil, newExec)
		if !errors.Is(err, wantErr) {
			t.Errorf("RecordExecutionHistory() error = %v, want %v", err, wantErr)
		}
	})
}
