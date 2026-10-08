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
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/workunitschema"
	"xmanager_cloud/storage/spanner/utils/spannerutils"
	"xmanager_cloud/validator/validator"
)

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
