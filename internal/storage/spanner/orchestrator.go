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

package spanner

import (
	"context"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/domain/artifactdomain"
	"xmanager_cloud/internal/domain/experimentdomain"
	"xmanager_cloud/internal/domain/workunitdomain"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
)

// CreateExperiment creates a new experiment in Spanner using the thin repository and pure domain layer.
func (s *Storage) CreateExperiment(ctx context.Context, exp *essmsgpb.Experiment) (*essmsgpb.Experiment, error) {
	if exp == nil {
		return nil, fmt.Errorf("experiment must not be nil: %w", status.ErrInvalidArgument)
	}
	ctx = logging.WithExperimentName(ctx, exp.GetName())
	logging.Info(ctx, "Write experiment to storage", "experiment", exp)

	xid, err := experimentdomain.PrepareExperimentCreation(ctx, exp, time.Now())
	if err != nil {
		return nil, err
	}

	_, err = s.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		if err := s.ExperimentRepository.InsertExperiment(ctx, tx, exp); err != nil {
			logging.Error(ctx, "failed to create mutation for experiment", "experiment", exp, "error", err)
			return err
		}

		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, s.ExperimentRepository.StatusMessageHistoryEnabled(), xid, nil, nil, nil, exp.GetStatus().GetStructuredStatusMessage(), tx)
		return nil
	})

	if err != nil {
		logging.Error(ctx, "failed to create experiment", "error", err)
		return nil, err
	}

	logging.Info(ctx, "Written experiment to storage")
	return exp, nil
}

// CreateWorkUnit creates a new work unit in Spanner using the thin repository and pure domain layer.
func (s *Storage) CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	if wu == nil {
		return nil, fmt.Errorf("work unit must not be nil: %w", status.ErrInvalidArgument)
	}
	ctx = logging.WithWorkUnitExecutionName(ctx, wu.GetCurrentWorkUnitExecution().GetName())
	logging.Info(ctx, "Write work unit to storage", "work_unit", wu)

	exp := &essmsgpb.Experiment{}
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		now := time.Now()

		xid, wid, err := validator.ValidateWorkUnitNameAllowZeroWID(wu.GetName())
		if err != nil {
			return fmt.Errorf("failed to validate work unit name: %w (%v)", status.ErrInvalidArgument, err)
		}

		initialEID := int64(1)
		if wid == 0 {
			wid, err = s.WorkUnitRepository.FetchNextWorkUnitID(ctx, tx, xid)
			if err != nil {
				return fmt.Errorf("failed to fetch next work unit ID: %w", err)
			}
		}

		if err := workunitdomain.PrepareWorkUnit(ctx, wu, xid, wid, initialEID, now); err != nil {
			return err
		}

		if err := s.WorkUnitRepository.InsertWorkUnit(ctx, tx, wu); err != nil {
			logging.Error(ctx, "failed to buffer write mutation for create work unit", "error", err)
			return err
		}

		exp, err = s.ExperimentRepository.GetExperiment(ctx, tx, xid)
		if err != nil {
			return fmt.Errorf("failed to fetch experiment: %w", err)
		}
		if exp == nil {
			return fmt.Errorf("experiment %d not found: %w", xid, status.ErrNotFound)
		}

		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, s.WorkUnitRepository.StatusMessageHistoryEnabled(), xid, &wid, &initialEID, nil, wu.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage(), tx)

		updatedExp, resourcesChanged, err := experimentdomain.PrepareWorkUnitCreation(ctx, exp, wu, now)
		if err != nil {
			return err
		}
		exp = updatedExp

		if err := s.ExperimentRepository.UpdateExperimentStatus(ctx, tx, updatedExp); err != nil {
			logging.Error(ctx, "failed to create mutation for experiment", "experiment", updatedExp, "error", err)
			return err
		}

		if resourcesChanged {
			if err := s.ExperimentRepository.UpdateExperimentResources(ctx, tx, updatedExp); err != nil {
				logging.Error(ctx, "failed to create mutation for experiment", "experiment", updatedExp, "error", err)
				return err
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

// CreateArtifact creates a new artifact in Spanner using the thin repository and pure domain layer.
func (s *Storage) CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error) {
	if artifact == nil {
		return nil, fmt.Errorf("artifact must not be nil: %w", status.ErrInvalidArgument)
	}
	ctx = logging.WithArtifactName(ctx, artifact.GetName())
	logging.Info(ctx, "Write artifact to storage", "artifact", artifact)

	if err := artifactdomain.PrepareArtifactCreation(ctx, artifact, time.Now()); err != nil {
		return nil, err
	}

	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		if err := s.ArtifactRepository.InsertArtifact(ctx, tx, artifact); err != nil {
			logging.Error(ctx, "Failed to create mutation for artifact", "artifact", artifact, "error", err)
			return err
		}
		return nil
	})
	if err != nil {
		logging.Error(ctx, "Failed to create artifact", "error", err)
		return nil, err
	}

	logging.Info(ctx, "Written artifact to storage")
	return artifact, nil
}
