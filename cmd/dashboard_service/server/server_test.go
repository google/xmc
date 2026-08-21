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

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/internal/auth"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

type (
	createDashboardFunc = func(ctx context.Context, dashboard *dpb.Dashboard) error
	getDashboardFunc    = func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error)
	listDashboardsFunc  = func(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error)
	updateDashboardFunc = func(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error)
	deleteDashboardFunc = func(ctx context.Context, dashboardId string) error
	createChartFunc     = func(ctx context.Context, chart *dpb.Chart) error
	getChartFunc        = func(ctx context.Context, chartId string) (*dpb.Chart, error)
)

type mockRepository struct {
	createDashboardFn createDashboardFunc
	getDashboardFn    getDashboardFunc
	listDashboardsFn  listDashboardsFunc
	updateDashboardFn updateDashboardFunc
	deleteDashboardFn deleteDashboardFunc
	createChartFn     createChartFunc
	getChartFn        getChartFunc
}

func (m *mockRepository) CreateChart(ctx context.Context, chart *dpb.Chart) error {
	if m.createChartFn != nil {
		return m.createChartFn(ctx, chart)
	}
	return nil
}

func (m *mockRepository) CreateDashboard(ctx context.Context, dashboard *dpb.Dashboard) error {
	if m.createDashboardFn != nil {
		return m.createDashboardFn(ctx, dashboard)
	}
	return nil
}

func (m *mockRepository) GetDashboard(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
	if m.getDashboardFn != nil {
		return m.getDashboardFn(ctx, dashboardId)
	}
	return nil, status.Error(codes.NotFound, "not found")
}

func (m *mockRepository) ListDashboards(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error) {
	if m.listDashboardsFn != nil {
		return m.listDashboardsFn(ctx, pageSize, pageToken, filter, orderBy, staleness)
	}
	return nil, "", nil
}

func (m *mockRepository) UpdateDashboard(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error) {
	if m.updateDashboardFn != nil {
		return m.updateDashboardFn(ctx, dashboard, updateMask)
	}
	return dashboard, nil
}

func (m *mockRepository) DeleteDashboard(ctx context.Context, dashboardId string) error {
	if m.deleteDashboardFn != nil {
		return m.deleteDashboardFn(ctx, dashboardId)
	}
	return nil
}

func (m *mockRepository) GetChart(ctx context.Context, chartId string) (*dpb.Chart, error) {
	if m.getChartFn != nil {
		return m.getChartFn(ctx, chartId)
	}
	return nil, status.Error(codes.NotFound, "not found")
}

type mockAuthorizer struct {
	authenticateFn          func(ctx context.Context) (string, string, error)
	isUserAuthorizedFn      func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error)
	isUserAuthorizedChartFn func(ctx context.Context, user string, acls *xpb.XidAcls) (bool, error)
}

func (m *mockAuthorizer) AuthenticateUserFromContext(ctx context.Context) (string, string, error) {
	if m.authenticateFn != nil {
		return m.authenticateFn(ctx)
	}
	return "user@example.com", "user-id-123", nil
}

func (m *mockAuthorizer) IsUserAuthorized(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
	if m.isUserAuthorizedFn != nil {
		return m.isUserAuthorizedFn(ctx, user, acls, role)
	}
	return true, nil
}

func (m *mockAuthorizer) IsUserAuthorizedForChart(ctx context.Context, user string, acls *xpb.XidAcls) (bool, error) {
	if m.isUserAuthorizedChartFn != nil {
		return m.isUserAuthorizedChartFn(ctx, user, acls)
	}
	return true, nil
}

type mockXidClient struct {
	xidapipb.XidServiceClient
	checkUserAuthorizationFn func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error)
}

func (m *mockXidClient) CheckUserAuthorization(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
	if m.checkUserAuthorizationFn != nil {
		return m.checkUserAuthorizationFn(ctx, in, opts...)
	}
	return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
}

type mockChartDataEngine struct {
	fetchChartDataFn func(ctx context.Context, chart *dpb.Chart, req *spb.GetChartDataRequest) (*dpb.ChartData, error)
}

