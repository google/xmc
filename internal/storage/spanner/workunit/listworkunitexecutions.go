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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"googlesql/safesql/go/safesql"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/validator/validator"
)

var listWorkUnitExecutionsSQL = safesql.New(`
SELECT
	Xid,
	WorkUnitId,
	ExecutionId,
	ExecutionMetadata,
	WorkUnitStatus,
	CreateTime,
	UpdateTime,
	CompleteTime,
FROM
  WorkUnitExecutions
WHERE
  BitReversedXid = @bitReversedXid AND WorkUnitId = @workUnitId
ORDER BY
  ExecutionId DESC;
`)

// ListWorkUnitExecutions retrieves a list of WorkUnitExecutions from storage by the parent work unit.
func (r *Repository) ListWorkUnitExecutions(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	logging.Info(ctx, "Fetch work unit executions", "xid", xid, "wid", wid)

	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()

	var workUnitExecutions []*essmsgpb.WorkUnitExecution

	wue, err := r.FetchCurrentWorkUnitExecutionTransaction(ctx, xid, wid, txn)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch work unit: %w", err)
	}
	workUnitExecutions = append(workUnitExecutions, wue)

	stmt := spanner.NewStatement(listWorkUnitExecutionsSQL.String())
	stmt.Params["bitReversedXid"] = reverseBits(xid)
	stmt.Params["workUnitId"] = wid

	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	if err := iter.Do(func(row *spanner.Row) error {
		wue, err := spannerdata.WorkUnitExecutionRowToProto(row)
		if err != nil {
			return err
		}

		workUnitExecutions = append(workUnitExecutions, wue)

		return nil
	}); err != nil {
		logging.Error(ctx, "Failed to read work unit execution rows", "error", err)
		return nil, fmt.Errorf("failed to read work unit execution rows: %w", err)
	}

	logging.Info(ctx, "Fetched work unit executions", "workUnitExecutions", workUnitExecutions)

	return workUnitExecutions, nil
}
