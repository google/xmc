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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
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

// UpdateExperiment updates an experiment with the given update mask.
func (r *Repository) UpdateExperiment(ctx context.Context, update *storage.ExperimentUpdate) (*essmsgpb.Experiment, *essmsgpb.Experiment, error) {
	if update == nil {
		return nil, nil, fmt.Errorf("update must not be nil")
	}
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(update.XID))
	logging.Info(ctx, "Update experiment in storage", "update", update.Experiment)

	var originalExperiment *essmsgpb.Experiment
	var updatedExperiment *essmsgpb.Experiment
	var mutated bool
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		mutated = false

		var err error
		originalExperiment, err = r.FetchExperimentTransaction(ctx, update.XID, txn)
		if err != nil {
			return err
		}

		updatedExperiment, err = data.MergeExperiments(originalExperiment, update.Experiment, update.UpdateMask)
		if err != nil {
			return fmt.Errorf("failed to merge Experiment: %w", err)
		}

		if proto.Equal(originalExperiment, updatedExperiment) {
			return nil
		}

		now := time.Now()
		updatedExperiment.UpdateTime = timestamppb.New(now)

		mutation, err := spannerdata.ExperimentProtoToUpdateMutation(updatedExperiment)
		if err != nil {
			return fmt.Errorf("failed to create Spanner mutation for Experiment: %w", err)
		}

		mutated = true

		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, r.statusMessageHistoryEnabled, update.XID, nil, nil, originalExperiment.GetStatus().GetStructuredStatusMessage(), updatedExperiment.GetStatus().GetStructuredStatusMessage(), txn)

		return txn.BufferWrite([]*spanner.Mutation{mutation})
	})

	if err != nil {
		return nil, nil, err
	}

	if mutated {
		protoDiff := diff.Diff(prototext.Format(originalExperiment), prototext.Format(updatedExperiment))
		logging.Info(ctx, fmt.Sprintf("Updated experiment in storage: %s", protoDiff), "updatedExperiment", updatedExperiment)
	} else {
		logging.Debug(ctx, "Experiment not updated; no changes detected", "updatedExperiment", updatedExperiment)
	}

	return originalExperiment, updatedExperiment, nil
}
