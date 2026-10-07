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

package experiment

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	"golang/godebug/diff/diff"
	"golang/protobuf/v2/encoding/prototext/prototext"
	"golang/protobuf/v2/proto/proto"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
)

// TODO(b/517479143): Refactor UpdateExperiment to extract experiment merging and status message persistence to a higher domain service layer.
// UpdateExperiment updates an experiment with the given update mask. We return both the original
// and updated experiments, so that the caller can choose whether to perform additional actions
// based on the original experiment state.
func (r *Repository) UpdateExperiment(ctx context.Context, update *storage.ExperimentUpdate) (storage.UpdatedExperiments, error) {
	if update == nil {
		return storage.UpdatedExperiments{}, fmt.Errorf("update must not be nil")
	}
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(update.XID))
	logging.Info(ctx, "Update experiment in storage", "update", update.Experiment)

	var originalExperiment, updatedExperiment *essmsgpb.Experiment
	var mutated bool
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		mutated = false

		// 1. Read the existing experiment from storage.
		var err error
		originalExperiment, err = r.FetchExperimentTransaction(ctx, update.XID, txn)
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
		now := time.Now()
		updatedExperiment.UpdateTime = timestamppb.New(now)

		// 3. Construct the Spanner mutation for the update.
		mutation, err := spannerdata.ExperimentProtoToUpdateMutation(updatedExperiment)
		if err != nil {
			return fmt.Errorf("failed to create Spanner mutation for Experiment: %w", err)
		}

		// 4. Apply the mutation to the transaction.
		mutated = true

		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, update.XID, nil, nil, originalExperiment.GetStatus().GetStructuredStatusMessage(), updatedExperiment.GetStatus().GetStructuredStatusMessage(), txn)

		return txn.BufferWrite([]*spanner.Mutation{mutation})
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

// UpdateExperimentStatus buffers a mutation to update an Experiment's status in Spanner using the bound read-write transaction.
func (r *TxRepository) UpdateExperimentStatus(ctx context.Context, exp *essmsgpb.Experiment) error {
	if exp == nil {
		return fmt.Errorf("experiment must not be nil")
	}
	expMutation, err := spannerdata.ExperimentProtoToStatusUpdateMutation(exp)
	if err != nil {
		logging.Error(ctx, "failed to create status mutation for experiment", "experiment", exp, "error", err)
		return err
	}
	return r.tx.BufferWrite([]*spanner.Mutation{expMutation})
}

// UpdateExperimentResources buffers a mutation to update an Experiment's resources in Spanner.
func (r *Repository) UpdateExperimentResources(ctx context.Context, tx *spanner.ReadWriteTransaction, exp *essmsgpb.Experiment) error {
	if exp == nil {
		return fmt.Errorf("experiment must not be nil")
	}
	if tx == nil {
		return fmt.Errorf("transaction must not be nil")
	}
	expResourcesMutation, err := spannerdata.ExperimentProtoToResourcesUpdateMutation(exp)
	if err != nil {
		logging.Error(ctx, "failed to create resources mutation for experiment", "experiment", exp, "error", err)
		return err
	}
	return tx.BufferWrite([]*spanner.Mutation{expResourcesMutation})
}
