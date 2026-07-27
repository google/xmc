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
	"golang/godebug/diff/diff"
	"golang/protobuf/v2/encoding/prototext/prototext"
	"golang/protobuf/v2/proto/proto"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
)

// UpdateWorkUnit updates a work unit with the given update mask.
func (r *Repository) UpdateWorkUnit(ctx context.Context, update *storage.WorkUnitUpdate) (originalWorkUnit *essmsgpb.WorkUnit, updatedWorkUnit *essmsgpb.WorkUnit, err error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(update.XID, update.WID))
	logging.Info(ctx, "UpdateWorkUnit", "update", update.WorkUnit)

	var mutated bool
	_, err = r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		mutated = false

		var err error
		originalWorkUnit, err = r.FetchWorkUnitTransaction(ctx, update.XID, update.WID, txn)
		if err != nil {
			return err
		}

		updatedWorkUnit, err = data.MergeWorkUnits(originalWorkUnit, update.WorkUnit, update.UpdateMask)
		if err != nil {
			return fmt.Errorf("failed to merge WorkUnit: %w", err)
		}

		if proto.Equal(originalWorkUnit, updatedWorkUnit) {
			return nil
		}

		now := time.Now()
		updatedWorkUnit.UpdateTime = timestamppb.New(now)

		mutation, err := spannerdata.WorkUnitProtoToUpdateMutation(updatedWorkUnit)
		if err != nil {
			return fmt.Errorf("failed to create Spanner mutation for WorkUnit: %w", err)
		}

		mutated = true
		return txn.BufferWrite([]*spanner.Mutation{mutation})
	})
	if err != nil {
		return nil, nil, err
	}

	if mutated {
		protoDiff := diff.Diff(prototext.Format(originalWorkUnit), prototext.Format(updatedWorkUnit))
		logging.Info(ctx, fmt.Sprintf("WorkUnit updated successfully: %s", protoDiff), "updatedWorkUnit", updatedWorkUnit)
	} else {
		logging.Info(ctx, "WorkUnit not updated; no changes detected", "updatedWorkUnit", updatedWorkUnit)
	}

	return originalWorkUnit, updatedWorkUnit, nil
}
