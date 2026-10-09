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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/workunitschema"
	"xmanager_cloud/storage/spanner/utils/spannerutils"
	"xmanager_cloud/validator/validator"
)

var getUpdatingWorkUnitsSQL = safesql.New(`
	SELECT
		Xid,
		WorkUnitId,
		ExecutionId,
		ExecutionCreateTime AS CreateTime,
		ExecutionUpdateTime AS UpdateTime,
		ExecutionCompleteTime AS CompleteTime,
		ExecutionMetadata,
		WorkUnitStatus,
		Executable
	FROM
		WorkUnits
	WHERE
		BitReversedXid = @bitReversedXid AND WorkUnitId IN UNNEST(@workUnitIds)
	FOR UPDATE;
`)

// FetchWorkUnit retrieves a single WorkUnit from storage by its unique XID and WID.
func (r *Repository) FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	return r.FetchWorkUnitTransaction(ctx, xid, wid, r.client.Single())
}

// FetchWorkUnit retrieves a single WorkUnit from storage by its unique XID and WID within the bound read-write transaction.
func (r *TxRepository) FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	return r.FetchWorkUnitTransaction(ctx, xid, wid, r.tx)
}

// FetchWorkUnitTransaction retrieves a single WorkUnit from storage by its unique XID and WID in a transaction.
func (r *Repository) FetchWorkUnitTransaction(ctx context.Context, xid int64, wid int64, txn spannerTransaction) (*essmsgpb.WorkUnit, error) {
	logging.Info(ctx, "Fetch work unit row")

	row, err := txn.ReadRow(ctx, workunitschema.WorkUnitTableName, spanner.Key{spannerutils.ReverseBits(xid), wid}, workunitschema.WorkUnitColumns)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, fmt.Errorf("WorkUnit %d for XID %d not found: %w", wid, xid, grpcerr.ErrNotFound)
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

// FetchUpdatingWorkUnits retrieves a batch of work units for update by their unique IDs within the bound read-write transaction.
func (r *TxRepository) FetchUpdatingWorkUnits(ctx context.Context, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnit, error) {
	return r.FetchUpdatingWorkUnitsTransaction(ctx, xid, wids, r.tx)
}

// FetchUpdatingWorkUnitsTransaction retrieves a batch of work units for update by their unique IDs in a transaction.
func (r *Repository) FetchUpdatingWorkUnitsTransaction(ctx context.Context, xid int64, wids []int64, txn spannerTransaction) (map[int64]*essmsgpb.WorkUnit, error) {
	stmt := spanner.NewStatement(getUpdatingWorkUnitsSQL.String())
	stmt.Params["bitReversedXid"] = spannerutils.ReverseBits(xid)
	stmt.Params["workUnitIds"] = wids

	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	workUnits := make(map[int64]*essmsgpb.WorkUnit, len(wids))
	if err := iter.Do(func(row *spanner.Row) error {
		workUnitExecution, err := spannerdata.WorkUnitExecutionRowToProto(row)
		if err != nil {
			return fmt.Errorf("failed to convert row to WorkUnitExecution proto: %w", err)
		}

		_, wid, _, err := validator.ValidateWorkUnitExecutionName(workUnitExecution.GetName())
		if err != nil {
			return fmt.Errorf("failed to validate WorkUnitExecution name %q: %w", workUnitExecution.GetName(), err)
		}

		var exec essmsgpb.Executable
		if err := row.ColumnByName("Executable", &exec); err != nil {
			logging.Error(ctx, "failed to scan Executable column from WorkUnits table", "work_unit_id", wid, "error", err)
			return fmt.Errorf("failed to scan Executable column: %w", err)
		}

		workUnits[wid] = &essmsgpb.WorkUnit{
			Name:                     validator.ConstructWorkUnitName(xid, wid),
			Executable:               &exec,
			CurrentWorkUnitExecution: workUnitExecution,
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return workUnits, nil
}
