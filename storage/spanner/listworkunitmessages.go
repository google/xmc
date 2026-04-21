package spanner

import (
	"context"
	"fmt"

	"googlesql/safesql/go/safesql"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/google_api/iterator/iterator"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
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

func (s *Storage) listWorkUnitMessagesBySeverity(ctx context.Context, xid int64, severity essmsgpb.StructuredMessage_Severity, txn spannerTransaction) ([]*essmsgpb.StructuredMessage, error) {
	logging.Info(ctx, "Fetch work unit messages by severity", "severity", severity)
	stmt := spanner.NewStatement(listWorkUnitMessagesSQL.String())
	stmt.Params["bitReversedXid"] = reverseBits(xid)
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
