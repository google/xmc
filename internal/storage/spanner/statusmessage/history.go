// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package statusmessage

import (
	"context"
	"crypto/rand"
	"fmt"
	"math"
	"math/big"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/protobuf/v2/proto/proto"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
)

// SaveStatusMessageHistoryIfChanged buffers a status message history mutation if the message changed and history is enabled.
// TODO(b/517479143): Move feature flag evaluation and status message change detection above the repository layer.
func SaveStatusMessageHistoryIfChanged(ctx context.Context, enabled bool, xid int64, wid *int64, eid *int64, currentMsg, newMsg *essmsgpb.StructuredMessage, txn *spanner.ReadWriteTransaction) {
	if !enabled {
		return
	}

	if newMsg == nil {
		return
	}

	if currentMsg != nil {
		currentClone := proto.Clone(currentMsg).(*essmsgpb.StructuredMessage)
		newClone := proto.Clone(newMsg).(*essmsgpb.StructuredMessage)

		currentClone.CreateTime = nil
		newClone.CreateTime = nil

		if proto.Equal(currentClone, newClone) {
			return
		}
	}

	SaveStatusMessageHistory(ctx, xid, wid, eid, currentMsg, newMsg, txn)
}

// SaveStatusMessageHistory buffers a status message history mutation in the Spanner transaction.
// TODO(b/517479143): Refactor SaveStatusMessageHistory to return an error and propagate it so status message history failures fail the transaction instead of silently logging.
func SaveStatusMessageHistory(ctx context.Context, xid int64, wid *int64, eid *int64, oldMsg, newMsg *essmsgpb.StructuredMessage, txn *spanner.ReadWriteTransaction) {
	messageID, err := generateMessageID(ctx)
	if err != nil {
		logging.Error(ctx, "Failed to generate MessageId, skipping saving status message history", "error", err)
		return
	}

	mutation, err := spannerdata.StatusMessageProtoToInsertMutation(newMsg, messageID, eid, oldMsg)
	if err != nil {
		logging.Error(ctx, "Failed to create mutation for status message", "error", err)
		return
	}

	txn.BufferWrite([]*spanner.Mutation{mutation})
}

func generateMessageID(ctx context.Context) (int64, error) {
	// Generate a random number between 0 and math.MaxInt64
	n, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return 0, fmt.Errorf("failed to generate random MessageId: %w", err)
	}
	return n.Int64(), nil
}
