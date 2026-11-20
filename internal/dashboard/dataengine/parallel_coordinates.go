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

package dataengine

import (
	"context"
	"fmt"
	"time"

	dpb "xmanager_cloud/api/protos/dashboard"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/validator"
)

// workUnitPageSize is the page size used to read every work unit of a study.
const workUnitPageSize = 500

// StudyReader reads the study experiments and work units that PARALLEL_COORDINATES plots are drawn
// from. It is the subset of the experiment store (storage.Storage) that the engine needs.
type StudyReader interface {
	FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error)
	ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error)
}

// fetchParallelCoordinatesData builds a PARALLEL_COORDINATES plot of a study: one axis per
// parameter of its study configuration, its objective metric, and one trajectory per work unit.
// The trajectories carry no parameter or objective values yet.
func (e *DataEngine) fetchParallelCoordinatesData(ctx context.Context, xid int64, plot *dpb.PlotSpec) ([]*dpb.ChartData_PlotData, error) {
	if e.studies == nil {
		return nil, fmt.Errorf("the study reader is not configured: %w", grpcerr.ErrFailedPrecondition)
	}
	config, err := fetchStudyConfig(ctx, e.studies, xid)
	if err != nil {
		return nil, err
	}
	trajectories, err := listRunTrajectories(ctx, e.studies, xid)
	if err != nil {
		return nil, err
	}
	return []*dpb.ChartData_PlotData{{
		PlotId: plot.GetPlotId(),
		Type:   dpb.PlotType_PARALLEL_COORDINATES,
		Content: &dpb.ChartData_PlotData_ParallelCoordinatesData{ParallelCoordinatesData: &dpb.ParallelCoordinatesData{
			Axes:         axesFromStudyConfig(config),
			Objective:    objectiveFromStudyConfig(config),
			Trajectories: trajectories,
		}},
	}}, nil
}

// fetchStudyConfig reads the study configuration of a STUDY experiment.
func fetchStudyConfig(ctx context.Context, studies StudyReader, xid int64) (*essmsgpb.StudyConfig, error) {
	experiment, err := studies.FetchExperiment(ctx, xid)
	if err != nil {
		return nil, fmt.Errorf("failed to read experiment %d: %w", xid, err)
	}
	if experiment.GetExperimentType() != essmsgpb.Experiment_STUDY || experiment.GetStudyConfig() == nil {
		return nil, fmt.Errorf("experiment %d is not a study with a study configuration: %w", xid, grpcerr.ErrFailedPrecondition)
	}
	return experiment.GetStudyConfig(), nil
}

// listRunTrajectories reads every work unit of the experiment, following the page tokens to the
// end, and returns their trajectories in the store's order (ascending work unit ID). The result is
// not capped: a study's work unit count is bounded by its max_trial_budget, which ESS clamps to the
// hpo component's maxTrialBudget.
func listRunTrajectories(ctx context.Context, studies StudyReader, xid int64) ([]*dpb.ParallelCoordinatesData_RunTrajectory, error) {
	parent := validator.ConstructExperimentName(xid)
	trajectories := []*dpb.ParallelCoordinatesData_RunTrajectory{}
	pageToken := ""
	for {
		workUnits, nextPageToken, err := studies.ListWorkUnits(ctx, parent, workUnitPageSize, pageToken, "", "", 0)
		if err != nil {
			return nil, fmt.Errorf("failed to list the work units of experiment %d: %w", xid, err)
		}
		if trajectories, err = appendRunTrajectories(trajectories, workUnits); err != nil {
			return nil, err
		}
		if nextPageToken == "" {
			return trajectories, nil
		}
		// A store that hands back the token it was given would make this loop spin forever.
		if nextPageToken == pageToken {
			return nil, fmt.Errorf("listing the work units of experiment %d returned the same page token twice: %w", xid, grpcerr.ErrInternal)
		}
		pageToken = nextPageToken
	}
}

// appendRunTrajectories appends the trajectories of a page of work units.
func appendRunTrajectories(trajectories []*dpb.ParallelCoordinatesData_RunTrajectory, workUnits []*essmsgpb.WorkUnit) ([]*dpb.ParallelCoordinatesData_RunTrajectory, error) {
	for _, workUnit := range workUnits {
		trajectory, err := runTrajectory(workUnit)
		if err != nil {
			return nil, err
		}
		trajectories = append(trajectories, trajectory)
	}
	return trajectories, nil
}

// runTrajectory describes one work unit: its ID, the state of its current execution, its creation
// time, and the time its current execution completed (unset until then).
func runTrajectory(workUnit *essmsgpb.WorkUnit) (*dpb.ParallelCoordinatesData_RunTrajectory, error) {
	_, wid, err := validator.ValidateWorkUnitName(workUnit.GetName())
	if err != nil {
		// A malformed name read back from storage is a server fault, not a caller error.
		return nil, fmt.Errorf("stored work unit %q has an invalid name (%v): %w", workUnit.GetName(), err, grpcerr.ErrInternal)
	}
	execution := workUnit.GetCurrentWorkUnitExecution()
	return &dpb.ParallelCoordinatesData_RunTrajectory{
		Wid:          wid,
		State:        dpb.ParallelCoordinatesData_RunTrajectory_WorkUnitState(execution.GetStatus().GetState()),
		CreateTime:   workUnit.GetCreateTime(),
		CompleteTime: execution.GetCompleteTime(),
	}, nil
}
