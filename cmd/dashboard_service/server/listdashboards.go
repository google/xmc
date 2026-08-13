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

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/validator/validator"
)

// ListDashboards retrieves a list of dashboards and filters out unauthorized entries.
func (s *Server) ListDashboards(ctx context.Context, req *spb.ListDashboardsRequest) (*spb.ListDashboardsResponse, error) {
	user, _, err := s.auth.AuthenticateUserFromContext(ctx)
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

	authorizedDashboards, err := s.filterAuthorizedDashboards(ctx, user, dashboards)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to filter authorized dashboards: %v", err)
	}

	return &spb.ListDashboardsResponse{
		Dashboards:    authorizedDashboards,
		NextPageToken: nextPageToken,
	}, nil
}

// filterAuthorizedDashboards filters out dashboards the user is not permitted to read.
func (s *Server) filterAuthorizedDashboards(ctx context.Context, user string, dashboards []*dpb.Dashboard) ([]*dpb.Dashboard, error) {
	if len(dashboards) == 0 {
		return nil, nil
	}

	allowed := make([]bool, len(dashboards))
	g, gctx := errgroup.WithContext(ctx)

	for i, dash := range dashboards {
		i, dash := i, dash
		g.Go(func() error {
			ok, err := s.checkDashboardAuthorization(gctx, user, dash, auth.Reader)
			if err != nil {
				return err
			}
			allowed[i] = ok
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	var authorized []*dpb.Dashboard
	for i, isAllowed := range allowed {
		if isAllowed {
			authorized = append(authorized, dashboards[i])
		}
	}
	return authorized, nil
}