func (m *mockChartDataEngine) FetchChartData(ctx context.Context, chart *dpb.Chart, req *spb.GetChartDataRequest) (*dpb.ChartData, error) {
	if m.fetchChartDataFn != nil {
		return m.fetchChartDataFn(ctx, chart, req)
	}
	return &dpb.ChartData{
		Name:      chart.GetName(),
		Xid:       chart.GetXid(),
		Wid:       chart.GetWid(),
		PlotsData: []*dpb.ChartData_PlotData{},
	}, nil
}
func TestGetChart(t *testing.T) {
	t.Run("returns chart from repository when found", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{
					Name:  "dashboards/123/charts/456",
					Title: "Sample Chart",
					Xid:   100,
					Wid:   1,
				}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)
		ctx := context.Background()

		req := &spb.GetChartRequest{Name: "dashboards/123/charts/456"}
		resp, err := s.GetChart(ctx, req)
		if err != nil {
			t.Fatalf("GetChart failed: %v", err)
		}
		if resp.GetName() != "dashboards/123/charts/456" {
			t.Errorf("GetChart().Name = %q, want %q", resp.GetName(), "dashboards/123/charts/456")
		}
	})

	t.Run("returns internal error when repository returns nil chart without error", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return nil, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)
		ctx := context.Background()

		req := &spb.GetChartRequest{Name: "dashboards/123/charts/456"}
		_, err := s.GetChart(ctx, req)
		if status.Code(err) != codes.Internal {
			t.Errorf("GetChart(nil chart) code = %v, want %v", status.Code(err), codes.Internal)
		}
	})

	t.Run("returns InvalidArgument when experiment chart without work unit is requested", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)
		ctx := context.Background()

		req := &spb.GetChartRequest{Name: "experiments/1001/charts/baseline"}
		_, err := s.GetChart(ctx, req)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("GetChart(experiment chart without work unit) code = %v, want %v", status.Code(err), codes.InvalidArgument)
		}
	})

	t.Run("synthesizes default chart for work unit 0 when not in repository", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return nil, status.Error(codes.NotFound, "not found")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)
		ctx := context.Background()

		req := &spb.GetChartRequest{Name: "experiments/1001/workUnits/0/charts/baseline"}
		resp, err := s.GetChart(ctx, req)
		if err != nil {
			t.Fatalf("GetChart failed: %v", err)
		}
		if resp.GetName() != "experiments/1001/workUnits/0/charts/baseline" {
			t.Errorf("GetChart().Name = %q, want %q", resp.GetName(), "experiments/1001/workUnits/0/charts/baseline")
		}
		if resp.GetXid() != 1001 {
			t.Errorf("GetChart().Xid = %d, want 1001", resp.GetXid())
		}
		if resp.GetWid() != 0 {
			t.Errorf("GetChart().Wid = %d, want 0", resp.GetWid())
		}
		if len(resp.GetPlots()) == 0 {
			t.Errorf("GetChart() expected default plots, got empty")
		}
	})

	t.Run("unauthenticated user returns Unauthenticated", func(t *testing.T) {
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "", "", status.Error(codes.Unauthenticated, "unauthenticated")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, authMock, &mockXidClient{}, nil)
		_, err := s.GetChart(context.Background(), &spb.GetChartRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("GetChart() code = %v, want %v", status.Code(err), codes.Unauthenticated)
		}
	})

	t.Run("chart missing valid xid returns FailedPrecondition", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "dashboards/123/charts/456", Xid: 0, Wid: 1}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)
		_, err := s.GetChart(context.Background(), &spb.GetChartRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("GetChart() code = %v, want %v", status.Code(err), codes.FailedPrecondition)
		}
	})

	t.Run("unauthorized user returns PermissionDenied", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "dashboards/123/charts/456", Xid: 100, Wid: 1}, nil
			},
		}
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, nil)
		_, err := s.GetChart(context.Background(), &spb.GetChartRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("GetChart() code = %v, want %v", status.Code(err), codes.PermissionDenied)
		}
	})

	t.Run("authorized user successfully gets chart and verifies permissions", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{
					Name:  "dashboards/123/charts/456",
					Title: "Sample Chart",
					Xid:   100,
					Wid:   1,
				}, nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "user@example.com", "uid-1", nil
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
		resp, err := s.GetChart(context.Background(), &spb.GetChartRequest{Name: "dashboards/123/charts/456"})
		if err != nil {
			t.Fatalf("GetChart() unexpected error: %v", err)
		}
		if resp.GetName() != "dashboards/123/charts/456" {
			t.Errorf("GetChart().Name = %q, want %q", resp.GetName(), "dashboards/123/charts/456")
		}
		if capturedReq == nil || capturedReq.GetName() != "xids/100" || capturedReq.GetUser() != "user@example.com" || capturedReq.GetRoleType() != xpb.XidAcls_READER {
			t.Errorf("unexpected authorization check request: %+v", capturedReq)
		}
	})
}

