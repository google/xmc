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

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	spb "xmanager_cloud/api/protos/dashboard"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestGetChart(t *testing.T) {
	ctx := context.Background()

	t.Run("successful retrieval under root pattern", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*spb.Chart, error) {
				if chartId != "chart-1" {
					t.Errorf("Expected chartId %q, got %q", "chart-1", chartId)
				}
				return &spb.Chart{
					Name:  "charts/chart-1",
					Title: "Loss Curve",
					Xid:   100,
					Wid:   2,
				}, nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "reader@example.com", "user-1", nil
			},
		}
		var capturedReq *xidapipb.CheckUserAuthorizationRequest
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				capturedReq = in
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, xidMock, nil)

		req := &spb.GetChartRequest{Name: "charts/chart-1"}
		resp, err := s.GetChart(ctx, req)
		if err != nil {
			t.Fatalf("GetChart failed: %v", err)
		}
		if resp.GetName() != "charts/chart-1" {
			t.Errorf("Expected name %q, got %q", "charts/chart-1", resp.GetName())
		}
		if resp.GetTitle() != "Loss Curve" {
			t.Errorf("Expected title %q, got %q", "Loss Curve", resp.GetTitle())
		}
		if capturedReq == nil || capturedReq.GetName() != "xids/100" || capturedReq.GetUser() != "reader@example.com" || capturedReq.GetRoleType() != xpb.XidAcls_READER {
			t.Errorf("CheckUserAuthorization called with unexpected request: %v", capturedReq)
		}
	})

	t.Run("unauthenticated caller returns Unauthenticated", func(t *testing.T) {
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "", "", errors.New("missing auth credentials")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, authMock, &mockXidClient{}, nil)

		_, err := s.GetChart(ctx, &spb.GetChartRequest{Name: "charts/chart-1"})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("Expected code Unauthenticated, got %v", status.Code(err))
		}
	})

	t.Run("invalid request returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)

		tests := []struct {
			name string
			req  *spb.GetChartRequest
		}{
			{name: "nil request", req: nil},
			{name: "empty name", req: &spb.GetChartRequest{Name: ""}},
			{name: "invalid name format", req: &spb.GetChartRequest{Name: "invalid-name"}},
			{name: "work unit hierarchical path rejected", req: &spb.GetChartRequest{Name: "experiments/1/workUnits/2/charts/c1"}},
			{name: "dashboard hierarchical path rejected", req: &spb.GetChartRequest{Name: "dashboards/dash-1/charts/c1"}},
			{name: "empty chart id", req: &spb.GetChartRequest{Name: "charts/"}},
			{name: "whitespace chart id", req: &spb.GetChartRequest{Name: "charts/   "}},
			{name: "extra path parts", req: &spb.GetChartRequest{Name: "charts/c1/extra"}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, err := s.GetChart(ctx, tc.req)
				if status.Code(err) != codes.InvalidArgument {
					t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
				}
			})
		}
	})

	t.Run("nil repo in server struct returns FailedPrecondition", func(t *testing.T) {
		s := &Server{
			auth:      &mockAuthorizer{},
			xidClient: &mockXidClient{},
		}
		_, err := s.GetChart(ctx, &spb.GetChartRequest{Name: "charts/chart-1"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("Expected code FailedPrecondition, got %v", status.Code(err))
		}
	})

	t.Run("chart not found in repository returns NotFound", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*spb.Chart, error) {
				return nil, status.Error(codes.NotFound, "not found")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.GetChart(ctx, &spb.GetChartRequest{Name: "charts/chart-not-found"})
		if status.Code(err) != codes.NotFound {
			t.Errorf("Expected code NotFound, got %v", status.Code(err))
		}
	})

	t.Run("repository error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*spb.Chart, error) {
				return nil, errors.New("spanner timeout")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.GetChart(ctx, &spb.GetChartRequest{Name: "charts/chart-1"})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("unauthorized user returns PermissionDenied", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*spb.Chart, error) {
				return &spb.Chart{
					Name:  "charts/chart-1",
					Title: "Secret Chart",
					Xid:   100,
					Wid:   1,
				}, nil
			},
		}
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, nil)

		_, err := s.GetChart(ctx, &spb.GetChartRequest{Name: "charts/chart-1"})
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("Expected code PermissionDenied, got %v", status.Code(err))
		}
	})

	t.Run("metadata propagation forwards auth headers to downstream XID service", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*spb.Chart, error) {
				return &spb.Chart{
					Name:  "charts/chart-1",
					Title: "Chart",
					Xid:   100,
					Wid:   1,
				}, nil
			},
		}
		var capturedMD metadata.MD
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				if md, ok := metadata.FromOutgoingContext(ctx); ok {
					capturedMD = md
				}
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, nil)

		inMD := metadata.Pairs(
			"x-goog-iap-jwt-assertion", "jwt-token-123",
			"x-goog-user-email", "user@example.com",
			"authorization", "Bearer token-456",
		)
		inCtx := metadata.NewIncomingContext(ctx, inMD)

		_, err := s.GetChart(inCtx, &spb.GetChartRequest{Name: "charts/chart-1"})
		if err != nil {
			t.Fatalf("GetChart failed: %v", err)
		}
		if capturedMD == nil {
			t.Fatal("Expected outgoing metadata to be populated, got nil")
		}
		if got := capturedMD.Get("x-goog-iap-jwt-assertion"); len(got) == 0 || got[0] != "jwt-token-123" {
			t.Errorf("Expected jwt assertion %q, got %v", "jwt-token-123", got)
		}
		if got := capturedMD.Get("x-goog-user-email"); len(got) == 0 || got[0] != "user@example.com" {
			t.Errorf("Expected user email %q, got %v", "user@example.com", got)
		}
		if got := capturedMD.Get("authorization"); len(got) == 0 || got[0] != "Bearer token-456" {
			t.Errorf("Expected authorization header %q, got %v", "Bearer token-456", got)
		}
	})
}
