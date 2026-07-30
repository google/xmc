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
	"net/http"
	"net/http/httptest"
	"testing"

	mpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

func TestCreateDashboard(t *testing.T) {
	tests := []struct {
		name     string
		req      *spb.CreateDashboardRequest
		wantName string
	}{
		{
			name: "create dashboard",
			req: &spb.CreateDashboardRequest{
				Dashboard: &mpb.Dashboard{Name: "dashboards/new", Title: "New Dashboard"},
			},
			wantName: "dashboards/new",
		},
	}

	s := newServer(LoadConfig())
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.CreateDashboard(ctx, tt.req)
			if err != nil {
				t.Fatalf("CreateDashboard failed: %v", err)
			}
			if resp.GetName() != tt.wantName {
				t.Errorf("CreateDashboard().Name = %q, want %q", resp.GetName(), tt.wantName)
			}
		})
	}
}

func TestGetDashboard(t *testing.T) {
	tests := []struct {
		name     string
		req      *spb.GetDashboardRequest
		wantName string
	}{
		{
			name:     "get sample dashboard",
			req:      &spb.GetDashboardRequest{Name: "dashboards/123"},
			wantName: "dashboards/123",
		},
	}

	s := newServer(LoadConfig())
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.GetDashboard(ctx, tt.req)
			if err != nil {
				t.Fatalf("GetDashboard failed: %v", err)
			}
			if resp.GetName() != tt.wantName {
				t.Errorf("GetDashboard().Name = %q, want %q", resp.GetName(), tt.wantName)
			}
		})
	}
}

func TestListDashboards(t *testing.T) {
	s := newServer(LoadConfig())
	ctx := context.Background()

	resp, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{})
	if err != nil {
		t.Fatalf("ListDashboards failed: %v", err)
	}
	if len(resp.GetDashboards()) != 0 {
		t.Errorf("ListDashboards() returned %d dashboards, want 0", len(resp.GetDashboards()))
	}
}

func TestUpdateDashboard(t *testing.T) {
	tests := []struct {
		name     string
		req      *spb.UpdateDashboardRequest
		wantName string
	}{
		{
			name: "update dashboard",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &mpb.Dashboard{Name: "dashboards/123", Title: "Updated Dashboard"},
			},
			wantName: "dashboards/123",
		},
	}

	s := newServer(LoadConfig())
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.UpdateDashboard(ctx, tt.req)
			if err != nil {
				t.Fatalf("UpdateDashboard failed: %v", err)
			}
			if resp.GetName() != tt.wantName {
				t.Errorf("UpdateDashboard().Name = %q, want %q", resp.GetName(), tt.wantName)
			}
		})
	}
}

func TestDeleteDashboard(t *testing.T) {
	s := newServer(LoadConfig())
	ctx := context.Background()

	_, err := s.DeleteDashboard(ctx, &spb.DeleteDashboardRequest{Name: "dashboards/123"})
	if err != nil {
		t.Fatalf("DeleteDashboard failed: %v", err)
	}
}

func TestCreateChart(t *testing.T) {
	tests := []struct {
		name     string
		req      *spb.CreateChartRequest
		wantName string
	}{
		{
			name: "create chart",
			req: &spb.CreateChartRequest{
				Chart: &mpb.Chart{Name: "dashboards/123/charts/new", Title: "New Chart"},
			},
			wantName: "dashboards/123/charts/new",
		},
	}

	s := newServer(LoadConfig())
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.CreateChart(ctx, tt.req)
			if err != nil {
				t.Fatalf("CreateChart failed: %v", err)
			}
			if resp.GetName() != tt.wantName {
				t.Errorf("CreateChart().Name = %q, want %q", resp.GetName(), tt.wantName)
			}
		})
	}
}

func TestGetChart(t *testing.T) {
	tests := []struct {
		name     string
		req      *spb.GetChartRequest
		wantName string
	}{
		{
			name:     "get sample chart",
			req:      &spb.GetChartRequest{Name: "dashboards/123/charts/456"},
			wantName: "dashboards/123/charts/456",
		},
	}

	s := newServer(LoadConfig())
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.GetChart(ctx, tt.req)
			if err != nil {
				t.Fatalf("GetChart failed: %v", err)
			}
			if resp.GetName() != tt.wantName {
				t.Errorf("GetChart().Name = %q, want %q", resp.GetName(), tt.wantName)
			}
		})
	}
}

func TestGetChartData(t *testing.T) {
	s := newServer(LoadConfig())
	ctx := context.Background()

	resp, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
	if err != nil {
		t.Fatalf("GetChartData failed: %v", err)
	}
	if resp == nil {
		t.Error("GetChartData returned nil response")
	}
}

func TestListCharts(t *testing.T) {
	s := newServer(LoadConfig())
	ctx := context.Background()

	resp, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "dashboards/123"})
	if err != nil {
		t.Fatalf("ListCharts failed: %v", err)
	}
	if len(resp.GetCharts()) == 0 {
		t.Error("ListCharts returned zero charts, expected at least 1")
	}
}

func TestUpdateChart(t *testing.T) {
	tests := []struct {
		name     string
		req      *spb.UpdateChartRequest
		wantName string
	}{
		{
			name: "update chart",
			req: &spb.UpdateChartRequest{
				Chart: &mpb.Chart{Name: "dashboards/123/charts/456", Title: "Updated Chart"},
			},
			wantName: "dashboards/123/charts/456",
		},
	}

	s := newServer(LoadConfig())
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.UpdateChart(ctx, tt.req)
			if err != nil {
				t.Fatalf("UpdateChart failed: %v", err)
			}
			if resp.GetName() != tt.wantName {
				t.Errorf("UpdateChart().Name = %q, want %q", resp.GetName(), tt.wantName)
			}
		})
	}
}

func TestDeleteChart(t *testing.T) {
	s := newServer(LoadConfig())
	ctx := context.Background()

	_, err := s.DeleteChart(ctx, &spb.DeleteChartRequest{Name: "dashboards/123/charts/456"})
	if err != nil {
		t.Fatalf("DeleteChart failed: %v", err)
	}
}

func TestProbeEndpoints(t *testing.T) {
	mux := setupMux()

	tests := []struct {
		endpoint   string
		wantBody   string
		wantStatus int
	}{
		{endpoint: "/healthz", wantBody: "OK", wantStatus: http.StatusOK},
		{endpoint: "/readyz", wantBody: "OK", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.endpoint, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("GET %s status = %d, want %d", tt.endpoint, rec.Code, tt.wantStatus)
			}
			if rec.Body.String() != tt.wantBody {
				t.Errorf("GET %s body = %q, want %q", tt.endpoint, rec.Body.String(), tt.wantBody)
			}
		})
	}
}

