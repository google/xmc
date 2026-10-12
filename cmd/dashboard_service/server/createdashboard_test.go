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
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	dpb "github.com/google/xmc/api/protos/dashboard"
	xidapipb "github.com/google/xmc/api/protos/xid"
)

// existingChartFn answers GetChart with a chart belonging to the given experiment, so that chart
// IDs a request links by ID pass the reference authorization.
func existingChartFn(xid int64) getChartFunc {
	return func(ctx context.Context, chartId string) (*dpb.Chart, error) {
		return chartWithXid("charts/"+chartId, xid), nil
	}
}

func TestCreateDashboard(t *testing.T) {
	ctx := context.Background()

	t.Run("successful creation with auto-generated UUID", func(t *testing.T) {
		var savedDash *dpb.Dashboard
		repo := &mockRepository{
			createDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error {
				savedDash = dashboard
				return nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "author@example.com", "user-1", nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &dpb.CreateDashboardRequest{
			Dashboard: &dpb.Dashboard{
				Title:       "My Dashboard",
				Description: "Test Description",
			},
		}

		resp, err := s.CreateDashboard(ctx, req)
		if err != nil {
			t.Fatalf("CreateDashboard failed: %v", err)
		}
		if !strings.HasPrefix(resp.GetName(), "dashboards/") {
			t.Errorf("Expected name with prefix 'dashboards/', got %q", resp.GetName())
		}
		if resp.GetOwner() != "author@example.com" {
			t.Errorf("Expected owner 'author@example.com', got %q", resp.GetOwner())
		}
		if savedDash == nil || savedDash.GetName() != resp.GetName() {
			t.Errorf("Expected saved dashboard name %q, got %v", resp.GetName(), savedDash)
		}
	})

	t.Run("successful creation with chart_ids", func(t *testing.T) {
		var savedDash *dpb.Dashboard
		repo := &mockRepository{
			createDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error {
				savedDash = dashboard
				return nil
			},
			getChartFn: existingChartFn(4321),
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "author@example.com", "user-1", nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &dpb.CreateDashboardRequest{
			Dashboard: &dpb.Dashboard{
				Title:    "Dashboard with Charts",
				ChartIds: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "c9a646d3-9c61-4cd7-bf10-8e96dc638210"},
			},
		}

		resp, err := s.CreateDashboard(ctx, req)
		if err != nil {
			t.Fatalf("CreateDashboard failed: %v", err)
		}
		if len(resp.GetChartIds()) != 2 {
			t.Errorf("Expected 2 chart IDs, got %v", resp.GetChartIds())
		}
		if savedDash == nil || len(savedDash.GetChartIds()) != 2 {
			t.Errorf("Expected saved dashboard chart IDs to have 2 items, got %v", savedDash)
		}
	})

	t.Run("creation with inline charts names them and links them to the dashboard", func(t *testing.T) {
		var savedDash *dpb.Dashboard
		var savedCharts []*dpb.Chart
		repo := &mockRepository{
			createDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error {
				savedDash = dashboard
				savedCharts = charts
				return nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "author@example.com", "user-1", nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &dpb.CreateDashboardRequest{
			Dashboard: &dpb.Dashboard{Title: "Comparison"},
			Charts: []*dpb.Chart{
				{Title: "Baseline", Xid: 4321, Wid: 1},
				{Title: "Tuned", Xid: 8765, Wid: 2},
			},
		}

		resp, err := s.CreateDashboard(ctx, req)
		if err != nil {
			t.Fatalf("CreateDashboard failed: %v", err)
		}
		if len(savedCharts) != 2 {
			t.Fatalf("Expected 2 charts handed to the repository, got %d", len(savedCharts))
		}
		wantNames := []string{
			"experiments/4321/workUnits/1/charts/",
			"experiments/8765/workUnits/2/charts/",
		}
		for i, chart := range savedCharts {
			if !strings.HasPrefix(chart.GetName(), wantNames[i]) {
				t.Errorf("Expected chart %d name with prefix %q, got %q", i, wantNames[i], chart.GetName())
			}
			chartID := strings.TrimPrefix(chart.GetName(), wantNames[i])
			if resp.GetChartIds()[i] != chartID {
				t.Errorf("Expected chart_ids[%d] %q to match chart ID %q", i, resp.GetChartIds()[i], chartID)
			}
		}
		if savedDash == nil || len(savedDash.GetChartIds()) != 2 {
			t.Errorf("Expected saved dashboard to link 2 charts, got %v", savedDash)
		}
	})

	t.Run("inline charts are appended after pre-existing chart IDs", func(t *testing.T) {
		repo := &mockRepository{getChartFn: existingChartFn(4321)}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		req := &dpb.CreateDashboardRequest{
			Dashboard: &dpb.Dashboard{
				Title:    "Comparison",
				ChartIds: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479"},
			},
			Charts: []*dpb.Chart{{Title: "Baseline", Xid: 4321, Wid: 1}},
		}

		resp, err := s.CreateDashboard(ctx, req)
		if err != nil {
			t.Fatalf("CreateDashboard failed: %v", err)
		}
		if len(resp.GetChartIds()) != 2 {
			t.Fatalf("Expected 2 chart IDs, got %v", resp.GetChartIds())
		}
		if resp.GetChartIds()[0] != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
			t.Errorf("Expected the pre-existing chart ID to come first, got %v", resp.GetChartIds())
		}
	})

	t.Run("unauthorized experiment aborts the whole creation", func(t *testing.T) {
		called := false
		repo := &mockRepository{
			createDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error {
				called = true
				return nil
			},
		}
		xidClient := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(in.GetName() != "xids/8765")}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidClient, nil)

		req := &dpb.CreateDashboardRequest{
			Dashboard: &dpb.Dashboard{Title: "Comparison"},
			Charts: []*dpb.Chart{
				{Title: "Baseline", Xid: 4321, Wid: 1},
				{Title: "Tuned", Xid: 8765, Wid: 2},
			},
		}

		_, err := s.CreateDashboard(ctx, req)
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("Expected code PermissionDenied, got %v", status.Code(err))
		}
		if called {
			t.Error("Expected no dashboard to be written when an experiment is not writable")
		}
	})

	t.Run("a chart ID from an unreadable experiment aborts the creation", func(t *testing.T) {
		called := false
		repo := &mockRepository{
			getChartFn: existingChartFn(8765),
			createDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error {
				called = true
				return nil
			},
		}
		xidClient := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(in.GetName() != "xids/8765")}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidClient, nil)

		req := &dpb.CreateDashboardRequest{
			Dashboard: &dpb.Dashboard{
				Title:    "Comparison",
				ChartIds: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479"},
			},
		}

		_, err := s.CreateDashboard(ctx, req)
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("Expected code PermissionDenied, got %v", status.Code(err))
		}
		if called {
			t.Error("Expected no dashboard to be written when a referenced chart is not readable")
		}
	})

	t.Run("a chart ID that names no chart is rejected as InvalidArgument", func(t *testing.T) {
		called := false
		repo := &mockRepository{
			createDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error {
				called = true
				return nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		req := &dpb.CreateDashboardRequest{
			Dashboard: &dpb.Dashboard{
				Title:    "Comparison",
				ChartIds: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479"},
			},
		}

		_, err := s.CreateDashboard(ctx, req)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
		}
		if called {
			t.Error("Expected no dashboard to be written when a referenced chart does not exist")
		}
	})

	t.Run("successful creation with explicit dashboard_id", func(t *testing.T) {
		repo := &mockRepository{}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "author@example.com", "user-1", nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &dpb.CreateDashboardRequest{
			DashboardId: "custom-id-123",
			Dashboard: &dpb.Dashboard{
				Title: "Custom Dashboard",
			},
		}

		resp, err := s.CreateDashboard(ctx, req)
		if err != nil {
			t.Fatalf("CreateDashboard failed: %v", err)
		}
		if resp.GetName() != "dashboards/custom-id-123" {
			t.Errorf("Expected name 'dashboards/custom-id-123', got %q", resp.GetName())
		}
	})

	t.Run("unauthenticated caller returns Unauthenticated", func(t *testing.T) {
		repo := &mockRepository{}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "", "", errors.New("missing auth token")
			},
		}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &dpb.CreateDashboardRequest{
			Dashboard: &dpb.Dashboard{Title: "My Dash"},
		}

		_, err := s.CreateDashboard(ctx, req)
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("Expected code Unauthenticated, got %v", status.Code(err))
		}
	})

	t.Run("invalid request returns InvalidArgument", func(t *testing.T) {
		repo := &mockRepository{}
		authMock := &mockAuthorizer{}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		tests := []struct {
			name string
			req  *dpb.CreateDashboardRequest
		}{
			{name: "nil request", req: nil},
			{name: "nil dashboard", req: &dpb.CreateDashboardRequest{}},
			{name: "empty title", req: &dpb.CreateDashboardRequest{Dashboard: &dpb.Dashboard{Title: ""}}},
			{name: "invalid dashboard ID", req: &dpb.CreateDashboardRequest{
				DashboardId: "invalid/id",
				Dashboard:   &dpb.Dashboard{Title: "Title"},
			}},
			{name: "duplicate chart IDs", req: &dpb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Title:    "Title",
					ChartIds: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "f47ac10b-58cc-4372-a567-0e02b2c3d479"},
				},
			}},
			{name: "resource name chart ID rejected", req: &dpb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Title:    "Title",
					ChartIds: []string{"charts/f47ac10b-58cc-4372-a567-0e02b2c3d479"},
				},
			}},
			{name: "inline chart without title", req: &dpb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{Title: "Title"},
				Charts:    []*dpb.Chart{{Xid: 4321, Wid: 1}},
			}},
			{name: "inline chart without xid", req: &dpb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{Title: "Title"},
				Charts:    []*dpb.Chart{{Title: "Baseline", Wid: 1}},
			}},
			{name: "inline chart without wid", req: &dpb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{Title: "Title"},
				Charts:    []*dpb.Chart{{Title: "Baseline", Xid: 4321}},
			}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, err := s.CreateDashboard(ctx, tc.req)
				if status.Code(err) != codes.InvalidArgument {
					t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
				}
			})
		}
	})

	t.Run("repo error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			createDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error {
				return errors.New("spanner write failed")
			},
		}
		authMock := &mockAuthorizer{}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &dpb.CreateDashboardRequest{
			Dashboard: &dpb.Dashboard{Title: "My Dash"},
		}

		_, err := s.CreateDashboard(ctx, req)
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("repo AlreadyExists error returns AlreadyExists", func(t *testing.T) {
		repo := &mockRepository{
			createDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error {
				return status.Error(codes.AlreadyExists, "dashboard already exists")
			},
		}
		authMock := &mockAuthorizer{}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &dpb.CreateDashboardRequest{
			DashboardId: "duplicate-id",
			Dashboard:   &dpb.Dashboard{Title: "My Dash"},
		}

		_, err := s.CreateDashboard(ctx, req)
		if status.Code(err) != codes.AlreadyExists {
			t.Errorf("Expected code AlreadyExists, got %v", status.Code(err))
		}
	})
}
