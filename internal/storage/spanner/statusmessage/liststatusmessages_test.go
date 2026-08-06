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
package statusmessage_test

import (
	"context"
	"testing"
	"time"

	cloudspanner "golang/cloud_google_com/go/spanner/v/v1/spanner"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
	"xmanager_cloud/storage/spanner/emulator/emulator"
)

func TestListStatusMessages(t *testing.T) {
	ctx := t.Context()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	const xid int64 = 123
	spanner.CreateBlankExperiment(ctx, t, storage, xid)

	// Insert a status message
	msg := &essmsgpb.StructuredMessage{
		UserFacingText: "Test experiment message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
	}

	insertStatusMessage(ctx, t, storage, essClient, xid, nil, msg)

	// Insert another message with a work unit
	workUnitMsg := &essmsgpb.StructuredMessage{
		UserFacingText: "Test work unit message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123/workUnits/456",
	}

	wid := int64(456)
	insertStatusMessage(ctx, t, storage, essClient, xid, &wid, workUnitMsg)

	// Verify that we can list status messages for an experiment
	gotMessages, gotPageToken, err := storage.ListStatusMessages(ctx, "experiments/123", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListStatusMessages failed: %v", err)
	}

	if len(gotMessages) != 2 {
		t.Errorf("ListStatusMessages returned %d messages, want 2", len(gotMessages))
	}

	// Verify that we can list status messages for a work unit
	gotWorkUnitMessages, _, err := storage.ListStatusMessages(ctx, "experiments/123/workUnits/456", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListStatusMessages for work unit failed: %v", err)
	}

	if len(gotWorkUnitMessages) != 1 {
		t.Fatalf("ListStatusMessages for work unit returned %d messages, want 1", len(gotWorkUnitMessages))
	}

	if gotWorkUnitMessages[0].GetContent().GetUserFacingText() != workUnitMsg.UserFacingText {
		t.Errorf("ListStatusMessages for work unit returned user facing text %q, want %q", gotWorkUnitMessages[0].GetContent().GetUserFacingText(), workUnitMsg.UserFacingText)
	}

	if gotPageToken != "" {
		t.Errorf("ListStatusMessages returned non-empty page token: got %q, want empty", gotPageToken)
	}
}

func TestListStatusMessages_Filtering(t *testing.T) {
	ctx := t.Context()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	const xid int64 = 123
	spanner.CreateBlankExperiment(ctx, t, storage, xid)

	insertStatusMessage(ctx, t, storage, essClient, xid, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Message One",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
	})
	insertStatusMessage(ctx, t, storage, essClient, xid, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Message Two",
		Severity:       essmsgpb.StructuredMessage_ERROR,
		Source:         essmsgpb.StructuredMessage_ADAPTER,
		Parent:         "experiments/123",
	})

	// Filter by source
	{
		msgs, _, err := storage.ListStatusMessages(ctx, "experiments/123", 10, "", `source = "XMANAGER_BACKEND"`, "", 0)
		if err != nil {
			t.Fatalf("ListStatusMessages with source filter failed: %v", err)
		}
		if len(msgs) != 1 {
			t.Errorf("ListStatusMessages with source filter returned %d messages, want 1", len(msgs))
		} else if msgs[0].GetContent().GetUserFacingText() != "Message One" {
			t.Errorf("ListStatusMessages with source filter returned %q, want %q", msgs[0].GetContent().GetUserFacingText(), "Message One")
		}
	}

	// Filter by user_facing_text
	{
		msgs, _, err := storage.ListStatusMessages(ctx, "experiments/123", 10, "", `user_facing_text = "Message One"`, "", 0)
		if err != nil {
			t.Fatalf("ListStatusMessages with text filter failed: %v", err)
		}
		if len(msgs) != 1 {
			t.Errorf("ListStatusMessages with text filter returned %d messages, want 1", len(msgs))
		}
	}
}

func TestListStatusMessages_Ordering(t *testing.T) {
	ctx := t.Context()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	const xid int64 = 123
	spanner.CreateBlankExperiment(ctx, t, storage, xid)

	now := time.Now()
	insertStatusMessage(ctx, t, storage, essClient, xid, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Older Message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
		CreateTime:     timestamppb.New(now.Add(-1 * time.Hour)),
	})

	insertStatusMessage(ctx, t, storage, essClient, xid, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Newer Message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
		CreateTime:     timestamppb.New(now),
	})

	// Default ordering (desc)
	{
		msgs, _, err := storage.ListStatusMessages(ctx, "experiments/123", 10, "", "", "", 0)
		if err != nil {
			t.Fatalf("ListStatusMessages failed: %v", err)
		}
		if len(msgs) != 2 {
			t.Fatalf("ListStatusMessages returned %d messages, want 2", len(msgs))
		}
		if msgs[0].GetContent().GetUserFacingText() != "Newer Message" {
			t.Errorf("Default order (desc) failed: got %q, want %q", msgs[0].GetContent().GetUserFacingText(), "Newer Message")
		}
	}

	// Ascending ordering
	{
		msgs, _, err := storage.ListStatusMessages(ctx, "experiments/123", 10, "", "", "create_time asc", 0)
		if err != nil {
			t.Fatalf("ListStatusMessages failed: %v", err)
		}
		if len(msgs) != 2 {
			t.Fatalf("ListStatusMessages returned %d messages, want 2", len(msgs))
		}
		if msgs[0].GetContent().GetUserFacingText() != "Older Message" {
			t.Errorf("Ascending order failed: got %q, want %q", msgs[0].GetContent().GetUserFacingText(), "Older Message")
		}
	}
}

