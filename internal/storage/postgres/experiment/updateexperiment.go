// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package experiment

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kylelemons/godebug/diff"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// TODO(b/517479143): Refactor UpdateExperiment to extract experiment merging and status message persistence to a higher domain service layer.
// UpdateExperiment updates an experiment with the given update mask. We return both the original
// and updated experiments, so that the caller can choose whether to perform additional actions
// based on the original experiment state.
//
// The read-modify-write runs in a transaction of its own that locks the experiment row with FOR
// NO KEY UPDATE, so concurrent updates of the same experiment are serialized rather than lost.
// UpdateTime is set to the current time truncated to microseconds, the resolution of TIMESTAMPTZ,
// so that the returned experiment equals what later reads return.
func (r *Repository) UpdateExperiment(ctx context.Context, update *storage.ExperimentUpdate) (storage.UpdatedExperiments, error) {
	if update == nil {
		return storage.UpdatedExperiments{}, fmt.Errorf("update must not be nil")
	}
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(update.XID))
	logging.Info(ctx, "Update experiment in storage", "update", update.Experiment)

	if update.UpdateMask == nil {
		return storage.UpdatedExperiments{}, fmt.Errorf("failed to merge Experiment: update mask is nil: %w", grpcerr.ErrInvalidArgument)
	}

	var originalExperiment, updatedExperiment *pb.Experiment
	var mutated bool
	err := pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		// The callback may run more than once, so reset everything it captures.
		originalExperiment, updatedExperiment = nil, nil
		mutated = false

		// 1. Read and lock the existing experiment.
		var err error
		originalExperiment, err = fetchExperiment(ctx, tx, lockExperimentSQL, update.XID)
		if err != nil {
			return err
		}

		// 2. Merge the current and new experiments based on the update mask.
		updatedExperiment, err = data.MergeExperiments(originalExperiment, update.Experiment, update.UpdateMask)
		if err != nil {
			return fmt.Errorf("failed to merge Experiment: %w", err)
		}

		// 2.1. Check if the experiment has changed.
		if proto.Equal(originalExperiment, updatedExperiment) {
			return nil
		}

		// 2.2. Update the UpdateTime column to the current time.
		now := time.Now().Truncate(time.Microsecond)
		updatedExperiment.UpdateTime = timestamppb.New(now)

		// 3. Write the mutable columns back.
		mutated = true
		return updateExperimentRow(ctx, tx, updatedExperiment)
	})

	if err != nil {
		return storage.UpdatedExperiments{}, err
	}

	if mutated {
		protoDiff := diff.Diff(prototext.Format(originalExperiment), prototext.Format(updatedExperiment))
		logging.Info(ctx, fmt.Sprintf("Updated experiment in storage: %s", protoDiff), "updatedExperiment", updatedExperiment)
	} else {
		logging.Debug(ctx, "Experiment not updated; no changes detected", "updatedExperiment", updatedExperiment)
	}

	return storage.UpdatedExperiments{
		Original: originalExperiment,
		Updated:  updatedExperiment,
	}, nil
}

// updateExperimentRow writes the user-mutable columns of exp, the counterpart of the Spanner
// update mutation built from experimentschema.ExperimentUpdateColumns.
func updateExperimentRow(ctx context.Context, db pgdb.DBTX, exp *pb.Experiment) error {
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return fmt.Errorf("failed to validate experiment name %q: %w", exp.GetName(), err)
	}
	urls, err := marshalMessages(exp.GetUrls())
	if err != nil {
		return fmt.Errorf("failed to marshal experiment urls: %w", err)
	}
	return execExperimentUpdate(ctx, db, updateExperimentSQL, xid,
		exp.GetTitle(),               // Title
		int64(exp.GetPriority()),     // Priority
		urls,                         // Urls
		exp.GetTags(),                // Tags
		exp.GetNotes(),               // Notes
		exp.GetUpdateTime().AsTime(), // UpdateTime
	)
}

// UpdateExperimentStatus writes an Experiment's status using the bound read-write transaction,
// together with the ActiveState and LaunchState columns derived from it, the UpdateTime taken
// from the status's update time and the CompleteTime. It fails with a NotFound error when the
// experiment does not exist.
func (r *TxRepository) UpdateExperimentStatus(ctx context.Context, exp *pb.Experiment) error {
	if exp == nil {
		return errNilExperiment
	}
	if r.tx == nil {
		return errNilTx
	}
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return fmt.Errorf("failed to validate experiment name %q: %w", exp.GetName(), err)
	}
	status, err := proto.Marshal(exp.GetStatus())
	if err != nil {
		logging.Error(ctx, "failed to create status update for experiment", "experiment", exp, "error", err)
		return fmt.Errorf("failed to marshal experiment status: %w", err)
	}
	// Like the Spanner mutation, a missing status leaves UpdateTime NULL, which the NOT NULL
	// column rejects.
	var updateTime pgtype.Timestamptz
	if exp.GetStatus() != nil {
		updateTime = pgtype.Timestamptz{Time: exp.GetStatus().GetUpdateTime().AsTime(), Valid: true}
	}
	return execExperimentUpdate(ctx, r.tx, updateExperimentStatusSQL, xid,
		status,                                  // Status
		int64(exp.GetStatus().GetActiveState()), // ActiveState
		int64(exp.GetStatus().GetLaunchState()), // LaunchState
		updateTime,                              // UpdateTime
		nullTimestamptz(exp.GetCompleteTime()),  // CompleteTime
	)
}

// UpdateExperimentResources writes an Experiment's resources and UpdateTime using the bound
// read-write transaction. It fails with a NotFound error when the experiment does not exist.
func (r *TxRepository) UpdateExperimentResources(ctx context.Context, exp *pb.Experiment) error {
	if exp == nil {
		return errNilExperiment
	}
	if r.tx == nil {
		return errNilTx
	}
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return fmt.Errorf("failed to validate experiment name %q: %w", exp.GetName(), err)
	}
	resources, err := marshalMessages(exp.GetResources())
	if err != nil {
		logging.Error(ctx, "failed to create resources update for experiment", "experiment", exp, "error", err)
		return fmt.Errorf("failed to marshal experiment resources: %w", err)
	}
	return execExperimentUpdate(ctx, r.tx, updateExperimentResourcesSQL, xid,
		resources,                    // Resources
		exp.GetUpdateTime().AsTime(), // UpdateTime
	)
}
