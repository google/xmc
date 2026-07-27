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
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/validator/validator"
)

// CreateExperiment creates a new experiment in Spanner.
func (r *Repository) CreateExperiment(ctx context.Context, exp *essmsgpb.Experiment) (*essmsgpb.Experiment, error) {
	ctx = logging.WithExperimentName(ctx, exp.GetName())
	logging.Info(ctx, "Write experiment to storage", "experiment", exp)

	xid, err := validator.ValidateExperimentName(exp.Name)
	if err != nil {
		return nil, err
	}

	_, err = r.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		now := timestamppb.New(time.Now())
		exp.CreateTime = now
		exp.UpdateTime = now
		exp.Status.LastChildStatusUpdateTime = now
		exp.Status.UpdateTime = now

		expMutation, err := spannerdata.ExperimentProtoToInsertMutation(exp)
		if err != nil {
			logging.Error(ctx, "failed to create mutation for experiment", "experiment", exp, "error", err)
			return err
		}

		err = tx.BufferWrite([]*spanner.Mutation{expMutation})
		if err != nil {
			logging.Error(ctx, "failed to buffer write mutation for create experiment", "error", err)
			return err
		}

		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, r.statusMessageHistoryEnabled, xid, nil, nil, nil, exp.GetStatus().GetStructuredStatusMessage(), tx)

		return nil
	})

	if err != nil {
		logging.Error(ctx, "failed to create experiment", "error", err)
		return nil, err
	}

	logging.Info(ctx, "Written experiment to storage")

	return exp, nil
}
