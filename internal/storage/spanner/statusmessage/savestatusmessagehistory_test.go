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

package statusmessage_test

import (
	"context"
	"errors"
	"testing"

	cloudspanner "golang/cloud_google_com/go/spanner/v/v1/spanner"
	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
	"xmanager_cloud/storage/spanner/emulator/emulator"
)

func TestSaveStatusMessageHistoryIfChanged_NilNewMessage(t *testing.T) {
	fix := setupTest(t)
	ctx, essClient := fix.ctx, fix.essClient

	currentMsg := &essmsgpb.StructuredMessage{UserFacingText: "old"}

	statusmessage.SaveStatusMessageHistoryIfChanged(ctx, 123, nil, nil, currentMsg, nil, nil)

	messages := listStatusMessagesForTest(ctx, t, essClient, 123)
	if len(messages) > 0 {
		t.Errorf("saveStatusMessageHistoryIfChanged() wrote %d messages when new message is nil, want 0", len(messages))
	}
}

func TestSaveStatusMessageHistoryIfChanged_EqualMessages(t *testing.T) {
	fix := setupTest(t)
	ctx, essClient := fix.ctx, fix.essClient

	currentMsg := &essmsgpb.StructuredMessage{UserFacingText: "same"}
	newMsg := &essmsgpb.StructuredMessage{UserFacingText: "same"}

	statusmessage.SaveStatusMessageHistoryIfChanged(ctx, 123, nil, nil, currentMsg, newMsg, nil)

	messages := listStatusMessagesForTest(ctx, t, essClient, 123)
	if len(messages) > 0 {
		t.Errorf("saveStatusMessageHistoryIfChanged() wrote %d messages when messages are equal, want 0", len(messages))
	}
}

func TestSaveStatusMessageHistoryIfChanged_Success(t *testing.T) {
	fix := setupTest(t)
	ctx, storage, essClient := fix.ctx, fix.storage, fix.essClient

	const xid int64 = 123
	spanner.CreateBlankExperiment(ctx, t, storage, xid)

	currentMsg := &essmsgpb.StructuredMessage{
		Parent:         "experiments/123",
		UserFacingText: "old",
	}
	newMsg := &essmsgpb.StructuredMessage{
		Parent:         "experiments/123",
		UserFacingText: "new",
	}

	_, err := essClient.ReadWriteTransaction(ctx, func(ctx context.Context, txn *cloudspanner.ReadWriteTransaction) error {
		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, xid, nil, nil, currentMsg, newMsg, txn)
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
	fix := setupTest(t)
	ctx, essClient := fix.ctx, fix.essClient

	currentMsg := &essmsgpb.StructuredMessage{
		UserFacingText: "same",
		CreateTime:     &timestamppb.Timestamp{Seconds: 100},
	}
	newMsg := &essmsgpb.StructuredMessage{
		UserFacingText: "same",
		CreateTime:     &timestamppb.Timestamp{Seconds: 200},
	}

	statusmessage.SaveStatusMessageHistoryIfChanged(ctx, 123, nil, nil, currentMsg, newMsg, nil)

	messages := listStatusMessagesForTest(ctx, t, essClient, 123)
	if len(messages) > 0 {
		t.Errorf("saveStatusMessageHistoryIfChanged() wrote %d messages when messages are equal with different timestamps, want 0", len(messages))
	}
}

func TestSaveStatusMessageHistory_RequiresTransaction(t *testing.T) {
	fix := setupTest(t)
	ctx, storage := fix.ctx, fix.storage

	newMsg := &essmsgpb.StructuredMessage{Parent: "experiments/123", UserFacingText: "test"}
	err := storage.SaveStatusMessageHistory(ctx, 123, nil, nil, nil, newMsg)
	if !errors.Is(err, status.ErrFailedPrecondition) {
		t.Errorf("SaveStatusMessageHistory outside tx returned %v, want FailedPrecondition", err)
	}
}

func TestSaveStatusMessageHistory_SuccessInTransaction(t *testing.T) {
	fix := setupTest(t)
	ctx, storage, essClient := fix.ctx, fix.storage, fix.essClient

	const xid int64 = 456
	spanner.CreateBlankExperiment(ctx, t, storage, xid)

	newMsg := &essmsgpb.StructuredMessage{
		Parent:         "experiments/456",
		UserFacingText: "transactional message",
	}

	err := storage.RunInTransaction(ctx, func(txCtx context.Context) error {
		return storage.SaveStatusMessageHistory(txCtx, xid, nil, nil, nil, newMsg)
	})
	if err != nil {
		t.Fatalf("RunInTransaction with SaveStatusMessageHistory failed: %v", err)
	}

	messages := listStatusMessagesForTest(ctx, t, essClient, xid)
	if len(messages) != 1 {
		t.Fatalf("SaveStatusMessageHistory wrote %d rows, want 1", len(messages))
	}
	if messages[0] != "transactional message" {
		t.Errorf("SaveStatusMessageHistory UserFacingText = %q, want %q", messages[0], "transactional message")
	}
}

type testFixture struct {
	ctx       context.Context
	storage   *spanner.Storage
	essClient *cloudspanner.Client
}

func setupTest(t *testing.T) testFixture {
	t.Helper()
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage() err = %v, want nil", err)
	}
	return testFixture{
		ctx:       ctx,
		storage:   storage,
		essClient: essClient,
	}
}

func listStatusMessagesForTest(ctx context.Context, t *testing.T, essClient *cloudspanner.Client, xid int64) []string {
	t.Helper()
	stmt := cloudspanner.Statement{
		SQL:    "SELECT UserFacingText FROM StatusMessages WHERE Xid = @xid",
		Params: map[string]any{"xid": xid},
	}
	iter := essClient.Single().Query(ctx, stmt)
	defer iter.Stop()

	var messages []string
	if err := iter.Do(func(r *cloudspanner.Row) error {
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
