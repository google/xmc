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

package operation

import (
	"context"
	"fmt"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	lropb "longrunning/operations_go_proto"
	"task/go/status"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/operationschema"
	"xmanager_cloud/validator/validator"
)

// FetchWorkUnitOperation retrieves an operation by its unique ID.
func (r *Repository) FetchWorkUnitOperation(ctx context.Context, xid int64, wid int64, oid string) (*lropb.Operation, error) {
	ctx = logging.WithOperationName(ctx, validator.ConstructOperationName(xid, wid, oid))
	return r.FetchWorkUnitOperationTransaction(ctx, xid, wid, oid, r.client.Single())
}

// FetchWorkUnitOperationTransaction retrieves an operation in a transaction.
func (r *Repository) FetchWorkUnitOperationTransaction(ctx context.Context, xid int64, wid int64, oid string, txn spannerTransaction) (*lropb.Operation, error) {
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
