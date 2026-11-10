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

	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
	"xmanager_cloud/internal/workunit"
)

// CreateExperiment orchestrates the creation of a new experiment using the provided transaction runner.
// In the same transaction, it checks the components that the experiment requires, caps a study's
// trial budget at the limit of the resolved hpo component, inserts the experiment, and pins the
// components to it. It returns an error wrapping ErrComponentRequirementNotMet when a required
// component is missing or not ready, and otherwise the created experiment proto with initial commit
// timestamps populated.
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

	reqs := RequirementsFor(exp)
	// Each attempt restarts from the requested status and study config, so that a retried
	// transaction neither clamps twice nor keeps a warning computed from a stale component record.
	// Only these fields are restored instead of cloning the whole experiment, because proto.Clone
	// turns empty lists into nil and the Spanner converter writes nil lists of NOT NULL columns
	// (such as Experiments.Tags) as NULL.
	// TODO: normalize nil lists to empty ones in the Spanner experiment converter, then clone exp.
	requestedStatus := proto.Clone(exp.GetStatus()).(*essmsgpb.ExperimentStatus)
	requestedStudyConfig := proto.Clone(exp.GetStudyConfig()).(*essmsgpb.StudyConfig)
	err = runCreateTransaction(ctx, txRunner, len(reqs) > 0, func(ctx context.Context, tx *storage.Transaction) error {
		exp.Status = proto.Clone(requestedStatus).(*essmsgpb.ExperimentStatus)
		setStudyConfig(exp, proto.Clone(requestedStudyConfig).(*essmsgpb.StudyConfig))
		return insertExperiment(ctx, tx, xid, exp, reqs)
	})
	if err != nil {
		// Leave the caller's status and study config as requested, not as the failed attempt changed
		// them. Creation timestamps stay set, as before.
		exp.Status = requestedStatus
		setStudyConfig(exp, requestedStudyConfig)
		logging.Error(ctx, "failed to create experiment", "error", err)
		return nil, err
	}
	logStudyTrialBudgetClamp(ctx, requestedStudyConfig, exp.GetStudyConfig())

	logging.Info(ctx, "Written experiment to storage")
	return exp, nil
}

// insertExperiment resolves reqs, applies the study trial budget limit, inserts exp, pins the
// resolved components, and records the initial status message history, all through tx. The
// components are pinned after the experiment row that their snapshots reference.
func insertExperiment(ctx context.Context, tx *storage.Transaction, xid int64, exp *essmsgpb.Experiment, reqs []Requirement) error {
	recs, err := ResolveComponentRequirements(ctx, tx.Components, reqs)
	if err != nil {
		return err
	}
	initCreationTimestamps(exp)
	clampStudyTrialBudget(ctx, exp, recs)
	if err := tx.Experiments.InsertExperiment(ctx, exp); err != nil {
		logging.Error(ctx, "failed to insert experiment", "experiment", exp, "error", err)
		return err
	}
	if err := PinComponentRecords(ctx, tx.Components, xid, recs); err != nil {
		return err
	}
	if err := workunit.RecordStatusMessageHistory(ctx, tx.StatusMessages, xid, nil, nil, nil, exp.GetStatus().GetStructuredStatusMessage()); err != nil {
		return fmt.Errorf("failed to save status message history for experiment: %w", err)
	}
	return nil
}

// setStudyConfig sets the type_config of exp to cfg, or clears it when cfg is nil.
func setStudyConfig(exp *essmsgpb.Experiment, cfg *essmsgpb.StudyConfig) {
	if cfg == nil {
		exp.TypeConfig = nil
		return
	}
	exp.TypeConfig = &essmsgpb.Experiment_StudyConfig{StudyConfig: cfg}
}

// initCreationTimestamps sets the creation timestamps of exp and its status to the current time.
func initCreationTimestamps(exp *essmsgpb.Experiment) {
	if exp.Status == nil {
		exp.Status = &essmsgpb.ExperimentStatus{}
	}
	now := timestamppb.New(time.Now())
	exp.CreateTime = now
	exp.UpdateTime = now
	exp.Status.LastChildStatusUpdateTime = now
	exp.Status.UpdateTime = now
}

// runCreateTransaction runs fn in a read-write transaction, a serializable one when the experiment
// pins components, so that a concurrent catalog write conflicts with the check instead of committing
// between the check and the pin. Spanner read-write transactions are always serializable, so the
// choice only matters for PostgreSQL.
func runCreateTransaction(ctx context.Context, txRunner storage.TransactionRunner, serializable bool, fn func(ctx context.Context, tx *storage.Transaction) error) error {
	if !serializable {
		return txRunner.RunInTransaction(ctx, fn)
	}
	_, err := txRunner.RunInSerializableTransaction(ctx, fn)
	return err
}
