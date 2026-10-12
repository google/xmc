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
	"xmanager_cloud/internal/validator"
)

// GetChart retrieves a chart by its resource name after validating experiment read permissions.
func (s *Server) GetChart(ctx context.Context, req *spb.GetChartRequest) (*dpb.Chart, error) {
	user, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to authenticate user: %v", err)
	}

	if err := validator.ValidateGetChartRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid get chart request: %v", err)
	}

	chartID, _ := validator.ValidateRootChartName(req.GetName())

	if s.repo == nil {
		return nil, status.Error(codes.FailedPrecondition, "server repository not initialized")
	}

	chart, err := s.repo.GetChart(ctx, chartID)
	if err != nil {
		if isNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound, "chart %q not found", req.GetName())
		}
		return nil, status.Errorf(codes.Internal, "failed to get chart: %v", err)
	}

	if err := s.authorizeExperimentRead(ctx, user, chart.GetXid()); err != nil {
		return nil, err
	}

	return chart, nil
}
