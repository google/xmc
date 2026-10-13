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

const (
	// defaultChartPageSize is the page size applied when the caller omits page_size (AIP-158).
	defaultChartPageSize int32 = 10
	// maxChartPageSize caps the page size a caller may request (AIP-158).
	maxChartPageSize int32 = 100
)

// chartParentKind identifies which of the two collection parents ListCharts was called with. The
// two are authorized differently, so the handler resolves the shape explicitly instead of treating
// "not a work unit" as "therefore a dashboard".
type chartParentKind int

const (
	dashboardParent chartParentKind = iota
	workUnitParent
)

// ListCharts retrieves a paginated list of charts under a dashboard or work unit parent (AIP-132).
func (s *Server) ListCharts(ctx context.Context, req *dpb.ListChartsRequest) (*dpb.ListChartsResponse, error) {
	user, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to authenticate user: %v", err)
	}

	if err := validator.ValidateListChartsRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid list charts request: %v", err)
	}

	kind, err := s.resolveChartParent(ctx, user, req.GetParent())
	if err != nil {
		return nil, err
	}

	if s.repo == nil {
		return nil, status.Error(codes.FailedPrecondition, "server repository not initialized")
	}

	pageSize := clampPageSize(req.GetPageSize(), defaultChartPageSize, maxChartPageSize)
	charts, nextPageToken, err := s.repo.ListCharts(ctx, req.GetParent(), pageSize, req.GetPageToken(), "", "", 0)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list charts: %v", err)
	}

	charts, err = s.authorizeListedCharts(ctx, user, kind, charts)
	if err != nil {
		return nil, err
	}
	return &dpb.ListChartsResponse{Charts: charts, NextPageToken: nextPageToken}, nil
}

// resolveChartParent classifies the parent and enforces experiment READER access up front when it
// names a work unit, matching the per-chart check in GetChart. A dashboard parent carries no single
// xid, so its charts are authorized individually after the read instead.
func (s *Server) resolveChartParent(ctx context.Context, user, parent string) (chartParentKind, error) {
	if _, err := validator.ValidateDashboardName(parent); err == nil {
		return dashboardParent, nil
	}
	xid, _, err := validator.ValidateWorkUnitName(parent)
	if err != nil {
		return dashboardParent, status.Errorf(codes.InvalidArgument, "unsupported chart parent %q", parent)
	}
	return workUnitParent, s.authorizeExperimentRead(ctx, user, xid)
}

// authorizeListedCharts prunes charts the caller cannot read and guarantees a non-nil slice. A work
// unit parent was already authorized as a whole, so only dashboard parents need the per-chart pass.
// Pruning happens after pagination, so a page may hold fewer charts than page_size while
// next_page_token is still set; AIP-158 requires clients to follow the token regardless.
func (s *Server) authorizeListedCharts(ctx context.Context, user string, kind chartParentKind, charts []*dpb.Chart) ([]*dpb.Chart, error) {
	if kind == workUnitParent {
		if charts == nil {
			return []*dpb.Chart{}, nil
		}
		return charts, nil
	}
	return s.filterAuthorizedCharts(ctx, user, charts)
}
