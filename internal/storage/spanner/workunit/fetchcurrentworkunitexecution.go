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
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/workunitschema"
	"xmanager_cloud/storage/spanner/utils/spannerutils"
	"xmanager_cloud/validator/validator"
)

// FetchCurrentWorkUnitExecution retrieves the current work unit execution for a given work unit.
func (r *Repository) FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	return r.FetchCurrentWorkUnitExecutionTransaction(ctx, xid, wid, r.client.Single())
}

// FetchCurrentWorkUnitExecutionTransaction retrieves the current work unit execution in a transaction.
func (r *Repository) FetchCurrentWorkUnitExecutionTransaction(ctx context.Context, xid int64, wid int64, txn spannerTransaction) (*essmsgpb.WorkUnitExecution, error) {
	logging.Info(ctx, "Fetch current work unit execution row", "xid", xid, "wid", wid)

	row, err := txn.ReadRow(ctx, workunitschema.WorkUnitTableName, spanner.Key{spannerutils.ReverseBits(xid), wid}, workunitschema.CurrentWorkUnitExecutionColumns)
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
