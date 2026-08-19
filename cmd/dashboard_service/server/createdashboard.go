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

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/validator/validator"
)

// CreateDashboard handles creation of a new dashboard entity.
func (s *Server) CreateDashboard(ctx context.Context, req *spb.CreateDashboardRequest) (*dpb.Dashboard, error) {
	user, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to authenticate user: %v", err)
	}

	if err := validator.ValidateCreateDashboardRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid create dashboard request: %v", err)
	}

	dashID := req.GetDashboardId()
	if dashID == "" {
		dashID = uuid.New().String()
	}

	dash := req.GetDashboard()
	dash.Name = validator.ConstructDashboardName(dashID)
	dash.Owner = user

	if err := s.repo.CreateDashboard(ctx, dash); err != nil {
		if isAlreadyExistsError(err) {
			return nil, status.Errorf(codes.AlreadyExists, "dashboard %q already exists", dashID)
		}
		return nil, status.Errorf(codes.Internal, "failed to create dashboard: %v", err)
	}

	return dash, nil
}
