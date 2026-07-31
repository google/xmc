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
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	experimentdomain "xmanager_cloud/internal/domain/experiment"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
	"xmanager_cloud/logging/logging"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/validator/validator"
)

// CreateWorkUnit creates a new work unit in Spanner.
// TODO(b/517479143): Refactor CreateWorkUnit to separate validation, status denormalization, status message saving, and resource set merging into a domain service layer above the storage repository.
func (r *Repository) CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	ctx = logging.WithWorkUnitExecutionName(ctx, wu.GetCurrentWorkUnitExecution().GetName())
	logging.Info(ctx, "Write work unit to storage", "work_unit", wu)

	exp := &essmsgpb.Experiment{}
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		now := time.Now()

		// 1. Extract the XID and WID from the work unit name.
		xid, wid, err := validator.ValidateWorkUnitNameAllowZeroWID(wu.GetName())
		if err != nil {
			return fmt.Errorf("failed to validate work unit name: %w (%v)", status.ErrInvalidArgument, err)
		}

		// 1.1. If the work unit ID is 0, fetch the next available ID and update the names.
		initialEID := int64(1)
		if wid == 0 {
			wid, err = r.FetchNextWorkUnitIDTransaction(ctx, xid, tx)
			if err != nil {
				return fmt.Errorf("failed to fetch next work unit ID: %w", err)
			}
			wu.Name = validator.ConstructWorkUnitName(xid, wid)
			wu.CurrentWorkUnitExecution.Name = validator.ConstructWorkUnitExecutionName(xid, wid, initialEID)
			wu.CurrentWorkUnitExecution.Status.SystemMessage.Parent = wu.GetName()
		}

		// 1.2. Set the initial timestamps to the current time.
		wu.CreateTime = timestamppb.New(now)
		wu.UpdateTime = timestamppb.New(now)
		wu.CurrentWorkUnitExecution.CreateTime = timestamppb.New(now)
		wu.CurrentWorkUnitExecution.UpdateTime = timestamppb.New(now)
		wu.CurrentWorkUnitExecution.Status.UpdateTime = timestamppb.New(now)
		wu.CurrentWorkUnitExecution.Status.StateUpdateTime = timestamppb.New(now)

		// 2. Insert the work unit into Spanner.
		if err := r.InsertWorkUnit(ctx, tx, wu); err != nil {
			return fmt.Errorf("failed to insert work unit: %w", err)
		}

		// 3. Read the Experiment row.
		if r.experimentRepo == nil {
			return fmt.Errorf("experiment repository is not initialized")
		}
		exp, err = r.experimentRepo.FetchExperimentTransaction(ctx, xid, tx)
		if err != nil {
			return fmt.Errorf("failed to fetch experiment: %w", err)
		}
		if exp == nil {
			return fmt.Errorf("experiment %d not found", xid)
		}

		// 4. Update the denormalized status fields and resources in the Experiment row using domain rules.
		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, r.statusMessageHistoryEnabled, xid, &wid, &initialEID, nil, wu.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage(), tx)

		var resourcesChanged bool
		exp, resourcesChanged, err = experimentdomain.PrepareWorkUnitCreation(ctx, exp, wu, now)
		if err != nil {
			return fmt.Errorf("failed to prepare experiment status for work unit creation: %w", err)
		}

		// 5. Buffer mutations for Experiment status and resources via thin repository methods.
		if err := r.experimentRepo.UpdateExperimentStatus(ctx, tx, exp); err != nil {
			return fmt.Errorf("failed to update experiment status: %w", err)
		}
		if resourcesChanged {
			if err := r.experimentRepo.UpdateExperimentResources(ctx, tx, exp); err != nil {
				return fmt.Errorf("failed to update experiment resources: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		logging.Error(ctx, "failed to create work unit", "work_unit", wu, "error", err)
		return nil, err
	}

	logging.Info(ctx, "Written work unit to storage", "work_unit", wu)
	logging.Info(ctx, "Updated experiment successfully", "experiment", exp)

	return wu, nil
}

// InsertWorkUnit buffers a mutation to insert a new WorkUnit in Spanner.
func (r *Repository) InsertWorkUnit(ctx context.Context, tx *spanner.ReadWriteTransaction, wu *essmsgpb.WorkUnit) error {
	if wu == nil {
		return fmt.Errorf("work unit must not be nil")
	}
	mutation, err := spannerdata.WorkUnitProtoToInsertMutation(wu)
	if err != nil {
		return err
	}
	if err := tx.BufferWrite([]*spanner.Mutation{mutation}); err != nil {
		logging.Error(ctx, "failed to buffer write mutation for create work unit", "error", err)
		return err
	}
	return nil
}
