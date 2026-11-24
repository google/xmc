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

package server

import (
	"context"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/validator"
)

// authorizeChartWrite verifies caller write authorization on the experiment a chart belongs to and
// returns the stored chart alongside the authorized experiment XID.
//
// The pre-transaction snapshot establishes the authorized (Xid, Wid) scope, and UpdateChart stamps
// that canonical scope onto the chart name passed to storage so MergeCharts rejects any concurrent
// DeleteChart + CreateChart rebind inside the write transaction.
func (s *Server) authorizeChartWrite(ctx context.Context, user, chartName, chartID string) (*dpb.Chart, int64, error) {
	existing, err := s.repo.GetChart(ctx, chartID)
	if err != nil {
		if isNotFoundError(err) {
			return nil, 0, status.Errorf(codes.NotFound, "chart %q not found", chartName)
		}
		return nil, 0, status.Errorf(codes.Internal, "failed to get chart: %v", err)
	}
	if existing == nil {
		return nil, 0, status.Errorf(codes.Internal, "repository returned nil chart for %q", chartName)
	}

	targetXID := existing.GetXid()
	if targetXID <= 0 {
		return nil, 0, status.Errorf(codes.FailedPrecondition, "chart %q is missing a valid experiment XID (%d)", chartName, targetXID)
	}

	if err := s.authorizeExperimentWrite(ctx, user, targetXID); err != nil {
		return nil, 0, err
	}
	return existing, targetXID, nil
}

func scopedChartName(chartID string, xid, wid int64) string {
	if wid == 0 {
		return validator.ConstructExperimentChartName(xid, chartID)
	}
	return validator.ConstructWorkUnitChartName(xid, wid, chartID)
}

// validateUpdatedPlotTypes checks that an update replacing the plots keeps plot types that fit the
// level of the stored chart: only PARALLEL_COORDINATES on experiment-level charts, none on work unit
// charts.
func validateUpdatedPlotTypes(existing *dpb.Chart, req *dpb.UpdateChartRequest) error {
	paths := req.GetUpdateMask().GetPaths()
	if len(paths) > 0 && !slices.Contains(paths, "plots") {
		return nil
	}
	experimentLevel := existing.GetXid() > 0 && existing.GetWid() == 0
	return validator.ValidateChartPlotTypes(req.GetChart().GetPlots(), experimentLevel)
}

// UpdateChart handles updating an existing chart with authorization, field mask merging, and persistence.
func (s *Server) UpdateChart(ctx context.Context, req *dpb.UpdateChartRequest) (*dpb.Chart, error) {
	user, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to authenticate user: %v", err)
	}

	if err := validator.ValidateUpdateChartRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid update chart request: %v", err)
	}

	chartID, err := validator.ValidateRootChartName(req.GetChart().GetName())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid chart name: %v", err)
	}

	if s.repo == nil {
		return nil, status.Error(codes.FailedPrecondition, "server repository not initialized")
	}

	existing, targetXID, err := s.authorizeChartWrite(ctx, user, req.GetChart().GetName(), chartID)
	if err != nil {
		return nil, err
	}
	if err := validateUpdatedPlotTypes(existing, req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid update chart request: %v", err)
	}

	// Pass the authorized (Xid, Wid) scope in Name while leaving mutable fields untouched for the
	// storage transaction's masked merge (which re-verifies both scope and plot level in-transaction).
	scopedChart := proto.Clone(req.GetChart()).(*dpb.Chart)
	scopedChart.Name = scopedChartName(chartID, targetXID, existing.GetWid())
	updated, err := s.repo.UpdateChart(ctx, scopedChart, req.GetUpdateMask())
	if err != nil {
		if isNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound, "chart %q not found", req.GetChart().GetName())
		}
		if status.Code(err) == codes.InvalidArgument {
			return nil, status.Errorf(codes.InvalidArgument, "invalid update chart request: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to update chart: %v", err)
	}

	return updated, nil
}
