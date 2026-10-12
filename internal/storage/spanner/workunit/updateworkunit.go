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

	"cloud.google.com/go/spanner"
	essmsgpb "github.com/google/xmc/api/protos/experiment"
	"github.com/google/xmc/internal/logging"
	"github.com/google/xmc/internal/storage"
	"github.com/google/xmc/internal/storage/data"
	"github.com/google/xmc/internal/storage/spanner/data"
	"github.com/google/xmc/internal/validator"
	"github.com/kylelemons/godebug/diff"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// UpdateWorkUnit updates a work unit with the given update mask.
// TODO(b/517479143): Move work unit update merging logic outside the repository layer into a domain service layer.
// UpdateWorkUnit updates a work unit with the given update mask. It returns both the original and
// updated work units in that order, so that the caller can choose whether to perform additional
// actions based on the original work unit state.
func (r *Repository) UpdateWorkUnit(ctx context.Context, update *storage.WorkUnitUpdate) (storage.UpdatedWorkUnits, error) {
	if update == nil {
		return storage.UpdatedWorkUnits{}, fmt.Errorf("update must not be nil")
	}
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(update.XID, update.WID))
	logging.Info(ctx, "UpdateWorkUnit", "update", update.WorkUnit)

	var originalWorkUnit, updatedWorkUnit *essmsgpb.WorkUnit
	var mutated bool
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		mutated = false

		// 1. Read the existing WorkUnit from storage.
		var err error
		originalWorkUnit, err = r.FetchWorkUnitTransaction(ctx, update.XID, update.WID, txn)
		if err != nil {
			return err
		}

		// 2. Merge the current and new entries based on the update mask.
		updatedWorkUnit, err = data.MergeWorkUnits(originalWorkUnit, update.WorkUnit, update.UpdateMask)
		if err != nil {
			return fmt.Errorf("failed to merge WorkUnit: %w", err)
		}

		// 2.1. Check if the work unit has changed.
		if proto.Equal(originalWorkUnit, updatedWorkUnit) {
			return nil
		}

		// 2.2. Update the UpdateTime column to the current time.
		now := time.Now()
		updatedWorkUnit.UpdateTime = timestamppb.New(now)

		// 3. Construct the Spanner mutation for the update.
		mutation, err := spannerdata.WorkUnitProtoToUpdateMutation(updatedWorkUnit)
		if err != nil {
			return fmt.Errorf("failed to create Spanner mutation for WorkUnit: %w", err)
		}

		// 4. Apply the mutation to the transaction.
		mutated = true
		return txn.BufferWrite([]*spanner.Mutation{mutation})
	})
	if err != nil {
		return storage.UpdatedWorkUnits{}, err
	}

	if mutated {
		protoDiff := diff.Diff(prototext.Format(originalWorkUnit), prototext.Format(updatedWorkUnit))
		logging.Info(ctx, fmt.Sprintf("WorkUnit updated successfully: %s", protoDiff), "updatedWorkUnit", updatedWorkUnit)
	} else {
		logging.Info(ctx, "WorkUnit not updated; no changes detected", "updatedWorkUnit", updatedWorkUnit)
	}

	return storage.UpdatedWorkUnits{
		Original: originalWorkUnit,
		Updated:  updatedWorkUnit,
	}, nil
}
