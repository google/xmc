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

package spanner

import (
	"context"
	"testing"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
	"xmanager_cloud/storage/spanner/emulator/emulator"

	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestSaveStatusMessageHistoryIfChanged_Disabled(t *testing.T) {
	ctx, _, essClient := setupTest(t, false)

	currentMsg := &essmsgpb.StructuredMessage{UserFacingText: "old"}
	newMsg := &essmsgpb.StructuredMessage{UserFacingText: "new"}

	statusmessage.SaveStatusMessageHistoryIfChanged(ctx, false, 123, nil, nil, currentMsg, newMsg, nil)

	messages := listStatusMessagesForTest(ctx, t, essClient, 123)
	if len(messages) > 0 {
		t.Errorf("saveStatusMessageHistoryIfChanged() wrote %d messages when disabled, want 0", len(messages))
	}
}

func TestSaveStatusMessageHistoryIfChanged_NilNewMessage(t *testing.T) {
	ctx, _, essClient := setupTest(t, true)

	currentMsg := &essmsgpb.StructuredMessage{UserFacingText: "old"}

	statusmessage.SaveStatusMessageHistoryIfChanged(ctx, true, 123, nil, nil, currentMsg, nil, nil)

	messages := listStatusMessagesForTest(ctx, t, essClient, 123)
	if len(messages) > 0 {
		t.Errorf("saveStatusMessageHistoryIfChanged() wrote %d messages when new message is nil, want 0", len(messages))
	}
}

func TestSaveStatusMessageHistoryIfChanged_EqualMessages(t *testing.T) {
	ctx, _, essClient := setupTest(t, true)

	currentMsg := &essmsgpb.StructuredMessage{UserFacingText: "same"}
	newMsg := &essmsgpb.StructuredMessage{UserFacingText: "same"}

	statusmessage.SaveStatusMessageHistoryIfChanged(ctx, true, 123, nil, nil, currentMsg, newMsg, nil)

	messages := listStatusMessagesForTest(ctx, t, essClient, 123)
	if len(messages) > 0 {
		t.Errorf("saveStatusMessageHistoryIfChanged() wrote %d messages when messages are equal, want 0", len(messages))
	}
}

func TestSaveStatusMessageHistoryIfChanged_Success(t *testing.T) {
	ctx, storage, essClient := setupTest(t, true)

	const xid int64 = 123
	createBlankExperiment(ctx, t, storage, xid)

	currentMsg := &essmsgpb.StructuredMessage{
		Parent:         "experiments/123",
		UserFacingText: "old",
	}
	newMsg := &essmsgpb.StructuredMessage{
		Parent:         "experiments/123",
		UserFacingText: "new",
	}

	_, err := essClient.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, true, xid, nil, nil, currentMsg, newMsg, txn)
		return nil
	})
	if err != nil {
		t.Fatalf("saveStatusMessageHistoryIfChanged() ReadWriteTransaction failed: %v", err)
	}

	messages := listStatusMessagesForTest(ctx, t, essClient, xid)

	if len(messages) != 1 {
		t.Fatalf("saveStatusMessageHistoryIfChanged() wrote %d rows in StatusMessages, want 1", len(messages))
	}

	if messages[0] != "new" {
		t.Errorf("saveStatusMessageHistoryIfChanged() wrote UserFacingText %q, want %q", messages[0], "new")
	}
}

func TestSaveStatusMessageHistoryIfChanged_EqualMessagesDifferentTimestamp(t *testing.T) {
	ctx, _, essClient := setupTest(t, true)

	currentMsg := &essmsgpb.StructuredMessage{
		UserFacingText: "same",
		CreateTime:     &timestamppb.Timestamp{Seconds: 100},
	}
	newMsg := &essmsgpb.StructuredMessage{
		UserFacingText: "same",
		CreateTime:     &timestamppb.Timestamp{Seconds: 200},
	}

	statusmessage.SaveStatusMessageHistoryIfChanged(ctx, true, 123, nil, nil, currentMsg, newMsg, nil)

	messages := listStatusMessagesForTest(ctx, t, essClient, 123)
	if len(messages) > 0 {
		t.Errorf("saveStatusMessageHistoryIfChanged() wrote %d messages when messages are equal with different timestamps, want 0", len(messages))
	}
}

func setupTest(t *testing.T, historyEnabled bool) (context.Context, *Storage, *spanner.Client) {
	t.Helper()
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorageWithConfig(essClient, StorageConfig{StatusMessageHistoryEnabled: historyEnabled})
	if err != nil {
		t.Fatalf("NewStorageWithConfig() err = %v, want nil", err)
	}
	return ctx, storage, essClient
}

func listStatusMessagesForTest(ctx context.Context, t *testing.T, essClient *spanner.Client, xid int64) []string {
	t.Helper()
	stmt := spanner.Statement{
		SQL:    "SELECT UserFacingText FROM StatusMessages WHERE Xid = @xid",
		Params: map[string]any{"xid": xid},
	}
	iter := essClient.Single().Query(ctx, stmt)
	defer iter.Stop()

	var messages []string
	if err := iter.Do(func(r *spanner.Row) error {
		var text string
		if err := r.ColumnByName("UserFacingText", &text); err != nil {
			return err
		}
		messages = append(messages, text)
		return nil
	}); err != nil {
		t.Fatalf("failed to query status messages: %v", err)
	}
	return messages
}
