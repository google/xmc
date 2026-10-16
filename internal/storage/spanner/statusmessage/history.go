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

	"cloud.google.com/go/spanner"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
)

// SaveStatusMessageHistory buffers a status message history mutation in the bound Spanner transaction.
func (r *TxRepository) SaveStatusMessageHistory(ctx context.Context, xid int64, wid, eid *int64, oldMsg, newMsg *essmsgpb.StructuredMessage) error {
	if r.tx == nil {
		return fmt.Errorf("transaction must not be nil")
	}
	messageID, err := generateMessageID(ctx)
	if err != nil {
		logging.Error(ctx, "Failed to generate MessageId", "error", err)
		return err
	}

	mutation, err := spannerdata.StatusMessageProtoToInsertMutation(newMsg, messageID, eid, oldMsg)
	if err != nil {
		logging.Error(ctx, "Failed to create mutation for status message", "error", err)
		return err
	}

	return r.tx.BufferWrite([]*spanner.Mutation{mutation})
}

func generateMessageID(ctx context.Context) (int64, error) {
	// Generate a random number between 0 and math.MaxInt64
	n, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return 0, fmt.Errorf("failed to generate random MessageId: %w", err)
	}
	return n.Int64(), nil
}
