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

	dpb "github.com/google/xmc/api/protos/dashboard"
	"github.com/google/xmc/internal/validator"
)

// nameInlineCharts gives every chart created alongside a dashboard its server-assigned identity and
// returns the generated chart IDs in request order, ready to be linked from the dashboard.
func nameInlineCharts(charts []*dpb.Chart) []string {
	chartIDs := make([]string, 0, len(charts))
	for _, chart := range charts {
		chartID := uuid.New().String()
		populateChartProperties(chart, chartID, chart.GetXid(), chart.GetWid())
		chartIDs = append(chartIDs, chartID)
	}
	return chartIDs
}

// CreateDashboard handles creation of a new dashboard entity together with its charts.
//
// A dashboard and the charts it is created with are written in a single transaction. Creating them
// through separate RPCs cannot be made atomic by the client: a dashboard whose chart writes failed
// renders empty, and charts whose dashboard write failed are unreachable rows nothing links to.
func (s *Server) CreateDashboard(ctx context.Context, req *dpb.CreateDashboardRequest) (*dpb.Dashboard, error) {
	user, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to authenticate user: %v", err)
	}

	if err := validator.ValidateCreateDashboardRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid create dashboard request: %v", err)
	}

	charts := req.GetCharts()
	if err := s.authorizeChartExperimentWrites(ctx, user, charts); err != nil {
		return nil, err
	}
	// Charts the request links by ID already exist and may belong to somebody else's experiment, so
	// they are authorized separately from the ones created inline.
	if err := s.authorizeChartReferences(ctx, user, req.GetDashboard().GetChartIds()); err != nil {
		return nil, err
	}

	dashID := req.GetDashboardId()
	if dashID == "" {
		dashID = uuid.New().String()
	}

	dash := req.GetDashboard()
	dash.Name = validator.ConstructDashboardName(dashID)
	dash.Owner = user
	dash.ChartIds = append(dash.GetChartIds(), nameInlineCharts(charts)...)

	if err := s.repo.CreateDashboard(ctx, dash, charts); err != nil {
		if isAlreadyExistsError(err) {
			return nil, status.Errorf(codes.AlreadyExists, "dashboard %q already exists", dashID)
		}
		return nil, status.Errorf(codes.Internal, "failed to create dashboard: %v", err)
	}

	return dash, nil
}
