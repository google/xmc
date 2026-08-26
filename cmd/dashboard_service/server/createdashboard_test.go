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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

func TestCreateDashboard(t *testing.T) {
	ctx := context.Background()

	t.Run("successful creation with auto-generated UUID", func(t *testing.T) {
		var savedDash *dpb.Dashboard
		repo := &mockRepository{
			createDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard) error {
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

		req := &spb.CreateDashboardRequest{
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
			createDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard) error {
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

		req := &spb.CreateDashboardRequest{
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

	t.Run("successful creation with explicit dashboard_id", func(t *testing.T) {
		repo := &mockRepository{}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "author@example.com", "user-1", nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &spb.CreateDashboardRequest{
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

		req := &spb.CreateDashboardRequest{
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
			req  *spb.CreateDashboardRequest
		}{
			{name: "nil request", req: nil},
			{name: "nil dashboard", req: &spb.CreateDashboardRequest{}},
			{name: "empty title", req: &spb.CreateDashboardRequest{Dashboard: &dpb.Dashboard{Title: ""}}},
			{name: "invalid dashboard ID", req: &spb.CreateDashboardRequest{
				DashboardId: "invalid/id",
				Dashboard:   &dpb.Dashboard{Title: "Title"},
			}},
			{name: "duplicate chart IDs", req: &spb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Title:    "Title",
					ChartIds: []string{"f47ac10b-58cc-4372-a567-0e02b2c3d479", "f47ac10b-58cc-4372-a567-0e02b2c3d479"},
				},
			}},
			{name: "resource name chart ID rejected", req: &spb.CreateDashboardRequest{
				Dashboard: &dpb.Dashboard{
					Title:    "Title",
					ChartIds: []string{"charts/f47ac10b-58cc-4372-a567-0e02b2c3d479"},
				},
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
			createDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard) error {
				return errors.New("spanner write failed")
			},
		}
		authMock := &mockAuthorizer{}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &spb.CreateDashboardRequest{
			Dashboard: &dpb.Dashboard{Title: "My Dash"},
		}

		_, err := s.CreateDashboard(ctx, req)
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("repo AlreadyExists error returns AlreadyExists", func(t *testing.T) {
		repo := &mockRepository{
			createDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard) error {
				return status.Error(codes.AlreadyExists, "dashboard already exists")
			},
		}
		authMock := &mockAuthorizer{}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &spb.CreateDashboardRequest{
			DashboardId: "duplicate-id",
			Dashboard:   &dpb.Dashboard{Title: "My Dash"},
		}

		_, err := s.CreateDashboard(ctx, req)
		if status.Code(err) != codes.AlreadyExists {
			t.Errorf("Expected code AlreadyExists, got %v", status.Code(err))
		}
	})
}
