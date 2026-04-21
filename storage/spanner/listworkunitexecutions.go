package spanner

import (
	"context"
	"fmt"

	"googlesql/safesql/go/safesql"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
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

// ListWorkUnitExecutions retrieves a list of WorkUnitExecutions from storage by the parent work
// unit.
func (s *Storage) ListWorkUnitExecutions(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	logging.Info(ctx, "Fetch work unit executions", "xid", xid, "wid", wid)

	txn := s.client.ReadOnlyTransaction()
	defer txn.Close()

	var workUnitExecutions []*essmsgpb.WorkUnitExecution

	// 1. Fetch the current work unit execution.
	wue, err := s.fetchCurrentWorkUnitExecutionTransaction(ctx, xid, wid, txn)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch work unit: %w", err)
	}
	workUnitExecutions = append(workUnitExecutions, wue)

	// 2. Fetch the work unit executions from storage.
	stmt := spanner.NewStatement(listWorkUnitExecutionsSQL.String())
	stmt.Params["bitReversedXid"] = reverseBits(xid)
	stmt.Params["workUnitId"] = wid

	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	if err := iter.Do(func(r *spanner.Row) error {
		wue, err := spannerdata.WorkUnitExecutionRowToProto(r)
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