func TestNewServer(t *testing.T) {
	t.Run("panics on nil repo", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("expected NewServer to panic on nil repo, but it did not")
			}
		}()
		NewServer(&Config{}, nil, &mockAuthorizer{}, &mockXidClient{}, nil)
	})

	t.Run("panics on nil auth", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("expected NewServer to panic on nil auth, but it did not")
			}
		}()
		NewServer(&Config{}, &mockRepository{}, nil, &mockXidClient{}, nil)
	})

	t.Run("panics on nil xidClient", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("expected NewServer to panic on nil xidClient, but it did not")
			}
		}()
		NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, nil, nil)
	})

	t.Run("successful server initialization", func(t *testing.T) {
		s := NewServer(&Config{Port: "8080"}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)
		if s == nil {
			t.Fatal("expected non-nil server")
		}
	})
}

func TestGetChartData(t *testing.T) {
	ctx := context.Background()

	t.Run("nil request returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)
		_, err := s.GetChartData(ctx, nil)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("GetChartData(nil) code = %v, want %v", status.Code(err), codes.InvalidArgument)
		}
	})

	t.Run("invalid chart name returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "invalid-name"})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("GetChartData(invalid name) code = %v, want %v", status.Code(err), codes.InvalidArgument)
		}
	})

	t.Run("negative chart width returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456", ChartWidthPixels: -50})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("GetChartData(negative width) code = %v, want %v", status.Code(err), codes.InvalidArgument)
		}
	})

	t.Run("unauthenticated user returns Unauthenticated", func(t *testing.T) {
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "", "", status.Error(codes.Unauthenticated, "unauthenticated")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, authMock, &mockXidClient{}, nil)
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.Unauthenticated)
		}
	})

	t.Run("nil dataEngine returns FailedPrecondition", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.FailedPrecondition)
		}
	})

	t.Run("nil repo in server struct returns FailedPrecondition", func(t *testing.T) {
		s := &Server{
			auth:       &mockAuthorizer{},
			xidClient:  &mockXidClient{},
			dataEngine: &mockChartDataEngine{},
		}
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.FailedPrecondition)
		}
	})

	t.Run("nil xidClient in server struct returns FailedPrecondition", func(t *testing.T) {
		s := &Server{
			auth:       &mockAuthorizer{},
			repo:       &mockRepository{},
			dataEngine: &mockChartDataEngine{},
		}
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.FailedPrecondition)
		}
	})

	t.Run("chart not found returns NotFound", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return nil, status.Error(codes.NotFound, "not found")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, &mockChartDataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.NotFound {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.NotFound)
		}
	})

	t.Run("chart repo error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return nil, status.Error(codes.Unknown, "database failure")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, &mockChartDataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.Internal {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.Internal)
		}
	})

	t.Run("chart repo returning nil chart returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return nil, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, &mockChartDataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.Internal {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.Internal)
		}
	})

	t.Run("chart missing valid xid returns FailedPrecondition", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "dashboards/123/charts/456", Xid: 0, Wid: 1}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, &mockChartDataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.FailedPrecondition)
		}
	})

	t.Run("XID service returns RPC error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "dashboards/123/charts/456", Xid: 100, Wid: 1}, nil
			},
		}
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return nil, errors.New("xid service unavailable")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, &mockChartDataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.Internal {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.Internal)
		}
	})

	t.Run("XID service returns Authorized = false returns PermissionDenied", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "dashboards/123/charts/456", Xid: 100, Wid: 1}, nil
			},
		}
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, &mockChartDataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.PermissionDenied)
		}
	})

	t.Run("authorized user successfully gets chart data", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{
					Name: "dashboards/123/charts/456",
					Xid:  100,
					Wid:  1,
				}, nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "user@example.com", "uid-1", nil
			},
		}
		var capturedReq *xidapipb.CheckUserAuthorizationRequest
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				capturedReq = in
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, xidMock, &mockChartDataEngine{})
		resp, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if err != nil {
			t.Fatalf("GetChartData() unexpected error: %v", err)
		}
		if resp.GetName() != "dashboards/123/charts/456" {
			t.Errorf("GetChartData().Name = %q, want %q", resp.GetName(), "dashboards/123/charts/456")
		}
		if capturedReq == nil || capturedReq.GetName() != "xids/100" || capturedReq.GetUser() != "user@example.com" || capturedReq.GetRoleType() != xpb.XidAcls_READER {
			t.Errorf("CheckUserAuthorization called with unexpected request: %v", capturedReq)
		}
	})

	t.Run("context headers forwarded to XID service", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{
					Name: "dashboards/123/charts/456",
					Xid:  100,
					Wid:  1,
				}, nil
			},
		}
		authMock := &mockAuthorizer{}
		var capturedMD metadata.MD
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				capturedMD, _ = metadata.FromOutgoingContext(ctx)
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, xidMock, &mockChartDataEngine{})

		incomingMD := metadata.Pairs(
			"x-goog-iap-jwt-assertion", "jwt-token-val",
			"x-goog-user-email", "user@example.com",
			"authorization", "Bearer token-123",
		)
		callCtx := metadata.NewIncomingContext(context.Background(), incomingMD)

		_, err := s.GetChartData(callCtx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if err != nil {
			t.Fatalf("GetChartData() unexpected error: %v", err)
		}
		if jwts := capturedMD.Get("x-goog-iap-jwt-assertion"); len(jwts) != 1 || jwts[0] != "jwt-token-val" {
			t.Errorf("Expected forwarded x-goog-iap-jwt-assertion = %q, got %v", "jwt-token-val", jwts)
		}
		if emails := capturedMD.Get("x-goog-user-email"); len(emails) != 1 || emails[0] != "user@example.com" {
			t.Errorf("Expected forwarded x-goog-user-email = %q, got %v", "user@example.com", emails)
		}
		if auths := capturedMD.Get("authorization"); len(auths) != 1 || auths[0] != "Bearer token-123" {
			t.Errorf("Expected forwarded authorization = %q, got %v", "Bearer token-123", auths)
		}
	})

	t.Run("authorized user gets root-scoped chart data", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{
					Name: "charts/chart-loss",
					Xid:  1001,
					Wid:  0,
				}, nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "user@example.com", "uid-1", nil
			},
		}
		var capturedReq *xidapipb.CheckUserAuthorizationRequest
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				capturedReq = in
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, xidMock, &mockChartDataEngine{})
		resp, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "charts/chart-loss"})
		if err != nil {
			t.Fatalf("GetChartData() unexpected error: %v", err)
		}
		if resp.GetName() != "charts/chart-loss" {
			t.Errorf("GetChartData().Name = %q, want %q", resp.GetName(), "charts/chart-loss")
		}
		if capturedReq == nil || capturedReq.GetName() != "xids/1001" {
			t.Errorf("CheckUserAuthorization called with unexpected request: %v", capturedReq)
		}
	})

	t.Run("authorized user gets work-unit-scoped chart data", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{
					Name: "experiments/1001/workUnits/2002/charts/chart-loss",
					Xid:  1001,
					Wid:  2002,
				}, nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "user@example.com", "uid-1", nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, &mockChartDataEngine{})
		resp, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "experiments/1001/workUnits/2002/charts/chart-loss"})
		if err != nil {
			t.Fatalf("GetChartData() unexpected error: %v", err)
		}
		if resp.GetName() != "experiments/1001/workUnits/2002/charts/chart-loss" {
			t.Errorf("GetChartData().Name = %q, want %q", resp.GetName(), "experiments/1001/workUnits/2002/charts/chart-loss")
		}
	})

	t.Run("infers xid from work unit chart name when stored chart xid is zero", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{
					Name: "experiments/1001/workUnits/2002/charts/chart-loss",
					Xid:  0,
				}, nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "user@example.com", "uid-1", nil
			},
		}
		var capturedReq *xidapipb.CheckUserAuthorizationRequest
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				capturedReq = in
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, xidMock, &mockChartDataEngine{})
		resp, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "experiments/1001/workUnits/2002/charts/chart-loss"})
		if err != nil {
			t.Fatalf("GetChartData() unexpected error: %v", err)
		}
		if resp.GetName() != "experiments/1001/workUnits/2002/charts/chart-loss" {
			t.Errorf("GetChartData().Name = %q, want %q", resp.GetName(), "experiments/1001/workUnits/2002/charts/chart-loss")
		}
		if capturedReq == nil || capturedReq.GetName() != "xids/1001" {
			t.Errorf("Expected xids/1001, got %v", capturedReq)
		}
	})

	t.Run("returns InvalidArgument when experiment chart without work unit is requested", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, &mockChartDataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "experiments/234911122/charts/baseline"})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.InvalidArgument)
		}
	})

	t.Run("synthesizes fallback chart and returns data for work unit 0 when not in repository", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return nil, status.Error(codes.NotFound, "not found")
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "user@example.com", "uid-1", nil
			},
		}
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, xidMock, &mockChartDataEngine{})
		resp, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "experiments/234911122/workUnits/0/charts/baseline"})
		if err != nil {
			t.Fatalf("GetChartData() unexpected error: %v", err)
		}
		if resp.GetName() != "experiments/234911122/workUnits/0/charts/baseline" {
			t.Errorf("GetChartData().Name = %q, want %q", resp.GetName(), "experiments/234911122/workUnits/0/charts/baseline")
		}
		if resp.GetXid() != 234911122 {
			t.Errorf("GetChartData().Xid = %d, want 234911122", resp.GetXid())
		}
		if resp.GetWid() != 0 {
			t.Errorf("GetChartData().Wid = %d, want 0", resp.GetWid())
		}
	})

	t.Run("returns NotFound when repository returns NotFound and name is not a work unit chart", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return nil, status.Error(codes.NotFound, "not found")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, &mockChartDataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/nonexistent"})
		if status.Code(err) != codes.NotFound {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.NotFound)
		}
	})
}

