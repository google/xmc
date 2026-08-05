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

// Test cache invalidation on main

import (
	"context"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/protobuf/v2/proto/proto"
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
	var committedWU *essmsgpb.WorkUnit
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		now := time.Now()
		localWU := proto.Clone(wu).(*essmsgpb.WorkUnit)

		// 1. Extract the XID and WID from the work unit name.
		xid, wid, err := validator.ValidateWorkUnitNameAllowZeroWID(localWU.GetName())
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
			localWU.Name = validator.ConstructWorkUnitName(xid, wid)
			localWU.CurrentWorkUnitExecution.Name = validator.ConstructWorkUnitExecutionName(xid, wid, initialEID)
			localWU.CurrentWorkUnitExecution.Status.SystemMessage.Parent = localWU.GetName()
		}

		// 1.2. Set the initial timestamps to the current time.
		localWU.CreateTime = timestamppb.New(now)
		localWU.UpdateTime = timestamppb.New(now)
		localWU.CurrentWorkUnitExecution.CreateTime = timestamppb.New(now)
		localWU.CurrentWorkUnitExecution.UpdateTime = timestamppb.New(now)
		localWU.CurrentWorkUnitExecution.Status.UpdateTime = timestamppb.New(now)
		localWU.CurrentWorkUnitExecution.Status.StateUpdateTime = timestamppb.New(now)

		// Populate timestamps back onto input proto pointer for caller convenience (matching CreateExperiment behavior).
		wu.CreateTime = localWU.CreateTime
		wu.UpdateTime = localWU.UpdateTime
		if wu.CurrentWorkUnitExecution != nil {
			wu.CurrentWorkUnitExecution.CreateTime = localWU.CurrentWorkUnitExecution.CreateTime
			wu.CurrentWorkUnitExecution.UpdateTime = localWU.CurrentWorkUnitExecution.UpdateTime
			if wu.CurrentWorkUnitExecution.Status != nil {
				wu.CurrentWorkUnitExecution.Status.UpdateTime = localWU.CurrentWorkUnitExecution.Status.UpdateTime
				wu.CurrentWorkUnitExecution.Status.StateUpdateTime = localWU.CurrentWorkUnitExecution.Status.StateUpdateTime
			}
		}

		// 2. Insert the work unit into Spanner.
		if err := r.InsertWorkUnit(ctx, tx, localWU); err != nil {
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
		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, r.statusMessageHistoryEnabled, xid, &wid, &initialEID, nil, localWU.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage(), tx)

		var resourcesChanged bool
		updatedExp, resourcesChanged, err := experimentdomain.PrepareWorkUnitCreation(ctx, exp, localWU, now)
		if err != nil {
			return fmt.Errorf("failed to prepare experiment status for work unit creation: %w", err)
		}
		exp = updatedExp

		// 5. Buffer mutations for Experiment status and resources via thin repository methods.
		// TODO(b/517479143): Consolidate UpdateExperimentStatus and UpdateExperimentResources into a single mutation write to prevent duplicate row updates for the same Experiments row in Spanner.
		if err := r.experimentRepo.UpdateExperimentStatus(ctx, tx, exp); err != nil {
			return fmt.Errorf("failed to update experiment status: %w", err)
		}
		if resourcesChanged {
			if err := r.experimentRepo.UpdateExperimentResources(ctx, tx, exp); err != nil {
				return fmt.Errorf("failed to update experiment resources: %w", err)
			}
		}

		committedWU = localWU
		return nil
	})
	if err != nil {
		logging.Error(ctx, "failed to create work unit", "work_unit", wu, "error", err)
		return nil, err
	}

	logging.Info(ctx, "Written work unit to storage", "work_unit", committedWU)
	logging.Info(ctx, "Updated experiment successfully", "experiment", exp)

	return committedWU, nil
}

// InsertWorkUnit buffers a mutation to insert a new WorkUnit in Spanner.
func (r *Repository) InsertWorkUnit(ctx context.Context, tx *spanner.ReadWriteTransaction, wu *essmsgpb.WorkUnit) error {
	if wu == nil {
		return fmt.Errorf("work unit must not be nil")
	}
	if tx == nil {
		return fmt.Errorf("transaction must not be nil")
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
