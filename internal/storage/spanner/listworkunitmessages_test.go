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
	"fmt"
	"sort"
	"testing"
	"time"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/storage/storage"

	fieldmaskpb "protobuf/field_mask_go_proto"
	timestamppb "protobuf/timestamp_go_proto"
)

func TestListWorkUnitMessagesBySeverity(t *testing.T) {
	ctx := t.Context()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	xid := int64(1)
	createBlankExperiment(ctx, t, storageClient, xid)

	sysInfoMsg1 := &essmsgpb.StructuredMessage{
		UserFacingText: "info 1",
		Severity:       essmsgpb.StructuredMessage_INFO,
	}
	sysWarningMsg1 := &essmsgpb.StructuredMessage{
		UserFacingText: "warning 1",
		Severity:       essmsgpb.StructuredMessage_WARNING,
	}
	sysErrorMsg1 := &essmsgpb.StructuredMessage{
		UserFacingText: "error 1",
		Severity:       essmsgpb.StructuredMessage_ERROR,
	}
	sysInfoMsg2 := &essmsgpb.StructuredMessage{
		UserFacingText: "info 2",
		Severity:       essmsgpb.StructuredMessage_INFO,
	}
	userErrorMsg := &essmsgpb.StructuredMessage{
		UserFacingText: "user error",
		Severity:       essmsgpb.StructuredMessage_ERROR,
	}
	sysErrorMsg2 := &essmsgpb.StructuredMessage{
		UserFacingText: "system error 2",
		Severity:       essmsgpb.StructuredMessage_ERROR,
	}
	userErrorMsg2 := &essmsgpb.StructuredMessage{
		UserFacingText: "longer user error",
		Severity:       essmsgpb.StructuredMessage_ERROR,
	}

	messages := []struct {
		wid    int64
		system *essmsgpb.StructuredMessage
		user   *essmsgpb.StructuredMessage
	}{
		{wid: 1, system: sysInfoMsg1},
		{wid: 2, system: sysWarningMsg1},
		{wid: 3, system: sysErrorMsg1},
		{wid: 4, system: sysInfoMsg2},
		{wid: 6, user: userErrorMsg},
		{wid: 8, system: sysErrorMsg2, user: userErrorMsg2},
	}
	// Add a work unit with only a default status message.
	wu5 := createBlankWorkUnit(ctx, t, storageClient, xid, 5)
	sysInfoMsg5 := wu5.CurrentWorkUnitExecution.Status.SystemMessage

	for _, msg := range messages {
		createBlankWorkUnit(ctx, t, storageClient, xid, msg.wid)

		if msg.system != nil {
			msg.system.Parent = fmt.Sprintf("experiments/%d/workUnits/%d", xid, msg.wid)
			msg.system.CreateTime = timestamppb.New(time.Now())
		}
		if msg.user != nil {
			msg.user.Parent = fmt.Sprintf("experiments/%d/workUnits/%d", xid, msg.wid)
			msg.user.CreateTime = timestamppb.New(time.Now())
		}

		status := &essmsgpb.WorkUnitStatus{
			SystemMessage: msg.system,
			UserMessage:   msg.user,
		}
		updates := []*storage.WorkUnitExecutionUpdate{
			{
				WID: msg.wid,
				EID: 1,
				WorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: status,
				},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.system_message", "status.user_message"}},
			},
		}
		if _, err := storageClient.UpdateWorkUnitExecutions(ctx, xid, updates); err != nil {
			t.Fatalf("UpdateWorkUnitExecutions for work unit %d failed: %v", msg.wid, err)
		}
	}

	// Create another experiment with a work unit to ensure we only list messages for the requested xid.
	xid2 := int64(2)
	createBlankExperiment(ctx, t, storageClient, xid2)
	createBlankWorkUnit(ctx, t, storageClient, xid2, 1)
	sysInfoMsgXID2 := &essmsgpb.StructuredMessage{
		UserFacingText: "info xid2",
		Severity:       essmsgpb.StructuredMessage_INFO,
		CreateTime:     timestamppb.New(time.Now()),
	}
	updatesXID2 := []*storage.WorkUnitExecutionUpdate{
		{
			WID: 1,
			EID: 1,
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{SystemMessage: sysInfoMsgXID2},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.system_message"}},
		},
	}
	if _, err := storageClient.UpdateWorkUnitExecutions(ctx, xid2, updatesXID2); err != nil {
		t.Fatalf("UpdateWorkUnitExecutions for xid %d failed: %v", xid2, err)
	}

	tests := []struct {
		severity essmsgpb.StructuredMessage_Severity
		want     []*essmsgpb.StructuredMessage
	}{
		{essmsgpb.StructuredMessage_INFO, []*essmsgpb.StructuredMessage{sysInfoMsg1, sysInfoMsg2, sysInfoMsg5}},
		{essmsgpb.StructuredMessage_WARNING, []*essmsgpb.StructuredMessage{sysWarningMsg1}},
		{essmsgpb.StructuredMessage_ERROR, []*essmsgpb.StructuredMessage{sysErrorMsg1, userErrorMsg, sysErrorMsg2, userErrorMsg2}},
		{essmsgpb.StructuredMessage_FATAL, nil},
	}

	for _, tc := range tests {
		t.Run(tc.severity.String(), func(t *testing.T) {
			got, err := storageClient.ListWorkUnitMessagesBySeverity(ctx, xid, tc.severity, essClient.Single())
			if err != nil {
				t.Fatalf("listWorkUnitMessagesBySeverity failed: %v", err)
			}

			sort.Slice(tc.want, func(i, j int) bool {
				return tc.want[i].GetUserFacingText() < tc.want[j].GetUserFacingText()
			})
			sort.Slice(got, func(i, j int) bool {
				return got[i].GetUserFacingText() < got[j].GetUserFacingText()
			})
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("listWorkUnitMessagesBySeverity(%s) returned diff (-want +got):\n%s", tc.severity, diff)
			}
		})
	}
}

