package spanner

import (
	"context"
	"fmt"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/workunitschema"
	"xmanager_cloud/validator/validator"
	"task/go/status"
)

// FetchCurrentWorkUnitExecution retrieves the current work unit execution for a given work unit.
func (s *Storage) FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	return s.fetchCurrentWorkUnitExecutionTransaction(ctx, xid, wid, s.client.Single())
}

func (s *Storage) fetchCurrentWorkUnitExecutionTransaction(ctx context.Context, xid int64, wid int64, txn spannerTransaction) (*essmsgpb.WorkUnitExecution, error) {
	logging.Info(ctx, "Fetch current work unit execution row", "xid", xid, "wid", wid)

	row, err := txn.ReadRow(ctx, workunitschema.WorkUnitTableName, spanner.Key{reverseBits(xid), wid}, workunitschema.CurrentWorkUnitExecutionColumns)
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			return nil, fmt.Errorf("WorkUnit %d for XID %d not found: %w", wid, xid, status.ErrNotFound)
		}

		logging.Error(ctx, "Failed to read WorkUnit row", "error", err)
		return nil, fmt.Errorf("failed to read WorkUnit row: %w", err)
	}

	wue, err := spannerdata.WorkUnitCurrentExecutionRowToProto(row)
	if err != nil {
		return nil, fmt.Errorf("failed to convert WorkUnit row to proto: %w", err)
	}

	logging.Info(ctx, "Fetched current work unit execution from storage", "work_unit_execution", wue)

	return wue, nil
}
