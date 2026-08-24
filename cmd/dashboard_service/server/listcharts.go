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

const (
	// defaultChartPageSize is the page size applied when the caller omits page_size (AIP-158).
	defaultChartPageSize int32 = 10
	// maxChartPageSize caps the page size a caller may request (AIP-158).
	maxChartPageSize int32 = 100
)

// ListCharts retrieves a paginated list of charts under a dashboard or work unit parent (AIP-132).
func (s *Server) ListCharts(ctx context.Context, req *spb.ListChartsRequest) (*spb.ListChartsResponse, error) {
	user, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to authenticate user: %v", err)
	}

	if err := validator.ValidateListChartsRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid list charts request: %v", err)
	}

	if err := s.authorizeChartParent(ctx, user, req.GetParent()); err != nil {
		return nil, err
	}

	if s.repo == nil {
		return nil, status.Error(codes.FailedPrecondition, "server repository not initialized")
	}

	pageSize := clampChartPageSize(req.GetPageSize())
	charts, nextPageToken, err := s.repo.ListCharts(ctx, req.GetParent(), pageSize, req.GetPageToken(), "", "", 0)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list charts: %v", err)
	}

	if charts == nil {
		charts = []*dpb.Chart{}
	}
	return &spb.ListChartsResponse{Charts: charts, NextPageToken: nextPageToken}, nil
}

// authorizeChartParent enforces experiment READER access when the parent names a work unit,
// matching the per-chart check in GetChart. Dashboard parents stay an open read for authenticated
// users, consistent with GetDashboard and ListDashboards.
func (s *Server) authorizeChartParent(ctx context.Context, user, parent string) error {
	xid, _, err := validator.ValidateWorkUnitName(parent)
	if err != nil {
		return nil
	}
	return s.authorizeExperimentRead(ctx, user, xid)
}

// clampChartPageSize applies the AIP-158 default and maximum page size bounds.
func clampChartPageSize(pageSize int32) int32 {
	if pageSize <= 0 {
		return defaultChartPageSize
	}
	if pageSize > maxChartPageSize {
		return maxChartPageSize
	}
	return pageSize
}