func TestPropagateAuthContext(t *testing.T) {
	t.Run("no incoming metadata returns original context", func(t *testing.T) {
		ctx := context.Background()
		outCtx := propagateAuthContext(ctx)
		if _, ok := metadata.FromOutgoingContext(outCtx); ok {
			t.Errorf("Expected no outgoing metadata when incoming metadata is missing")
		}
	})

	t.Run("empty header values are omitted", func(t *testing.T) {
		incomingMD := metadata.Pairs(
			"x-goog-iap-jwt-assertion", "",
			"x-goog-user-email", "",
			"authorization", "",
		)
		ctx := metadata.NewIncomingContext(context.Background(), incomingMD)
		outCtx := propagateAuthContext(ctx)
		if _, ok := metadata.FromOutgoingContext(outCtx); ok {
			t.Errorf("Expected no outgoing metadata when all relevant incoming headers are empty")
		}
	})
}

func TestListCharts(t *testing.T) {
	s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)
	ctx := context.Background()

	resp, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "dashboards/123"})
	if err != nil {
		t.Fatalf("ListCharts failed: %v", err)
	}
	if len(resp.GetCharts()) == 0 {
		t.Fatal("ListCharts returned 0 charts, want at least 1")
	}
}

func TestUpdateChart(t *testing.T) {
	s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)
	ctx := context.Background()

	req := &spb.UpdateChartRequest{
		Chart: &dpb.Chart{Name: "dashboards/123/charts/456", Title: "Updated"},
	}
	resp, err := s.UpdateChart(ctx, req)
	if err != nil {
		t.Fatalf("UpdateChart failed: %v", err)
	}
	if resp.GetName() != "dashboards/123/charts/456" {
		t.Errorf("UpdateChart().Name = %q, want %q", resp.GetName(), "dashboards/123/charts/456")
	}
}

