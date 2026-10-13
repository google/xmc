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

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/validator"
)

// GetDashboard retrieves a dashboard by resource name with open read for authenticated users
// (AIP-131). The dashboard carries only chart IDs, not chart bodies, so there is nothing here to
// authorize per experiment; READER is enforced when those charts are fetched via GetChart or
// ListCharts.
func (s *Server) GetDashboard(ctx context.Context, req *dpb.GetDashboardRequest) (*dpb.Dashboard, error) {
	if _, _, err := s.auth.AuthenticateUserFromContext(ctx); err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to authenticate user: %v", err)
	}

	if err := validator.ValidateGetDashboardRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid get dashboard request: %v", err)
	}

	dashID, err := validator.ValidateDashboardName(req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid dashboard name: %v", err)
	}

	return s.readDashboard(ctx, dashID, req.GetName())
}

// readDashboard loads a dashboard by ID and maps storage failures onto gRPC status codes.
func (s *Server) readDashboard(ctx context.Context, dashID, name string) (*dpb.Dashboard, error) {
	if s.repo == nil {
		return nil, status.Error(codes.FailedPrecondition, "server repository not initialized")
	}

	dash, err := s.repo.GetDashboard(ctx, dashID)
	if err != nil {
		if isNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound, "dashboard %q not found", name)
		}
		return nil, status.Errorf(codes.Internal, "failed to get dashboard: %v", err)
	}
	if dash == nil {
		return nil, status.Errorf(codes.NotFound, "dashboard %q not found", name)
	}
	return dash, nil
}
