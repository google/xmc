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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestUpdateChart(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("successful full update by authorized writer", func(t *testing.T) {
		var updatedInRepo *dpb.Chart
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				if chartId != "chart-1" {
					t.Errorf("Expected chartId 'chart-1', got %q", chartId)
				}
				return &dpb.Chart{
					Name:        "charts/chart-1",
					Title:       "Old Title",
					Description: "Old Desc",
					Xid:         100,
					Wid:         1,
					CreateTime:  timestamppb.New(now.Add(-time.Hour)),
				}, nil
			},
			updateChartFn: func(ctx context.Context, chart *dpb.Chart) error {
				updatedInRepo = chart
				chart.UpdateTime = timestamppb.New(now)
				return nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "writer@example.com", "user-1", nil
			},
		}
		var capturedXIDReq *xidapipb.CheckUserAuthorizationRequest
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				capturedXIDReq = in
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, xidMock, nil)

		req := &spb.UpdateChartRequest{
			Chart: &dpb.Chart{
				Name:        "charts/chart-1",
				Title:       "New Title",
				Description: "New Description",
				Wid:         5,
				Plots: []*dpb.PlotSpec{
					{PlotId: "p1", Title: "Accuracy", Type: dpb.PlotType_SCATTER},
				},
				PlotLayout: &dpb.LayoutSpec{Type: dpb.LayoutSpec_GRID, Columns: 2},
			},
		}

		resp, err := s.UpdateChart(ctx, req)
		if err != nil {
			t.Fatalf("UpdateChart failed: %v", err)
		}

		if resp.GetTitle() != "New Title" {
			t.Errorf("Title: got %q, want 'New Title'", resp.GetTitle())
		}
		if resp.GetDescription() != "New Description" {
			t.Errorf("Description: got %q, want 'New Description'", resp.GetDescription())
		}
		if resp.GetWid() != 1 {
			t.Errorf("Wid: got %d, want 1 (wid must be preserved from existing chart)", resp.GetWid())
		}
		if resp.GetXid() != 100 {
			t.Errorf("Xid: got %d, want 100 (preserved from existing)", resp.GetXid())
		}
		if resp.GetName() != "charts/chart-1" {
			t.Errorf("Name: got %q, want 'charts/chart-1'", resp.GetName())
		}
		if resp.GetUpdateTime() == nil {
			t.Errorf("UpdateTime: expected non-nil")
		}
		if updatedInRepo == nil {
			t.Fatalf("Repository UpdateChart was not invoked")
		}
		if capturedXIDReq == nil || capturedXIDReq.GetName() != "xids/100" || capturedXIDReq.GetUser() != "writer@example.com" || capturedXIDReq.GetRoleType() != xpb.XidAcls_WRITER {
			t.Errorf("CheckUserAuthorization called with unexpected request: %v", capturedXIDReq)
		}
	})

	t.Run("successful partial update with field mask", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{
					Name:        "charts/chart-1",
					Title:       "Old Title",
					Description: "Old Desc",
					Xid:         100,
					Wid:         1,
					Plots:       []*dpb.PlotSpec{{PlotId: "p1", Title: "Original Plot"}},
				}, nil
			},
			updateChartFn: func(ctx context.Context, chart *dpb.Chart) error {
				chart.UpdateTime = timestamppb.New(now)
				return nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "writer@example.com", "user-1", nil
			},
		}
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, xidMock, nil)

		req := &spb.UpdateChartRequest{
			Chart: &dpb.Chart{
				Name:        "charts/chart-1",
				Description: "Updated Only Description",
				Wid:         999, // Should be ignored since not in mask
			},
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"description"},
			},
		}

		resp, err := s.UpdateChart(ctx, req)
		if err != nil {
			t.Fatalf("UpdateChart failed: %v", err)
		}

		if resp.GetTitle() != "Old Title" {
			t.Errorf("Title: got %q, want 'Old Title' (unchanged)", resp.GetTitle())
		}
		if resp.GetDescription() != "Updated Only Description" {
			t.Errorf("Description: got %q, want 'Updated Only Description'", resp.GetDescription())
		}
		if resp.GetWid() != 1 {
			t.Errorf("Wid: got %d, want 1 (unchanged)", resp.GetWid())
		}
		if len(resp.GetPlots()) != 1 || resp.GetPlots()[0].GetPlotId() != "p1" {
			t.Errorf("Plots: got %v, want unchanged original plots", resp.GetPlots())
		}
	})

	t.Run("unauthenticated caller returns Unauthenticated", func(t *testing.T) {
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "", "", errors.New("unauthenticated")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, authMock, &mockXidClient{}, nil)

		_, err := s.UpdateChart(ctx, &spb.UpdateChartRequest{
			Chart: &dpb.Chart{Name: "charts/chart-1", Title: "Title"},
		})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("Expected code Unauthenticated, got %v", status.Code(err))
		}
	})

	t.Run("invalid request returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)

		tests := []struct {
			name string
			req  *spb.UpdateChartRequest
		}{
			{name: "nil request", req: nil},
			{name: "nil chart", req: &spb.UpdateChartRequest{}},
			{name: "empty name", req: &spb.UpdateChartRequest{Chart: &dpb.Chart{Title: "Title"}}},
			{name: "invalid name format", req: &spb.UpdateChartRequest{Chart: &dpb.Chart{Name: "invalid", Title: "Title"}}},
			{name: "hierarchical work unit name rejected", req: &spb.UpdateChartRequest{Chart: &dpb.Chart{Name: "experiments/1/workUnits/2/charts/c1", Title: "Title"}}},
			{name: "hierarchical dashboard name rejected", req: &spb.UpdateChartRequest{Chart: &dpb.Chart{Name: "dashboards/d1/charts/c1", Title: "Title"}}},
			{name: "full update with empty title", req: &spb.UpdateChartRequest{Chart: &dpb.Chart{Name: "charts/c1", Title: ""}}},
			{name: "full update with whitespace title", req: &spb.UpdateChartRequest{Chart: &dpb.Chart{Name: "charts/c1", Title: "   "}}},
			{name: "invalid update mask path", req: &spb.UpdateChartRequest{
				Chart:      &dpb.Chart{Name: "charts/c1", Title: "Title"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
			}},
			{name: "update mask title with empty title", req: &spb.UpdateChartRequest{
				Chart:      &dpb.Chart{Name: "charts/c1", Title: " "},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, err := s.UpdateChart(ctx, tc.req)
				if status.Code(err) != codes.InvalidArgument {
					t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
				}
			})
		}
	})

	t.Run("chart not found on fetch returns NotFound", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return nil, status.Error(codes.NotFound, "not found")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.UpdateChart(ctx, &spb.UpdateChartRequest{
			Chart: &dpb.Chart{Name: "charts/nonexistent", Title: "Title"},
		})
		if status.Code(err) != codes.NotFound {
			t.Errorf("Expected code NotFound, got %v", status.Code(err))
		}
	})

	t.Run("storage error on fetch returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return nil, errors.New("read error")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.UpdateChart(ctx, &spb.UpdateChartRequest{
			Chart: &dpb.Chart{Name: "charts/chart-1", Title: "Title"},
		})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("nil chart returned by repo returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return nil, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.UpdateChart(ctx, &spb.UpdateChartRequest{
			Chart: &dpb.Chart{Name: "charts/chart-1", Title: "Title"},
		})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("missing valid experiment XID returns FailedPrecondition", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "charts/chart-1", Title: "Title", Xid: 0}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		_, err := s.UpdateChart(ctx, &spb.UpdateChartRequest{
			Chart: &dpb.Chart{Name: "charts/chart-1", Title: "Title"},
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("Expected code FailedPrecondition, got %v", status.Code(err))
		}
	})

	t.Run("unauthorized user returns PermissionDenied", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "charts/chart-1", Title: "Title", Xid: 100}, nil
			},
		}
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, nil)

		_, err := s.UpdateChart(ctx, &spb.UpdateChartRequest{
			Chart: &dpb.Chart{Name: "charts/chart-1", Title: "Title"},
		})
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("Expected code PermissionDenied, got %v", status.Code(err))
		}
	})

	t.Run("xid client check error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "charts/chart-1", Title: "Title", Xid: 100}, nil
			},
		}
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return nil, errors.New("rpc unavailable")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, nil)

		_, err := s.UpdateChart(ctx, &spb.UpdateChartRequest{
			Chart: &dpb.Chart{Name: "charts/chart-1", Title: "Title"},
		})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("nil repo in server struct returns FailedPrecondition", func(t *testing.T) {
		s := &Server{
			auth:      &mockAuthorizer{},
			xidClient: &mockXidClient{},
		}
		_, err := s.UpdateChart(ctx, &spb.UpdateChartRequest{
			Chart: &dpb.Chart{Name: "charts/chart-1", Title: "Title"},
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("Expected code FailedPrecondition, got %v", status.Code(err))
		}
	})

	t.Run("repo update error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "charts/chart-1", Title: "Title", Xid: 100}, nil
			},
			updateChartFn: func(ctx context.Context, chart *dpb.Chart) error {
				return errors.New("write timeout")
			},
		}
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, nil)

		_, err := s.UpdateChart(ctx, &spb.UpdateChartRequest{
			Chart: &dpb.Chart{Name: "charts/chart-1", Title: "Title"},
		})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("repo update returns not found returns NotFound", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "charts/chart-1", Title: "Title", Xid: 100}, nil
			},
			updateChartFn: func(ctx context.Context, chart *dpb.Chart) error {
				return status.Error(codes.NotFound, "chart not found")
			},
		}
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, nil)

		_, err := s.UpdateChart(ctx, &spb.UpdateChartRequest{
			Chart: &dpb.Chart{Name: "charts/chart-1", Title: "Title"},
		})
		if status.Code(err) != codes.NotFound {
			t.Errorf("Expected code NotFound, got %v", status.Code(err))
		}
	})

	t.Run("metadata propagation forwards auth headers to downstream XID service", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "charts/chart-1", Title: "Title", Xid: 100}, nil
			},
			updateChartFn: func(ctx context.Context, chart *dpb.Chart) error {
				return nil
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
			"x-goog-iap-jwt-assertion", "jwt-token-789",
			"x-goog-user-email", "writer@example.com",
			"authorization", "Bearer writer-token",
		)
		inCtx := metadata.NewIncomingContext(ctx, inMD)

		_, err := s.UpdateChart(inCtx, &spb.UpdateChartRequest{
			Chart: &dpb.Chart{Name: "charts/chart-1", Title: "Title"},
		})
		if err != nil {
			t.Fatalf("UpdateChart failed: %v", err)
		}
		if capturedMD == nil {
			t.Fatal("Expected outgoing metadata to be populated, got nil")
		}
		if got := capturedMD.Get("x-goog-iap-jwt-assertion"); len(got) == 0 || got[0] != "jwt-token-789" {
			t.Errorf("Expected jwt assertion %q, got %v", "jwt-token-789", got)
		}
		if got := capturedMD.Get("x-goog-user-email"); len(got) == 0 || got[0] != "writer@example.com" {
			t.Errorf("Expected user email %q, got %v", "writer@example.com", got)
		}
		if got := capturedMD.Get("authorization"); len(got) == 0 || got[0] != "Bearer writer-token" {
			t.Errorf("Expected authorization header %q, got %v", "Bearer writer-token", got)
		}
	})
}
