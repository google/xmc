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
	"slices"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/validator"
)

// InsertWorkUnit buffers a mutation to insert a new WorkUnit in Spanner using the bound read-write transaction.
func (r *TxRepository) InsertWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) error {
	return r.InsertWorkUnits(ctx, []*essmsgpb.WorkUnit{wu})
}

// InsertWorkUnits buffers the insert mutations of new WorkUnits of one experiment in Spanner in a
// single write using the bound read-write transaction.
func (r *TxRepository) InsertWorkUnits(ctx context.Context, wus []*essmsgpb.WorkUnit) error {
	if slices.Contains(wus, nil) {
		return status.Error(codes.InvalidArgument, "work unit must not be nil")
	}
	if err := requireOneExperiment(wus); err != nil {
		return err
	}
	if r.tx == nil {
		return fmt.Errorf("transaction must not be nil")
	}
	mutations := make([]*spanner.Mutation, len(wus))
	for i, wu := range wus {
		mutation, err := spannerdata.WorkUnitProtoToInsertMutation(wu)
		if err != nil {
			return err
		}
		mutations[i] = mutation
	}
	if err := r.tx.BufferWrite(mutations); err != nil {
		logging.Error(ctx, "failed to buffer write mutation for create work unit", "error", err)
		return err
	}
	return nil
}

// requireOneExperiment returns codes.InvalidArgument when wus belong to more than one experiment.
// Work units with an invalid execution name are left for the insert mutation to reject.
func requireOneExperiment(wus []*essmsgpb.WorkUnit) error {
	xids := make(map[int64]bool, 1)
	for _, wu := range wus {
		if xid, _, _, err := validator.ValidateWorkUnitExecutionName(wu.GetCurrentWorkUnitExecution().GetName()); err == nil {
			xids[xid] = true
		}
	}
	if len(xids) > 1 {
		return status.Error(codes.InvalidArgument, "work units must belong to one experiment")
	}
	return nil
}
