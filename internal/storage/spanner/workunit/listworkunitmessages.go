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

package workunit

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage/spanner/utils"
	"xmanager_cloud/logging/logging"
)

var listWorkUnitMessagesSQL = safesql.New(`
SELECT
	WorkUnitStatus
FROM
	WorkUnits
WHERE
	BitReversedXid = @bitReversedXid
	AND ExecutionStatusMessageSeverity = @severity
`)

// ListWorkUnitMessagesBySeverity retrieves work unit status messages matching a given severity level.
func (r *Repository) ListWorkUnitMessagesBySeverity(ctx context.Context, xid int64, severity essmsgpb.StructuredMessage_Severity, txn spannerTransaction) ([]*essmsgpb.StructuredMessage, error) {
	logging.Info(ctx, "Fetch work unit messages by severity", "severity", severity)
	stmt := spanner.NewStatement(listWorkUnitMessagesSQL.String())
	stmt.Params["bitReversedXid"] = spannerutils.ReverseBits(xid)
	stmt.Params["severity"] = severity

	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	var messages []*essmsgpb.StructuredMessage
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to get messages: %w", err)
		}

		statusProto := &essmsgpb.WorkUnitStatus{}
		if err := row.ColumnByName("WorkUnitStatus", statusProto); err != nil {
			return nil, fmt.Errorf("failed to read WorkUnitStatus column: %w", err)
		}

		if msg := statusProto.GetSystemMessage(); msg != nil && msg.GetSeverity() == severity {
			messages = append(messages, msg)
		}
		if msg := statusProto.GetUserMessage(); msg != nil && msg.GetSeverity() == severity {
			messages = append(messages, msg)
		}
	}
	logging.Info(ctx, "Fetched work unit messages by severity", "count", len(messages), "severity", severity)
	return messages, nil
}
