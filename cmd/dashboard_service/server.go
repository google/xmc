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

package main

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	mpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/internal/dashboard/dataengine"
	"xmanager_cloud/internal/storage/spanner/dashboard"
)

type server struct {
	spb.UnimplementedDashboardServiceServer
	cfg        *Config
	repo       *dashboard.Repository
	dataEngine *dataengine.DataEngine
}

func newServer(cfg *Config, repo *dashboard.Repository, de *dataengine.DataEngine) *server {
	return &server{
		cfg:        cfg,
		repo:       repo,
		dataEngine: de,
	}
}

func (s *server) CreateDashboard(ctx context.Context, req *spb.CreateDashboardRequest) (*mpb.Dashboard, error) {
	return req.GetDashboard(), nil
}

func (s *server) GetDashboard(ctx context.Context, req *spb.GetDashboardRequest) (*mpb.Dashboard, error) {
	return &mpb.Dashboard{
		Name:  req.GetName(),
		Title: "Sample Dashboard",
	}, nil
}

func (s *server) ListDashboards(ctx context.Context, req *spb.ListDashboardsRequest) (*spb.ListDashboardsResponse, error) {
	return &spb.ListDashboardsResponse{}, nil
}

func (s *server) UpdateDashboard(ctx context.Context, req *spb.UpdateDashboardRequest) (*mpb.Dashboard, error) {
	return req.GetDashboard(), nil
}

func (s *server) DeleteDashboard(ctx context.Context, req *spb.DeleteDashboardRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (s *server) CreateChart(ctx context.Context, req *spb.CreateChartRequest) (*mpb.Chart, error) {
	return req.GetChart(), nil
}

func (s *server) GetChart(ctx context.Context, req *spb.GetChartRequest) (*mpb.Chart, error) {
	return &mpb.Chart{
		Name:  req.GetName(),
		Title: "Sample Chart",
	}, nil
}

func (s *server) GetChartData(ctx context.Context, req *spb.GetChartDataRequest) (*mpb.ChartData, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	if s.dataEngine == nil {
		return nil, status.Error(codes.FailedPrecondition, "data engine is not configured")
	}
	if s.repo == nil {
		return nil, status.Error(codes.FailedPrecondition, "chart repository is not configured")
	}
	chart, err := s.repo.GetChart(ctx, req.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to get chart %q: %w", req.GetName(), err)
	}
	return s.dataEngine.FetchChartData(ctx, chart, req)
}

func (s *server) ListCharts(ctx context.Context, req *spb.ListChartsRequest) (*spb.ListChartsResponse, error) {
	return &spb.ListChartsResponse{
		Charts: []*mpb.Chart{
			{
				Name:  req.GetParent() + "/charts/sample",
				Title: "Sample Chart",
			},
		},
	}, nil
}

func (s *server) UpdateChart(ctx context.Context, req *spb.UpdateChartRequest) (*mpb.Chart, error) {
	return req.GetChart(), nil
}

func (s *server) DeleteChart(ctx context.Context, req *spb.DeleteChartRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}
