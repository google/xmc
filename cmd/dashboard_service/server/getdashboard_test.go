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
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/internal/auth"
	xpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestGetDashboard(t *testing.T) {
	ctx := context.Background()

	t.Run("successful retrieval by authorized user", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{
					Name:  "dashboards/" + dashboardId,
					Title: "Sample Dashboard",
					Owner: "owner@example.com",
				}, nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "reader@example.com", "user-1", nil
			},
			isUserAuthorizedFn: func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
				if role == auth.Reader {
					return true, nil
				}
				return false, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, nil)

		req := &spb.GetDashboardRequest{Name: "dashboards/dash-123"}
		resp, err := s.GetDashboard(ctx, req)
		if err != nil {
			t.Fatalf("GetDashboard failed: %v", err)
		}
		if resp.GetName() != "dashboards/dash-123" {
			t.Errorf("Expected name 'dashboards/dash-123', got %q", resp.GetName())
		}
	})

	t.Run("unauthenticated caller returns Unauthenticated", func(t *testing.T) {
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "", "", errors.New("unauthenticated")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, authMock, nil)

		_, err := s.GetDashboard(ctx, &spb.GetDashboardRequest{Name: "dashboards/dash-123"})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("Expected code Unauthenticated, got %v", status.Code(err))
		}
	})

	t.Run("invalid request returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, nil)

		tests := []struct {
			name string
			req  *spb.GetDashboardRequest
		}{
			{name: "nil request", req: nil},
			{name: "empty name", req: &spb.GetDashboardRequest{Name: ""}},
			{name: "invalid name format", req: &spb.GetDashboardRequest{Name: "invalid-name"}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, err := s.GetDashboard(ctx, tc.req)
				if status.Code(err) != codes.InvalidArgument {
					t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
				}
			})
		}
	})

	t.Run("dashboard not found returns NotFound", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return nil, status.Error(codes.NotFound, "row not found")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, nil)

		_, err := s.GetDashboard(ctx, &spb.GetDashboardRequest{Name: "dashboards/dash-123"})
		if status.Code(err) != codes.NotFound {
			t.Errorf("Expected code NotFound, got %v", status.Code(err))
		}
	})

	t.Run("repo error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return nil, errors.New("spanner timeout")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, nil)

		_, err := s.GetDashboard(ctx, &spb.GetDashboardRequest{Name: "dashboards/dash-123"})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("unauthorized user returns PermissionDenied", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{Name: "dashboards/" + dashboardId, Owner: "other@example.com"}, nil
			},
		}
		authMock := &mockAuthorizer{
			isUserAuthorizedFn: func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
				return false, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, nil)

		_, err := s.GetDashboard(ctx, &spb.GetDashboardRequest{Name: "dashboards/dash-123"})
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("Expected code PermissionDenied, got %v", status.Code(err))
		}
	})

	t.Run("authorization check error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{Name: "dashboards/" + dashboardId, Owner: "other@example.com"}, nil
			},
		}
		authMock := &mockAuthorizer{
			isUserAuthorizedFn: func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
				return false, errors.New("cloud identity api failure")
			},
		}
		s := NewServer(&Config{}, repo, authMock, nil)

		_, err := s.GetDashboard(ctx, &spb.GetDashboardRequest{Name: "dashboards/dash-123"})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})
}
