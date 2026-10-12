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
	"google.golang.org/protobuf/types/known/emptypb"

	spb "github.com/google/xmc/api/protos/dashboard"
	"github.com/google/xmc/internal/auth"
	"github.com/google/xmc/internal/validator"
)

// DeleteDashboard handles deleting an existing dashboard with owner authorization.
func (s *Server) DeleteDashboard(ctx context.Context, req *spb.DeleteDashboardRequest) (*emptypb.Empty, error) {
	user, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to authenticate user: %v", err)
	}

	if err := validator.ValidateDeleteDashboardRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid delete dashboard request: %v", err)
	}

	dashID, err := validator.ValidateDashboardName(req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid dashboard name: %v", err)
	}

	if s.repo == nil {
		return nil, status.Error(codes.FailedPrecondition, "server repository not initialized")
	}

	existing, err := s.repo.GetDashboard(ctx, dashID)
	if err != nil {
		if isNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound, "dashboard %q not found", req.GetName())
		}
		return nil, status.Errorf(codes.Internal, "failed to get dashboard: %v", err)
	}

	authorized, err := s.checkDashboardAuthorization(ctx, user, existing, auth.Owner)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to check dashboard authorization: %v", err)
	}
	if !authorized {
		return nil, status.Errorf(codes.PermissionDenied, "user %q is not authorized to delete dashboard %q", user, req.GetName())
	}

	if err := s.repo.DeleteDashboard(ctx, dashID); err != nil {
		if isNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound, "dashboard %q not found", req.GetName())
		}
		return nil, status.Errorf(codes.Internal, "failed to delete dashboard: %v", err)
	}

	return &emptypb.Empty{}, nil
}
