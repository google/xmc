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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/internal/auth"
	xpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestListDashboards(t *testing.T) {
	ctx := context.Background()

	t.Run("list with full authorized results", func(t *testing.T) {
		repo := &mockRepository{
			listDashboardsFn: func(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error) {
				return []*dpb.Dashboard{
					{Name: "dashboards/1", Owner: "user@example.com"},
					{Name: "dashboards/2", Owner: "user@example.com"},
				}, "next-tok", nil
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

		resp, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{PageSize: 10})
		if err != nil {
			t.Fatalf("ListDashboards failed: %v", err)
		}
		if len(resp.GetDashboards()) != 2 {
			t.Fatalf("Expected 2 dashboards, got %d", len(resp.GetDashboards()))
		}
		if resp.GetNextPageToken() != "next-tok" {
			t.Errorf("Expected next page token 'next-tok', got %q", resp.GetNextPageToken())
		}
	})

	t.Run("filters out unauthorized dashboards", func(t *testing.T) {
		repo := &mockRepository{
			listDashboardsFn: func(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error) {
				return []*dpb.Dashboard{
					{Name: "dashboards/1", Owner: "user@example.com"},
					{Name: "dashboards/2", Owner: "other@example.com"},
					{Name: "dashboards/3", Owner: "user@example.com"},
				}, "", nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "user@example.com", "u-1", nil
			},
			isUserAuthorizedFn: func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
				if len(acls.GetOwners()) > 0 && acls.GetOwners()[0].GetUser() == user {
					return true, nil
				}
				return false, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, nil)

		resp, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{})
		if err != nil {
			t.Fatalf("ListDashboards failed: %v", err)
		}
		if len(resp.GetDashboards()) != 2 {
			t.Fatalf("Expected 2 authorized dashboards, got %d", len(resp.GetDashboards()))
		}
		if resp.GetDashboards()[0].GetName() != "dashboards/1" || resp.GetDashboards()[1].GetName() != "dashboards/3" {
			t.Errorf("Unexpected filtered list: %v", resp.GetDashboards())
		}
	})

	t.Run("empty list", func(t *testing.T) {
		repo := &mockRepository{
			listDashboardsFn: func(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error) {
				return nil, "", nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, nil)

		resp, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{})
		if err != nil {
			t.Fatalf("ListDashboards failed: %v", err)
		}
		if len(resp.GetDashboards()) != 0 {
			t.Errorf("Expected 0 dashboards, got %d", len(resp.GetDashboards()))
		}
	})

	t.Run("unauthenticated caller returns Unauthenticated", func(t *testing.T) {
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "", "", errors.New("missing token")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, authMock, nil)

		_, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("Expected code Unauthenticated, got %v", status.Code(err))
		}
	})

	t.Run("negative page size returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, nil)

		_, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{PageSize: -1})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
		}
	})

	t.Run("repo error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			listDashboardsFn: func(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error) {
				return nil, "", errors.New("storage query failure")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, nil)

		_, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("authorization check error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			listDashboardsFn: func(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error) {
				return []*dpb.Dashboard{{Name: "dashboards/1"}}, "", nil
			},
		}
		authMock := &mockAuthorizer{
			isUserAuthorizedFn: func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
				return false, errors.New("auth service error")
			},
		}
		s := NewServer(&Config{}, repo, authMock, nil)

		_, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})
}
