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

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestListCharts(t *testing.T) {
	ctx := context.Background()

	t.Run("returns charts and forwards pagination arguments to the repository", func(t *testing.T) {
		var gotParent, gotPageToken string
		var gotPageSize int32
		repo := &mockRepository{
			listChartsFn: func(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Chart, string, error) {
				gotParent, gotPageSize, gotPageToken = parent, pageSize, pageToken
				return []*dpb.Chart{
					{Name: "charts/1", Title: "Loss", Xid: 42},
					{Name: "charts/2", Title: "Accuracy", Xid: 42},
				}, "next-tok", nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		resp, err := s.ListCharts(ctx, &spb.ListChartsRequest{
			Parent:    "dashboards/1",
			PageSize:  2,
			PageToken: "tok-1",
		})
		if err != nil {
			t.Fatalf("ListCharts failed: %v", err)
		}
		if len(resp.GetCharts()) != 2 {
			t.Fatalf("Expected 2 charts, got %d", len(resp.GetCharts()))
		}
		if resp.GetNextPageToken() != "next-tok" {
			t.Errorf("Expected next page token 'next-tok', got %q", resp.GetNextPageToken())
		}
		if gotParent != "dashboards/1" {
			t.Errorf("Expected parent 'dashboards/1', got %q", gotParent)
		}
		if gotPageSize != 2 {
			t.Errorf("Expected page size 2, got %d", gotPageSize)
		}
		if gotPageToken != "tok-1" {
			t.Errorf("Expected page token 'tok-1', got %q", gotPageToken)
		}
	})

	t.Run("work unit parent is forwarded verbatim", func(t *testing.T) {
		var gotParent string
		repo := &mockRepository{
			listChartsFn: func(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Chart, string, error) {
				gotParent = parent
				return []*dpb.Chart{{Name: "experiments/42/workUnits/7/charts/loss"}}, "", nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		resp, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "experiments/42/workUnits/7"})
		if err != nil {
			t.Fatalf("ListCharts failed: %v", err)
		}
		if gotParent != "experiments/42/workUnits/7" {
			t.Errorf("Expected parent 'experiments/42/workUnits/7', got %q", gotParent)
		}
		if len(resp.GetCharts()) != 1 {
			t.Errorf("Expected 1 chart, got %d", len(resp.GetCharts()))
		}
	})

	t.Run("work unit parent requires experiment READER access", func(t *testing.T) {
		var gotXidName, gotUser string
		var gotRole xpb.XidAcls_RoleType
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				gotXidName, gotUser, gotRole = in.GetName(), in.GetUser(), in.GetRoleType()
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)}, nil
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, xidMock, nil)

		_, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "experiments/42/workUnits/7"})
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("Expected code PermissionDenied, got %v", status.Code(err))
		}
		if gotXidName != "xids/42" {
			t.Errorf("Expected xid name 'xids/42', got %q", gotXidName)
		}
		if gotUser != "user@example.com" {
			t.Errorf("Expected authenticated user 'user@example.com', got %q", gotUser)
		}
		if gotRole != xpb.XidAcls_READER {
			t.Errorf("Expected role READER, got %v", gotRole)
		}
	})

	t.Run("dashboard parent is not authorized as a whole", func(t *testing.T) {
		called := false
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				called = true
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, xidMock, nil)

		if _, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "dashboards/1"}); err != nil {
			t.Fatalf("ListCharts failed: %v", err)
		}
		if called {
			t.Error("Expected no XID authorization call when a dashboard parent yields no charts")
		}
	})

	t.Run("dashboard parent prunes charts the caller cannot read", func(t *testing.T) {
		repo := &mockRepository{
			listChartsFn: func(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Chart, string, error) {
				return []*dpb.Chart{
					{Name: "charts/readable", Xid: 11},
					{Name: "charts/forbidden", Xid: 22},
				}, "next-tok", nil
			},
		}
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(in.GetName() == "xids/11")}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, nil)

		resp, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "dashboards/1"})
		if err != nil {
			t.Fatalf("ListCharts failed: %v", err)
		}
		if len(resp.GetCharts()) != 1 || resp.GetCharts()[0].GetName() != "charts/readable" {
			t.Fatalf("Expected only 'charts/readable', got %d charts", len(resp.GetCharts()))
		}
		// Pruning must not suppress the token: AIP-158 clients keep following it even on a short page.
		if resp.GetNextPageToken() != "next-tok" {
			t.Errorf("Expected next page token 'next-tok', got %q", resp.GetNextPageToken())
		}
	})

	t.Run("dashboard parent propagates XID service failure as Internal", func(t *testing.T) {
		repo := &mockRepository{
			listChartsFn: func(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Chart, string, error) {
				return []*dpb.Chart{{Name: "charts/1", Xid: 11}}, "", nil
			},
		}
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return nil, errors.New("xid service unavailable")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, nil)

		_, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "dashboards/1"})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("XID service failure returns Internal", func(t *testing.T) {
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return nil, errors.New("xid service unavailable")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, xidMock, nil)

		_, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "experiments/42/workUnits/7"})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("empty result returns non-nil slice and empty page token", func(t *testing.T) {
		repo := &mockRepository{
			listChartsFn: func(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Chart, string, error) {
				return nil, "", nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		resp, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "dashboards/1"})
		if err != nil {
			t.Fatalf("ListCharts failed: %v", err)
		}
		if resp.GetCharts() == nil {
			t.Error("Expected non-nil charts slice, got nil")
		}
		if len(resp.GetCharts()) != 0 {
			t.Errorf("Expected 0 charts, got %d", len(resp.GetCharts()))
		}
		if resp.GetNextPageToken() != "" {
			t.Errorf("Expected empty next page token, got %q", resp.GetNextPageToken())
		}
	})

	t.Run("page size defaults and cap follow AIP-158", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			requested int32
			want      int32
		}{
			{name: "unset defaults to 10", requested: 0, want: defaultChartPageSize},
			{name: "in-range value is preserved", requested: 25, want: 25},
			{name: "at cap is preserved", requested: maxChartPageSize, want: maxChartPageSize},
			{name: "above cap is clamped to 100", requested: 5000, want: maxChartPageSize},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var gotPageSize int32
				repo := &mockRepository{
					listChartsFn: func(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Chart, string, error) {
						gotPageSize = pageSize
						return nil, "", nil
					},
				}
				s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

				if _, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "dashboards/1", PageSize: tc.requested}); err != nil {
					t.Fatalf("ListCharts failed: %v", err)
				}
				if gotPageSize != tc.want {
					t.Errorf("Expected repository page size %d, got %d", tc.want, gotPageSize)
				}
			})
		}
	})

	t.Run("nil request returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.ListCharts(ctx, nil)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
		}
	})

	t.Run("unauthenticated caller returns Unauthenticated", func(t *testing.T) {
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "", "", errors.New("missing token")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, authMock, &mockXidClient{}, nil)

		_, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "dashboards/1"})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("Expected code Unauthenticated, got %v", status.Code(err))
		}
	})

	t.Run("missing parent returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.ListCharts(ctx, &spb.ListChartsRequest{})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
		}
	})

	t.Run("unsupported parent shapes return InvalidArgument", func(t *testing.T) {
		for _, parent := range []string{
			"experiments/123",             // experiment is not a chart collection parent
			"experiments/123/workUnits",   // truncated work unit name
			"experiments/abc/workUnits/1", // non-numeric xid
			"dashboards/1/charts/2",       // chart name, not a parent
			"charts/1",
		} {
			t.Run(parent, func(t *testing.T) {
				s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)

				_, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: parent})
				if status.Code(err) != codes.InvalidArgument {
					t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
				}
			})
		}
	})

	t.Run("negative page size returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "dashboards/1", PageSize: -1})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
		}
	})

	t.Run("nil repo in server struct returns FailedPrecondition", func(t *testing.T) {
		s := &Server{
			auth:      &mockAuthorizer{},
			xidClient: &mockXidClient{},
		}

		_, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "dashboards/1"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("Expected code FailedPrecondition, got %v", status.Code(err))
		}
	})

	t.Run("repo error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			listChartsFn: func(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Chart, string, error) {
				return nil, "", errors.New("storage query failure")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "dashboards/1"})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})
}
