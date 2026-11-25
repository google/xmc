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

// validateUpdatedPlotTypes checks that an update replacing the plots keeps plot types that fit the
// level of the stored chart: only PARALLEL_COORDINATES on experiment-level charts, none on work unit
// charts.
func validateUpdatedPlotTypes(existing *dpb.Chart, req *dpb.UpdateChartRequest) error {
	paths := req.GetUpdateMask().GetPaths()
	if len(paths) > 0 && !slices.Contains(paths, "plots") {
		return nil
	}
	return validator.ValidateChartPlotTypes(req.GetChart().GetPlots(), validator.IsExperimentLevelChart(existing))
}

func (s *Server) persistScopedChartUpdate(ctx context.Context, req *dpb.UpdateChartRequest, chartID string, targetXID, existingWid int64) (*dpb.Chart, error) {
	// Pass the authorized (Xid, Wid) scope in Name while leaving mutable fields untouched for the
	// storage transaction's masked merge (which re-verifies both scope and plot level in-transaction).
	scopedChart := proto.Clone(req.GetChart()).(*dpb.Chart)
	scopedChart.Name = validator.ConstructCanonicalChartName(chartID, targetXID, existingWid)
	updated, err := s.repo.UpdateChart(ctx, scopedChart, req.GetUpdateMask())
	if err == nil {
		return updated, nil
	}
	if isNotFoundError(err) {
		return nil, status.Errorf(codes.NotFound, "chart %q not found", req.GetChart().GetName())
	}
	if status.Code(err) == codes.InvalidArgument {
		return nil, status.Errorf(codes.InvalidArgument, "invalid update chart request: %v", err)
	}
	return nil, status.Errorf(codes.Internal, "failed to update chart: %v", err)
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
	if s.repo == nil {
		return nil, status.Error(codes.FailedPrecondition, "server repository not initialized")
	}

	chartID, _ := validator.ValidateRootChartName(req.GetChart().GetName())
	existing, targetXID, err := s.authorizeChartWrite(ctx, user, req.GetChart().GetName(), chartID)
	if err != nil {
		return nil, err
	}
	if err := validateUpdatedPlotTypes(existing, req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid update chart request: %v", err)
	}
	return s.persistScopedChartUpdate(ctx, req, chartID, targetXID, existing.GetWid())
}