func TestDeleteChart(t *testing.T) {
	s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)
	ctx := context.Background()

	_, err := s.DeleteChart(ctx, &spb.DeleteChartRequest{Name: "dashboards/123/charts/456"})
	if err != nil {
		t.Fatalf("DeleteChart failed: %v", err)
	}
}

func TestCheckDashboardAuthorization(t *testing.T) {
	ctx := context.Background()

	t.Run("nil dashboard returns false and nil error", func(t *testing.T) {
		s := &Server{auth: &mockAuthorizer{}}
		authz, err := s.checkDashboardAuthorization(ctx, "user@example.com", nil, auth.Owner)
		if err != nil || authz {
			t.Errorf("checkDashboardAuthorization(nil dash) = (%v, %v), want (false, nil)", authz, err)
		}
	})

	t.Run("nil auth in server returns true", func(t *testing.T) {
		s := &Server{}
		dash := &dpb.Dashboard{Owner: "user@example.com"}
		authz, err := s.checkDashboardAuthorization(ctx, "user@example.com", dash, auth.Owner)
		if err != nil || !authz {
			t.Errorf("checkDashboardAuthorization(nil auth) = (%v, %v), want (true, nil)", authz, err)
		}
	})
}

func TestIsNotFoundError(t *testing.T) {
	if isNotFoundError(nil) {
		t.Errorf("isNotFoundError(nil) = true, want false")
	}
	if !isNotFoundError(status.Error(codes.NotFound, "not found")) {
		t.Errorf("isNotFoundError(status.NotFound) = false, want true")
	}
}

