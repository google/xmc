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
	"testing"

	cloudspanner "cloud.google.com/go/spanner"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
)

func TestSaveStatusMessageHistory_Success(t *testing.T) {
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

	repo, err := statusmessage.NewRepository(essClient)
	if err != nil {
		t.Fatalf("NewRepository() failed: %v", err)
	}

	_, err = essClient.ReadWriteTransaction(ctx, func(ctx context.Context, txn *cloudspanner.ReadWriteTransaction) error {
		return repo.WithTx(txn).SaveStatusMessageHistory(ctx, xid, nil, nil, currentMsg, newMsg)
	})
	if err != nil {
		t.Fatalf("SaveStatusMessageHistory() ReadWriteTransaction failed: %v", err)
	}

	messages := listStatusMessagesForTest(ctx, t, essClient, xid)

	if len(messages) != 1 {
		t.Fatalf("SaveStatusMessageHistory() wrote %d rows in StatusMessages, want 1", len(messages))
	}

	if messages[0] != "new" {
		t.Errorf("SaveStatusMessageHistory() wrote UserFacingText %q, want %q", messages[0], "new")
	}
}

func TestTxRepository_SaveStatusMessageHistory_PropagatesError(t *testing.T) {
	fix := setupTest(t)
	ctx, essClient := fix.ctx, fix.essClient

	repo, err := statusmessage.NewRepository(essClient)
	if err != nil {
		t.Fatalf("NewRepository() failed: %v", err)
	}

	invalidMsg := &essmsgpb.StructuredMessage{
		Parent:         "invalid-parent",
		UserFacingText: "new",
	}

	_, err = essClient.ReadWriteTransaction(ctx, func(ctx context.Context, txn *cloudspanner.ReadWriteTransaction) error {
		return repo.WithTx(txn).SaveStatusMessageHistory(ctx, 123, nil, nil, nil, invalidMsg)
	})
	if err == nil {
		t.Fatalf("TxRepository.SaveStatusMessageHistory() with invalid parent succeeded, want error")
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