func TestListStatusMessages_Pagination(t *testing.T) {
	ctx := t.Context()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	const xid int64 = 123
	spanner.CreateBlankExperiment(ctx, t, storage, xid)

	now := time.Now()
	insertStatusMessage(ctx, t, storage, essClient, xid, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Message One",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
		CreateTime:     timestamppb.New(now.Add(-1 * time.Hour)),
	})

	insertStatusMessage(ctx, t, storage, essClient, xid, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Message Two",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
		CreateTime:     timestamppb.New(now),
	})

	// Page 1
	var firstPageToken string
	{
		msgs, token, err := storage.ListStatusMessages(ctx, "experiments/123", 1, "", "", "", 0)
		if err != nil {
			t.Fatalf("ListStatusMessages failed: %v", err)
		}
		if len(msgs) != 1 {
			t.Fatalf("ListStatusMessages page 1 returned %d messages, want 1", len(msgs))
		}
		if msgs[0].GetContent().GetUserFacingText() != "Message Two" {
			t.Errorf("Page 1 returned %q, want %q", msgs[0].GetContent().GetUserFacingText(), "Message Two")
		}
		if token == "" {
			t.Errorf("Page 1 returned empty token")
		}
		firstPageToken = token
	}

	// Page 2
	{
		msgs, token, err := storage.ListStatusMessages(ctx, "experiments/123", 1, firstPageToken, "", "", 0)
		if err != nil {
			t.Fatalf("ListStatusMessages failed: %v", err)
		}
		if len(msgs) != 1 {
			t.Fatalf("ListStatusMessages page 2 returned %d messages, want 1", len(msgs))
		}
		if msgs[0].GetContent().GetUserFacingText() != "Message One" {
			t.Errorf("Page 2 returned %q, want %q", msgs[0].GetContent().GetUserFacingText(), "Message One")
		}
		if token != "" {
			t.Errorf("Page 2 returned non-empty token: %q", token)
		}
	}
}

func TestPagination_Experiment(t *testing.T) {
	ctx := t.Context()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	const xid int64 = 123
	spanner.CreateBlankExperiment(ctx, t, storage, xid)

	now := time.Now()
	insertStatusMessage(ctx, t, storage, essClient, xid, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Message One",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
		CreateTime:     timestamppb.New(now),
	})

	insertStatusMessage(ctx, t, storage, essClient, xid, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Message Two",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
		CreateTime:     timestamppb.New(now),
	})

	// Page 1
	var firstPageToken string
	{
		msgs, token, err := storage.ListStatusMessages(ctx, "experiments/123", 1, "", "", "", 0)
		if err != nil {
			t.Fatalf("ListStatusMessages failed: %v", err)
		}
		if len(msgs) != 1 {
			t.Fatalf("ListStatusMessages page 1 returned %d messages, want 1", len(msgs))
		}
		firstPageToken = token
	}

	// Page 2
	{
		msgs, _, err := storage.ListStatusMessages(ctx, "experiments/123", 1, firstPageToken, "", "", 0)
		if err != nil {
			t.Fatalf("ListStatusMessages failed: %v", err)
		}
		if len(msgs) != 1 {
			t.Fatalf("ListStatusMessages page 2 returned %d messages, want 1", len(msgs))
		}
	}
}

func TestListStatusMessages_EmptyParent(t *testing.T) {
	ctx := t.Context()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	// List with empty parent should fail
	_, _, err := storage.ListStatusMessages(ctx, "", 10, "", "", "", 0)
	if err == nil {
		t.Errorf("ListStatusMessages with empty parent succeeded, want error")
	}
}

func insertStatusMessage(ctx context.Context, t *testing.T, storage *spanner.Storage, essClient *cloudspanner.Client, xid int64, wid *int64, msg *essmsgpb.StructuredMessage) {
	t.Helper()
	_, err := essClient.ReadWriteTransaction(ctx, func(ctx context.Context, txn *cloudspanner.ReadWriteTransaction) error {
		statusmessage.SaveStatusMessageHistory(ctx, xid, wid, nil, nil, msg, txn)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to save status message history: %v", err)
	}
}

func TestListStatusMessages_DefaultOrdering(t *testing.T) {
	ctx := t.Context()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	xid := int64(123)
	spanner.CreateBlankExperiment(ctx, t, storage, xid)

	testTime := time.Date(2026, time.April, 13, 0, 0, 0, 0, time.UTC)

	msgOlder := &essmsgpb.StructuredMessage{
		UserFacingText: "Older Message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
		CreateTime:     timestamppb.New(testTime),
	}
	insertStatusMessage(ctx, t, storage, essClient, xid, nil, msgOlder)

	msgNewer := &essmsgpb.StructuredMessage{
		UserFacingText: "Newer Message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123/workUnits/1",
		CreateTime:     timestamppb.New(testTime.Add(time.Hour)),
	}
	var wid int64 = 1
	insertStatusMessage(ctx, t, storage, essClient, xid, &wid, msgNewer)

	gotMessages, _, err := storage.ListStatusMessages(ctx, "experiments/123", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListStatusMessages failed: %v", err)
	}

	if len(gotMessages) != 2 {
		t.Fatalf("ListStatusMessages returned %d messages, want 2", len(gotMessages))
	}

	// Ensure that the default ordering is by create_time desc when mixing experiment and work unit messages.
	if gotMessages[0].GetContent().GetUserFacingText() != "Newer Message" {
		t.Errorf("Expected first message to be 'Newer Message', got %q. Default ordering might be using work_unit_id first.", gotMessages[0].GetContent().GetUserFacingText())
	}
}
