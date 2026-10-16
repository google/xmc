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

	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// CreateExperiment orchestrates the creation of a new experiment using the provided transaction runner.
// It returns the experiment proto with initial commit timestamps populated.
func CreateExperiment(ctx context.Context, txRunner storage.TransactionRunner, exp *essmsgpb.Experiment) (*essmsgpb.Experiment, error) {
	if exp == nil {
		return nil, fmt.Errorf("experiment must not be nil")
	}
	ctx = logging.WithExperimentName(ctx, exp.GetName())
	logging.Info(ctx, "Write experiment to storage", "experiment", exp)

	// 1. Validate the experiment name and extract the XID.
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return nil, err
	}

	err = txRunner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		if exp.Status == nil {
			exp.Status = &essmsgpb.ExperimentStatus{}
		}
		// 2. Set the timestamps to the current time.
		now := timestamppb.New(time.Now())
		exp.CreateTime = now
		exp.UpdateTime = now
		exp.Status.LastChildStatusUpdateTime = now
		exp.Status.UpdateTime = now

		// 3. Insert the experiment via the transaction-scoped repository.
		if err := tx.Experiments.InsertExperiment(ctx, exp); err != nil {
			logging.Error(ctx, "failed to insert experiment", "experiment", exp, "error", err)
			return err
		}

		// 4. Record the initial status message history.
		if err := tx.StatusMessages.SaveStatusMessageHistoryIfChanged(ctx, xid, nil, nil, nil, exp.GetStatus().GetStructuredStatusMessage()); err != nil {
			logging.Error(ctx, "failed to save status message history for experiment", "xid", xid, "error", err)
			return err
		}

		return nil
	})
	if err != nil {
		logging.Error(ctx, "failed to create experiment", "error", err)
		return nil, err
	}

	logging.Info(ctx, "Written experiment to storage")
	return exp, nil
}
