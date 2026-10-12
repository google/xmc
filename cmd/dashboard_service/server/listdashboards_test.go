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

	spb "github.com/google/xmc/api/protos/dashboard"
)

func TestListDashboards(t *testing.T) {
	ctx := context.Background()

	t.Run("returns all dashboards without owner filtering", func(t *testing.T) {
		repo := &mockRepository{
			listDashboardsFn: func(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*spb.Dashboard, string, error) {
				return []*spb.Dashboard{
					{Name: "dashboards/1", Owner: "user@example.com"},
					{Name: "dashboards/2", Owner: "other@example.com"},
					{Name: "dashboards/3", Owner: "third@example.com"},
				}, "next-tok", nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "user@example.com", "u-1", nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		resp, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{PageSize: 10})
		if err != nil {
			t.Fatalf("ListDashboards failed: %v", err)
		}
		if len(resp.GetDashboards()) != 3 {
			t.Fatalf("Expected 3 dashboards, got %d", len(resp.GetDashboards()))
		}
		if resp.GetNextPageToken() != "next-tok" {
			t.Errorf("Expected next page token 'next-tok', got %q", resp.GetNextPageToken())
		}
	})

	t.Run("empty list", func(t *testing.T) {
		repo := &mockRepository{
			listDashboardsFn: func(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*spb.Dashboard, string, error) {
				return nil, "", nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

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
		s := NewServer(&Config{}, &mockRepository{}, authMock, &mockXidClient{}, nil)

		_, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("Expected code Unauthenticated, got %v", status.Code(err))
		}
	})

	t.Run("negative page size returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{PageSize: -1})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
		}
	})

	t.Run("page size is bounded before reaching the repository", func(t *testing.T) {
		testCases := []struct {
			name      string
			requested int32
			want      int32
		}{
			{name: "unset defaults to 10", requested: 0, want: defaultDashboardPageSize},
			{name: "below cap is preserved", requested: 25, want: 25},
			{name: "at cap is preserved", requested: maxDashboardPageSize, want: maxDashboardPageSize},
			{name: "above cap is clamped to 100", requested: 5000, want: maxDashboardPageSize},
		}
		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				var gotPageSize int32
				repo := &mockRepository{
					listDashboardsFn: func(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*spb.Dashboard, string, error) {
						gotPageSize = pageSize
						return nil, "", nil
					},
				}
				s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

				if _, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{PageSize: tc.requested}); err != nil {
					t.Fatalf("ListDashboards failed: %v", err)
				}
				if gotPageSize != tc.want {
					t.Errorf("Expected repository page size %d, got %d", tc.want, gotPageSize)
				}
			})
		}
	})

	t.Run("repo error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			listDashboardsFn: func(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*spb.Dashboard, string, error) {
				return nil, "", errors.New("storage query failure")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})
}
