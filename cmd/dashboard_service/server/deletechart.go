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

	spb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/validator"
)

// DeleteChart deletes an existing chart after validating experiment write permissions (AIP-135).
func (s *Server) DeleteChart(ctx context.Context, req *spb.DeleteChartRequest) (*emptypb.Empty, error) {
	user, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to authenticate user: %v", err)
	}

	if err := validator.ValidateDeleteChartRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid delete chart request: %v", err)
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
	if chart == nil {
		return nil, status.Errorf(codes.Internal, "repository returned nil chart for %q", req.GetName())
	}

	xid := chart.GetXid()
	if xid <= 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "chart %q is missing a valid experiment XID (%d)", req.GetName(), xid)
	}

	if err := s.authorizeExperimentWrite(ctx, user, xid); err != nil {
		return nil, err
	}

	if err := s.repo.DeleteChart(ctx, chartID); err != nil {
		if isNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound, "chart %q not found", req.GetName())
		}
		return nil, status.Errorf(codes.Internal, "failed to delete chart: %v", err)
	}

	return &emptypb.Empty{}, nil
}
