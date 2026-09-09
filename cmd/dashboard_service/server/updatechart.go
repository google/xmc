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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/validator/validator"
)

// authorizeChartWrite verifies caller write authorization on the experiment a chart belongs to.
//
// The chart is read only to learn that experiment. The update itself re-reads it inside its own
// transaction, so nothing from this snapshot is carried into the write.
func (s *Server) authorizeChartWrite(ctx context.Context, user, chartName, chartID string) error {
	existing, err := s.repo.GetChart(ctx, chartID)
	if err != nil {
		if isNotFoundError(err) {
			return status.Errorf(codes.NotFound, "chart %q not found", chartName)
		}
		return status.Errorf(codes.Internal, "failed to get chart: %v", err)
	}
	if existing == nil {
		return status.Errorf(codes.Internal, "repository returned nil chart for %q", chartName)
	}

	targetXID := s.resolveChartXID(existing, chartName)
	if targetXID <= 0 {
		return status.Errorf(codes.FailedPrecondition, "chart %q is missing a valid experiment XID (%d)", chartName, targetXID)
	}

	return s.authorizeExperimentWrite(ctx, user, targetXID)
}

// UpdateChart handles updating an existing chart with authorization, field mask merging, and persistence.
func (s *Server) UpdateChart(ctx context.Context, req *spb.UpdateChartRequest) (*dpb.Chart, error) {
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

	if err := s.authorizeChartWrite(ctx, user, req.GetChart().GetName(), chartID); err != nil {
		return nil, err
	}

	// The field mask is handed to the repository rather than applied here: the merge has to run
	// against the state the write commits against, which only the storage transaction can see.
	updated, err := s.repo.UpdateChart(ctx, req.GetChart(), req.GetUpdateMask())
	if err != nil {
		if isNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound, "chart %q not found", req.GetChart().GetName())
		}
		return nil, status.Errorf(codes.Internal, "failed to update chart: %v", err)
	}

	return updated, nil
}
