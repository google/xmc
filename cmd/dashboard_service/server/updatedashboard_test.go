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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/internal/auth"
	xpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestUpdateDashboard(t *testing.T) {
	ctx := context.Background()

	t.Run("successful full update by owner", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{
					Name:  "dashboards/" + dashboardId,
					Title: "Original",
					Owner: "user@example.com",
				}, nil
			},
			updateDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error) {
				return dashboard, nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "user@example.com", "u-1", nil
			},
			isUserAuthorizedFn: func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
				return true, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, nil)

		req := &spb.UpdateDashboardRequest{
			Dashboard: &dpb.Dashboard{
				Name:  "dashboards/dash-123",
				Title: "Updated Title",
			},
		}

		resp, err := s.UpdateDashboard(ctx, req)
		if err != nil {
			t.Fatalf("UpdateDashboard failed: %v", err)
		}
		if resp.GetTitle() != "Updated Title" {
			t.Errorf("Expected title 'Updated Title', got %q", resp.GetTitle())
		}
	})

	t.Run("successful partial update with field mask", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{
					Name:        "dashboards/" + dashboardId,
					Title:       "Original",
					Description: "Original Desc",
					Owner:       "user@example.com",
				}, nil
			},
			updateDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{
					Name:        dashboard.GetName(),
					Title:       "Original",
					Description: dashboard.GetDescription(),
				}, nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "user@example.com", "u-1", nil
			},
			isUserAuthorizedFn: func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
				return true, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, nil)

		req := &spb.UpdateDashboardRequest{
			Dashboard: &dpb.Dashboard{
				Name:        "dashboards/dash-123",
				Description: "New Description",
			},
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"description"},
			},
		}

		resp, err := s.UpdateDashboard(ctx, req)
		if err != nil {
			t.Fatalf("UpdateDashboard failed: %v", err)
		}
		if resp.GetDescription() != "New Description" {
			t.Errorf("Expected description 'New Description', got %q", resp.GetDescription())
		}
	})

	t.Run("prevents unauthorized ownership modification during update", func(t *testing.T) {
		var updatedDash *dpb.Dashboard
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{
					Name:  "dashboards/" + dashboardId,
					Title: "Original",
					Owner: "original-owner@example.com",
				}, nil
			},
			updateDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error) {
				updatedDash = dashboard
				return dashboard, nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "original-owner@example.com", "u-1", nil
			},
			isUserAuthorizedFn: func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
				return true, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, nil)

		req := &spb.UpdateDashboardRequest{
			Dashboard: &dpb.Dashboard{
				Name:  "dashboards/dash-123",
				Title: "Updated Title",
				Owner: "attacker@example.com",
			},
		}

		resp, err := s.UpdateDashboard(ctx, req)
		if err != nil {
			t.Fatalf("UpdateDashboard failed: %v", err)
		}
		if resp.GetOwner() != "original-owner@example.com" {
			t.Errorf("Expected owner to remain 'original-owner@example.com', got %q", resp.GetOwner())
		}
		if updatedDash == nil || updatedDash.GetOwner() != "original-owner@example.com" {
			t.Errorf("Expected repo to receive original owner, got %v", updatedDash)
		}
	})

	t.Run("unauthenticated caller returns Unauthenticated", func(t *testing.T) {
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "", "", errors.New("unauthenticated")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, authMock, nil)

		_, err := s.UpdateDashboard(ctx, &spb.UpdateDashboardRequest{
			Dashboard: &dpb.Dashboard{Name: "dashboards/1", Title: "Title"},
		})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("Expected code Unauthenticated, got %v", status.Code(err))
		}
	})

	t.Run("invalid request returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, nil)

		tests := []struct {
			name string
			req  *spb.UpdateDashboardRequest
		}{
			{name: "nil request", req: nil},
			{name: "nil dashboard", req: &spb.UpdateDashboardRequest{}},
			{name: "empty name", req: &spb.UpdateDashboardRequest{Dashboard: &dpb.Dashboard{Title: "Title"}}},
			{name: "invalid name format", req: &spb.UpdateDashboardRequest{Dashboard: &dpb.Dashboard{Name: "invalid", Title: "Title"}}},
			{name: "invalid update mask path", req: &spb.UpdateDashboardRequest{
				Dashboard:  &dpb.Dashboard{Name: "dashboards/1", Title: "Title"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"owner"}},
			}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, err := s.UpdateDashboard(ctx, tc.req)
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
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, nil)

		_, err := s.UpdateDashboard(ctx, &spb.UpdateDashboardRequest{
			Dashboard: &dpb.Dashboard{Name: "dashboards/dash-123", Title: "Title"},
		})
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
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, nil)

		_, err := s.UpdateDashboard(ctx, &spb.UpdateDashboardRequest{
			Dashboard: &dpb.Dashboard{Name: "dashboards/dash-123", Title: "Title"},
		})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("unauthorized user rejected with PermissionDenied", func(t *testing.T) {
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

		_, err := s.UpdateDashboard(ctx, &spb.UpdateDashboardRequest{
			Dashboard: &dpb.Dashboard{Name: "dashboards/dash-123", Title: "Title"},
		})
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
		s := NewServer(&Config{}, repo, authMock, nil)

		_, err := s.UpdateDashboard(ctx, &spb.UpdateDashboardRequest{
			Dashboard: &dpb.Dashboard{Name: "dashboards/dash-123", Title: "Title"},
		})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("repo update error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{Name: "dashboards/" + dashboardId, Owner: "user@example.com"}, nil
			},
			updateDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error) {
				return nil, errors.New("update write failed")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, nil)

		_, err := s.UpdateDashboard(ctx, &spb.UpdateDashboardRequest{
			Dashboard: &dpb.Dashboard{Name: "dashboards/dash-123", Title: "Title"},
		})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("repo update returns not found returns NotFound", func(t *testing.T) {
		repo := &mockRepository{
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{Name: "dashboards/" + dashboardId, Owner: "user@example.com"}, nil
			},
			updateDashboardFn: func(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error) {
				return nil, status.Error(codes.NotFound, "dashboard not found")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, nil)

		_, err := s.UpdateDashboard(ctx, &spb.UpdateDashboardRequest{
			Dashboard: &dpb.Dashboard{Name: "dashboards/dash-123", Title: "Title"},
		})
		if status.Code(err) != codes.NotFound {
			t.Errorf("Expected code NotFound, got %v", status.Code(err))
		}
	})
}
