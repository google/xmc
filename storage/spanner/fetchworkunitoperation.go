package spanner

import (
	"context"
	"fmt"

	lropb "longrunning/operations_go_proto"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/operationschema"
	"xmanager_cloud/validator/validator"
	"task/go/status"
)

// FetchWorkUnitOperation retrieves an operation by its unique ID.
func (s *Storage) FetchWorkUnitOperation(ctx context.Context, xid int64, wid int64, oid string) (*lropb.Operation, error) {
	ctx = logging.WithOperationName(ctx, validator.ConstructOperationName(xid, wid, oid))
	return s.fetchWorkUnitOperationTransaction(ctx, xid, wid, oid, s.client.Single())
}

func (s *Storage) fetchWorkUnitOperationTransaction(ctx context.Context, xid int64, wid int64, oid string, txn spannerTransaction) (*lropb.Operation, error) {
	logging.Info(ctx, "Fetch work unit operation")

	row, err := txn.ReadRow(ctx, operationschema.OperationsTable, spanner.Key{reverseBits(xid), wid, oid}, operationschema.OperationColumns)
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			return nil, fmt.Errorf("operation %s for XID %d, WID %d not found: %w", oid, xid, wid, status.ErrNotFound)
		}

		logging.Error(ctx, "Failed to read Operation row", "error", err)
		return nil, fmt.Errorf("failed to read Operation row: %w", err)
	}

	operation, err := spannerdata.OperationRowToProto(row)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Operation row to proto: %w", err)
	}

	logging.Info(ctx, "Fetched work unit operation", "operation", operation)

	return operation, nil
}
