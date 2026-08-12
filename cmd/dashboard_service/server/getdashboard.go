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
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/validator/validator"
)

// GetDashboard handles retrieval of a dashboard by its resource name.
func (s *Server) GetDashboard(ctx context.Context, req *spb.GetDashboardRequest) (*dpb.Dashboard, error) {
	user, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to authenticate user: %v", err)
	}

	if err := validator.ValidateGetDashboardRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid get dashboard request: %v", err)
	}

	dashID, err := validator.ValidateDashboardName(req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid dashboard name: %v", err)
	}

	dash, err := s.repo.GetDashboard(ctx, dashID)
	if err != nil {
		if isNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound, "dashboard %q not found", req.GetName())
		}
		return nil, status.Errorf(codes.Internal, "failed to get dashboard: %v", err)
	}

	authorized, err := s.checkDashboardAuthorization(ctx, user, dash, auth.Reader)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to check dashboard authorization: %v", err)
	}
	if !authorized {
		return nil, status.Errorf(codes.PermissionDenied, "user %q is not authorized to access dashboard %q", user, req.GetName())
	}

	return dash, nil
}
