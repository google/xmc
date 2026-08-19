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

	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/validator/validator"
)

// ListDashboards retrieves a paginated list of dashboards without post-query owner pruning (AIP-132).
func (s *Server) ListDashboards(ctx context.Context, req *spb.ListDashboardsRequest) (*spb.ListDashboardsResponse, error) {
	_, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to authenticate user: %v", err)
	}

	if err := validator.ValidateListDashboardsRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid list dashboards request: %v", err)
	}

	dashboards, nextPageToken, err := s.repo.ListDashboards(ctx, req.GetPageSize(), req.GetPageToken(), req.GetFilter(), "", 0)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list dashboards: %v", err)
	}

	return &spb.ListDashboardsResponse{
		Dashboards:    dashboards,
		NextPageToken: nextPageToken,
	}, nil
}
