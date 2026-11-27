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

package worker

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
)

// ExperimentReader fetches an experiment from the Experiment State Server.
type ExperimentReader interface {
	GetExperiment(ctx context.Context, name string) (*spb.Experiment, error)
}

// NewESSReconciler returns a ReconcileFunc that re-reads each ticked experiment from ESS using
// reader and gates reconciliation on experiment_type == STUDY and status.active_state == ACTIVE.
func NewESSReconciler(reader ExperimentReader) (ReconcileFunc, error) {
	if reader == nil {
		return nil, errors.New("experiment reader must not be nil")
	}
	return func(ctx context.Context, xid int64) error {
		name := validator.ConstructExperimentName(xid)
		exp, err := reader.GetExperiment(ctx, name)
		if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
			logging.Info(ctx, "Skipping study reconcile tick: experiment not found")
			return nil
		}
		if err != nil {
			return fmt.Errorf("get experiment %s: %w", name, err)
		}
		if exp == nil {
			return fmt.Errorf("get experiment %s: received nil experiment", name)
		}
		if exp.GetExperimentType() != spb.Experiment_STUDY {
			logging.Info(ctx, "Skipping study reconcile tick: experiment is not a STUDY",
				"experiment_type", exp.GetExperimentType().String())
			return nil
		}
		if exp.GetStatus().GetActiveState() != spb.ExperimentStatus_ACTIVE {
			logging.Info(ctx, "Skipping study reconcile tick: study is not active",
				"active_state", exp.GetStatus().GetActiveState().String(),
				"launch_state", exp.GetStatus().GetLaunchState().String())
			return nil
		}
		logging.Info(ctx, "Verified active study on ESS",
			"experiment_type", exp.GetExperimentType().String(),
			"active_state", exp.GetStatus().GetActiveState().String(),
			"launch_state", exp.GetStatus().GetLaunchState().String(),
			"algorithm", exp.GetStudyConfig().GetAlgorithm(),
			"max_trial_budget", exp.GetStudyConfig().GetMaxTrialBudget())
		return nil
	}, nil
}
