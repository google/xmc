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
	"golang/protobuf/v2/proto/proto"
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

	var committedExp *essmsgpb.Experiment
	xid, err := experimentdomain.PrepareExperimentCreation(ctx, exp, time.Now())
	if err != nil {
		return nil, err
	}

	_, err = s.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		localExp := proto.Clone(exp).(*essmsgpb.Experiment)
		if err := s.ExperimentRepository.InsertExperiment(ctx, tx, localExp); err != nil {
			logging.Error(ctx, "failed to create mutation for experiment", "experiment", localExp, "error", err)
			return err
		}

		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, s.ExperimentRepository.StatusMessageHistoryEnabled(), xid, nil, nil, nil, localExp.GetStatus().GetStructuredStatusMessage(), tx)
		committedExp = localExp
		return nil
	})

	if err != nil {
		logging.Error(ctx, "failed to create experiment", "error", err)
		return nil, err
	}

	exp.Reset()
	proto.Merge(exp, committedExp)
	preserveEmptySlicesExperiment(exp)
	logging.Info(ctx, "Written experiment to storage", "experiment", exp)
	return exp, nil
}

// CreateWorkUnit creates a new work unit in Spanner using the thin repository and pure domain layer.
func (s *Storage) CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	if wu == nil {
		return nil, fmt.Errorf("work unit must not be nil: %w", status.ErrInvalidArgument)
	}
	ctx = logging.WithWorkUnitExecutionName(ctx, wu.GetCurrentWorkUnitExecution().GetName())
	logging.Info(ctx, "Write work unit to storage", "work_unit", wu)

	var committedWU *essmsgpb.WorkUnit
	var committedExp *essmsgpb.Experiment
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		now := time.Now()
		localWU := proto.Clone(wu).(*essmsgpb.WorkUnit)

		xid, wid, err := validator.ValidateWorkUnitNameAllowZeroWID(localWU.GetName())
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

		if err := workunitdomain.PrepareWorkUnit(ctx, localWU, xid, wid, initialEID, now); err != nil {
			return err
		}

		if err := s.WorkUnitRepository.InsertWorkUnit(ctx, tx, localWU); err != nil {
			logging.Error(ctx, "failed to buffer write mutation for create work unit", "error", err)
			return err
		}

		localExp, err := s.ExperimentRepository.GetExperiment(ctx, tx, xid)
		if err != nil {
			return fmt.Errorf("failed to fetch experiment: %w", err)
		}
		if localExp == nil {
			return fmt.Errorf("experiment %d not found: %w", xid, status.ErrNotFound)
		}

		statusmessage.SaveStatusMessageHistoryIfChanged(ctx, s.WorkUnitRepository.StatusMessageHistoryEnabled(), xid, &wid, &initialEID, nil, localWU.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage(), tx)

		updatedExp, resourcesChanged, err := experimentdomain.PrepareWorkUnitCreation(ctx, localExp, localWU, now)
		if err != nil {
			return err
		}

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

		committedWU = localWU
		committedExp = updatedExp
		return nil
	})
	if err != nil {
		logging.Error(ctx, "failed to create work unit", "work_unit", wu, "error", err)
		return nil, err
	}

	wu.Reset()
	proto.Merge(wu, committedWU)
	preserveEmptySlicesWorkUnit(wu)

	logging.Info(ctx, "Written work unit to storage", "work_unit", committedWU)
	logging.Info(ctx, "Updated experiment successfully", "experiment", committedExp)
	return wu, nil
}

// CreateArtifact creates a new artifact in Spanner using the thin repository and pure domain layer.
func (s *Storage) CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error) {
	if artifact == nil {
		return nil, fmt.Errorf("artifact must not be nil: %w", status.ErrInvalidArgument)
	}
	ctx = logging.WithArtifactName(ctx, artifact.GetName())
	logging.Info(ctx, "Write artifact to storage", "artifact", artifact)

	var committedArtifact *essmsgpb.Artifact
	if err := artifactdomain.PrepareArtifactCreation(ctx, artifact, time.Now()); err != nil {
		return nil, err
	}

	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		localArtifact := proto.Clone(artifact).(*essmsgpb.Artifact)
		if err := s.ArtifactRepository.InsertArtifact(ctx, tx, localArtifact); err != nil {
			logging.Error(ctx, "Failed to create mutation for artifact", "artifact", localArtifact, "error", err)
			return err
		}
		committedArtifact = localArtifact
		return nil
	})
	if err != nil {
		logging.Error(ctx, "Failed to create artifact", "error", err)
		return nil, err
	}

	artifact.Reset()
	proto.Merge(artifact, committedArtifact)
	preserveEmptySlicesArtifact(artifact)

	logging.Info(ctx, "Written artifact to storage")
	return artifact, nil
}

func preserveEmptySlicesExperiment(exp *essmsgpb.Experiment) {
	if exp != nil {
		if exp.GetUrls() == nil {
			exp.Urls = []*essmsgpb.Url{}
		}
		if exp.GetTags() == nil {
			exp.Tags = []string{}
		}
		if exp.GetResources() == nil {
			exp.Resources = []*essmsgpb.ResourceSet{}
		}
	}
}

func preserveEmptySlicesWorkUnit(wu *essmsgpb.WorkUnit) {
	if wu != nil {
		if wu.GetUrls() == nil {
			wu.Urls = []*essmsgpb.Url{}
		}
		if wu.GetTags() == nil {
			wu.Tags = []string{}
		}
	}
}

func preserveEmptySlicesArtifact(art *essmsgpb.Artifact) {
	if art != nil {
		if art.GetTags() == nil {
			art.Tags = []string{}
		}
	}
}
