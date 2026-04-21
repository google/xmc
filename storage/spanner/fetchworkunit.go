package spanner

import (
	"context"
	"fmt"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/workunitschema"
	"xmanager_cloud/validator/validator"
	"task/go/status"
)

// FetchWorkUnit retrieves a single WorkUnit from storage by its unique XID and WID.
func (s *Storage) FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	return s.fetchWorkUnitTransaction(ctx, xid, wid, s.client.Single())
}

func (s *Storage) fetchWorkUnitTransaction(ctx context.Context, xid int64, wid int64, txn spannerTransaction) (*essmsgpb.WorkUnit, error) {
	logging.Info(ctx, "Fetch work unit row")

	row, err := txn.ReadRow(ctx, workunitschema.WorkUnitTableName, spanner.Key{reverseBits(xid), wid}, workunitschema.WorkUnitColumns)
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			return nil, fmt.Errorf("WorkUnit %d for XID %d not found: %w", wid, xid, status.ErrNotFound)
		}

		logging.Error(ctx, "Failed to read work unit row", "error", err)
		return nil, fmt.Errorf("failed to read work unit row: %w", err)
	}

	workUnit, err := spannerdata.WorkUnitRowToProto(row)
	if err != nil {
		return nil, fmt.Errorf("failed to convert WorkUnit row to proto: %w", err)
	}

	logging.Info(ctx, "Fetched work unit from storage", "work_unit", workUnit)

	return workUnit, nil
}