func TestListWorkUnitMessagesBySeverity_EdgeCases(t *testing.T) {
	ctx := t.Context()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create an experiment with no work units.
	xid1 := int64(10)
	createBlankExperiment(ctx, t, storageClient, xid1)

	// Create an experiment with work units, but no status messages.
	xid2 := int64(11)
	createBlankExperiment(ctx, t, storageClient, xid2)
	wu1 := blankWorkUnitProto(xid2, 1)
	wu1.CurrentWorkUnitExecution.Status.SystemMessage = nil
	createAndStoreWorkUnit(ctx, t, storageClient, wu1)
	wu2 := blankWorkUnitProto(xid2, 2)
	wu2.CurrentWorkUnitExecution.Status.SystemMessage = nil
	createAndStoreWorkUnit(ctx, t, storageClient, wu2)

	tests := []struct {
		name string
		xid  int64
	}{
		{"experimentNoWorkUnits", xid1},
		{"experimentNoMessages", xid2},
		{"nonExistentExperiment", 999},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, severity := range []essmsgpb.StructuredMessage_Severity{
				essmsgpb.StructuredMessage_INFO,
				essmsgpb.StructuredMessage_WARNING,
				essmsgpb.StructuredMessage_ERROR,
				essmsgpb.StructuredMessage_FATAL,
			} {
				got, err := storageClient.ListWorkUnitMessagesBySeverity(ctx, tc.xid, severity, essClient.Single())
				if err != nil {
					t.Errorf("listWorkUnitMessagesBySeverity(%d, %s) failed: %v", tc.xid, severity, err)
				}
				if len(got) != 0 {
					t.Errorf("listWorkUnitMessagesBySeverity(%d, %s) returned %d messages, want 0", tc.xid, severity, len(got))
				}
			}
		})
	}
}
