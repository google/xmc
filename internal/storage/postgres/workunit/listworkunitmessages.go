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

package workunit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// listWorkUnitMessagesSQL reads the current statuses of the work units of an experiment whose
// highest status message severity is the requested one.
var listWorkUnitMessagesSQL = safesql.New(`SELECT WorkUnitStatus
FROM WorkUnits
WHERE Xid = $1 AND ExecutionStatusMessageSeverity = $2`)

// ListWorkUnitMessagesBySeverity retrieves the system and user status messages of severity
// severity from the current executions of the work units of experiment xid within the transaction.
func (r *TxRepository) ListWorkUnitMessagesBySeverity(ctx context.Context, xid int64, severity essmsgpb.StructuredMessage_Severity) ([]*essmsgpb.StructuredMessage, error) {
	if err := r.requireTx(); err != nil {
		return nil, err
	}
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Fetch work unit messages by severity", "severity", severity)

	rows, err := r.tx.Query(ctx, listWorkUnitMessagesSQL.String(), xid, int64(severity))
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", pgdb.TranslateError(err, workUnitEntity))
	}
	statuses, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", pgdb.TranslateError(err, workUnitEntity))
	}

	var messages []*essmsgpb.StructuredMessage
	for _, encoded := range statuses {
		statusProto := &essmsgpb.WorkUnitStatus{}
		if err := unmarshalColumn("WorkUnitStatus", encoded, statusProto); err != nil {
			return nil, err
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
