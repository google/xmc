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
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/dashboard/dataengine"
	xpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

type (
	createDashboardFunc = func(ctx context.Context, dashboard *dpb.Dashboard) error
	getDashboardFunc    = func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error)
	listDashboardsFunc  = func(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error)
	updateDashboardFunc = func(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error)
	deleteDashboardFunc = func(ctx context.Context, dashboardId string) error
	getChartFunc        = func(ctx context.Context, chartId string) (*dpb.Chart, error)
)

type mockRepository struct {
	createDashboardFn createDashboardFunc
	getDashboardFn    getDashboardFunc
	listDashboardsFn  listDashboardsFunc
	updateDashboardFn updateDashboardFunc
	deleteDashboardFn deleteDashboardFunc
	getChartFn        getChartFunc
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

func TestCreateChart(t *testing.T) {
	s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, nil)
	ctx := context.Background()

	req := &spb.CreateChartRequest{
		Parent: "dashboards/123",
		Chart:  &dpb.Chart{Name: "dashboards/123/charts/new", Title: "New Chart"},
	}
	resp, err := s.CreateChart(ctx, req)
	if err != nil {
		t.Fatalf("CreateChart failed: %v", err)
	}
	if resp.GetName() != "dashboards/123/charts/new" {
		t.Errorf("CreateChart().Name = %q, want %q", resp.GetName(), "dashboards/123/charts/new")
	}
}

func TestGetChart(t *testing.T) {
	s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, nil)
	ctx := context.Background()

	req := &spb.GetChartRequest{Name: "dashboards/123/charts/456"}
	resp, err := s.GetChart(ctx, req)
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}
	if resp.GetName() != "dashboards/123/charts/456" {
		t.Errorf("GetChart().Name = %q, want %q", resp.GetName(), "dashboards/123/charts/456")
	}
}

func TestNewServer(t *testing.T) {
	t.Run("panics on nil repo", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("expected NewServer to panic on nil repo, but it did not")
			}
		}()
		NewServer(&Config{}, nil, &mockAuthorizer{}, nil)
	})

	t.Run("panics on nil auth", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("expected NewServer to panic on nil auth, but it did not")
			}
		}()
		NewServer(&Config{}, &mockRepository{}, nil, nil)
	})

	t.Run("successful server initialization", func(t *testing.T) {
		s := NewServer(&Config{Port: "8080"}, &mockRepository{}, &mockAuthorizer{}, nil)
		if s == nil {
			t.Fatal("expected non-nil server")
		}
	})
}

func TestGetChartData(t *testing.T) {
	ctx := context.Background()

	t.Run("nil request returns InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, nil)
		_, err := s.GetChartData(ctx, nil)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("GetChartData(nil) code = %v, want %v", status.Code(err), codes.InvalidArgument)
		}
	})

	t.Run("unauthenticated user returns Unauthenticated", func(t *testing.T) {
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "", "", status.Error(codes.Unauthenticated, "unauthenticated")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, authMock, nil)
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.Unauthenticated)
		}
	})

	t.Run("nil dataEngine returns FailedPrecondition", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, nil)
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.FailedPrecondition)
		}
	})

	t.Run("nil repo in server struct returns FailedPrecondition", func(t *testing.T) {
		s := &Server{
			auth:       &mockAuthorizer{},
			dataEngine: &dataengine.DataEngine{},
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
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &dataengine.DataEngine{})
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
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &dataengine.DataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.Internal {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.Internal)
		}
	})

	t.Run("invalid chart name format returns InvalidArgument", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "invalid-name-format"}, nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &dataengine.DataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "invalid-name-format"})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.InvalidArgument)
		}
	})

	t.Run("parent dashboard not found returns NotFound", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "dashboards/123/charts/456"}, nil
			},
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return nil, status.Error(codes.NotFound, "dashboard not found")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &dataengine.DataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.NotFound {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.NotFound)
		}
	})

	t.Run("parent dashboard repo error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "dashboards/123/charts/456"}, nil
			},
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return nil, status.Error(codes.Internal, "storage error")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &dataengine.DataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.Internal {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.Internal)
		}
	})

	t.Run("dashboard authorization error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "dashboards/123/charts/456"}, nil
			},
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{Name: "dashboards/123", Owner: "owner@example.com"}, nil
			},
		}
		authMock := &mockAuthorizer{
			isUserAuthorizedFn: func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
				return false, status.Error(codes.Internal, "auth check failed")
			},
		}
		s := NewServer(&Config{}, repo, authMock, &dataengine.DataEngine{})
		_, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if status.Code(err) != codes.Internal {
			t.Errorf("GetChartData() code = %v, want %v", status.Code(err), codes.Internal)
		}
	})

	t.Run("unauthorized user returns PermissionDenied", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return &dpb.Chart{Name: "dashboards/123/charts/456"}, nil
			},
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{Name: "dashboards/123", Owner: "owner@example.com"}, nil
			},
		}
		authMock := &mockAuthorizer{
			isUserAuthorizedFn: func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
				return false, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, &dataengine.DataEngine{})
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
					Xid:  1,
					Wid:  1,
				}, nil
			},
			getDashboardFn: func(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
				return &dpb.Dashboard{Name: "dashboards/123", Owner: "user@example.com"}, nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "user@example.com", "uid-1", nil
			},
			isUserAuthorizedFn: func(ctx context.Context, user string, acls *xpb.XidAcls, role auth.Role) (bool, error) {
				return true, nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, &dataengine.DataEngine{})
		resp, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
		if err != nil {
			t.Fatalf("GetChartData() unexpected error: %v", err)
		}
		if resp.GetName() != "dashboards/123/charts/456" {
			t.Errorf("GetChartData().Name = %q, want %q", resp.GetName(), "dashboards/123/charts/456")
		}
	})
}

func TestListCharts(t *testing.T) {
	s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, nil)
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
	s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, nil)
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
	s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, nil)
	ctx := context.Background()

	_, err := s.DeleteChart(ctx, &spb.DeleteChartRequest{Name: "dashboards/123/charts/456"})
	if err != nil {
		t.Fatalf("DeleteChart failed: %v", err)
	}
}