func TestIsAlreadyExistsError(t *testing.T) {
	if isAlreadyExistsError(nil) {
		t.Errorf("isAlreadyExistsError(nil) = true, want false")
	}
	if !isAlreadyExistsError(status.Error(codes.AlreadyExists, "already exists")) {
		t.Errorf("isAlreadyExistsError(status.AlreadyExists) = false, want true")
	}
}

func TestAuthorizeExperiment(t *testing.T) {
	ctx := context.Background()

	t.Run("authorizeExperimentWrite success", func(t *testing.T) {
		var capturedReq *xidapipb.CheckUserAuthorizationRequest
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				capturedReq = in
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := &Server{xidClient: xidMock}
		err := s.authorizeExperimentWrite(ctx, "writer@example.com", 123)
		if err != nil {
			t.Fatalf("authorizeExperimentWrite returned unexpected error: %v", err)
		}
		if capturedReq == nil || capturedReq.GetName() != "xids/123" || capturedReq.GetUser() != "writer@example.com" || capturedReq.GetRoleType() != xpb.XidAcls_WRITER {
			t.Errorf("authorizeExperimentWrite called with unexpected request: %v", capturedReq)
		}
	})

	t.Run("authorizeExperimentWrite unauthorized returns PermissionDenied", func(t *testing.T) {
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(false)}, nil
			},
		}
		s := &Server{xidClient: xidMock}
		err := s.authorizeExperimentWrite(ctx, "user@example.com", 123)
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("authorizeExperimentWrite code = %v, want %v", status.Code(err), codes.PermissionDenied)
		}
	})

	t.Run("authorizeExperimentWrite RPC error returns Internal", func(t *testing.T) {
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return nil, errors.New("rpc unavailable")
			},
		}
		s := &Server{xidClient: xidMock}
		err := s.authorizeExperimentWrite(ctx, "user@example.com", 123)
		if status.Code(err) != codes.Internal {
			t.Errorf("authorizeExperimentWrite code = %v, want %v", status.Code(err), codes.Internal)
		}
	})
}
