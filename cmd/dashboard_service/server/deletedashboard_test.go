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

func TestDeleteDashboard(t *testing.T) {
	ctx := context.Background()

	t.Run("successful deletion by owner", func(t *testing.T) {
		deletedID := ""
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{
					Name:  "dashboards/" + dashboardId,
					Owner: "owner@example.com",
				}, nil
			},
			deleteDashboardFn: func(ctx context.Context, dashboardId string) error {
				deletedID = dashboardId
				return nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "owner@example.com", "u-1", nil
			},
			isUserAuthorizedFn: func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
				return true, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &spb.DeleteDashboardRequest{Name: "dashboards/dash-123"}
		_, err := s.DeleteDashboard(ctx, req)
		if err != nil {
			t.Fatalf("DeleteDashboard failed: %v", err)
		}
		if deletedID != "dash-123" {
			t.Errorf("Expected deleted ID 'dash-123', got %q", deletedID)
		}
	})

	t.Run("unauthenticated caller returns Unauthenticated", func(t *testing.T) {
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "", "", errors.New("unauthenticated")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, authMock, &mockXidClient{}, nil)

		_, err := s.DeleteDashboard(ctx, &spb.DeleteDashboardRequest{Name: "dashboards/dash-123"})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("Expected code Unauthenticated, got %v", status.Code(err))
		}
	})

	t.Run("invalid request returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)

		tests := []struct {
			name string
			req  *spb.DeleteDashboardRequest
		}{
			{name: "nil request", req: nil},
			{name: "empty name", req: &spb.DeleteDashboardRequest{Name: ""}},
			{name: "invalid name format", req: &spb.DeleteDashboardRequest{Name: "invalid-name"}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, err := s.DeleteDashboard(ctx, tc.req)
				if status.Code(err) != codes.InvalidArgument {
					t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
				}
			})
		}
	})

	t.Run("dashboard not found on fetch returns NotFound", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return nil, status.Error(codes.NotFound, "not found")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.DeleteDashboard(ctx, &spb.DeleteDashboardRequest{Name: "dashboards/dash-123"})
		if status.Code(err) != codes.NotFound {
			t.Errorf("Expected code NotFound, got %v", status.Code(err))
		}
	})

	t.Run("storage error on fetch returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return nil, errors.New("read error")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.DeleteDashboard(ctx, &spb.DeleteDashboardRequest{Name: "dashboards/dash-123"})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("non-owner attempting deletion rejected with PermissionDenied", func(t *testing.T) {
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
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		_, err := s.DeleteDashboard(ctx, &spb.DeleteDashboardRequest{Name: "dashboards/dash-123"})
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
				return false, errors.New("auth error")
			},
		}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		_, err := s.DeleteDashboard(ctx, &spb.DeleteDashboardRequest{Name: "dashboards/dash-123"})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("repo delete error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{Name: "dashboards/" + dashboardId, Owner: "owner@example.com"}, nil
			},
			deleteDashboardFn: func(ctx context.Context, dashboardId string) error {
				return errors.New("delete failed")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.DeleteDashboard(ctx, &spb.DeleteDashboardRequest{Name: "dashboards/dash-123"})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("repo delete returns not found returns NotFound", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{Name: "dashboards/" + dashboardId, Owner: "owner@example.com"}, nil
			},
			deleteDashboardFn: func(ctx context.Context, dashboardId string) error {
				return status.Error(codes.NotFound, "not found")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.DeleteDashboard(ctx, &spb.DeleteDashboardRequest{Name: "dashboards/dash-123"})
		if status.Code(err) != codes.NotFound {
			t.Errorf("Expected code NotFound, got %v", status.Code(err))
		}
	})

	t.Run("nil repo in server struct returns FailedPrecondition", func(t *testing.T) {
		s := &Server{
			auth: &mockAuthorizer{},
		}
		_, err := s.DeleteDashboard(ctx, &spb.DeleteDashboardRequest{Name: "dashboards/dash-123"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("Expected code FailedPrecondition, got %v", status.Code(err))
		}
	})
}
