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
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	dpb "xmanager_cloud/api/protos/dashboard"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
)

// chartWithXid builds a minimal chart carrying only the fields the ACL filter reads.
func chartWithXid(name string, xid int64) *dpb.Chart {
	return &dpb.Chart{Name: name, Xid: xid, Wid: 1}
}

// chartNames extracts the resource names of charts for order-sensitive assertions.
func chartNames(charts []*dpb.Chart) []string {
	names := make([]string, 0, len(charts))
	for _, chart := range charts {
		names = append(names, chart.GetName())
	}
	return names
}

func TestFilterAuthorizedCharts(t *testing.T) {
	ctx := context.Background()

	t.Run("drops charts from experiments the user cannot read", func(t *testing.T) {
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(in.GetName() == "xids/11")}, nil
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, xidMock, nil)

		charts := []*dpb.Chart{
			chartWithXid("experiments/11/workUnits/1/charts/a", 11),
			chartWithXid("experiments/22/workUnits/1/charts/b", 22),
			chartWithXid("experiments/11/workUnits/1/charts/c", 11),
		}
		got, err := s.filterAuthorizedCharts(ctx, "user@example.com", charts)
		if err != nil {
			t.Fatalf("filterAuthorizedCharts failed: %v", err)
		}

		want := []string{"experiments/11/workUnits/1/charts/a", "experiments/11/workUnits/1/charts/c"}
		gotNames := chartNames(got)
		if len(gotNames) != len(want) {
			t.Fatalf("filterAuthorizedCharts returned %v, want %v", gotNames, want)
		}
		for i, name := range want {
			if gotNames[i] != name {
				t.Errorf("filterAuthorizedCharts[%d] = %q, want %q", i, gotNames[i], name)
			}
		}
	})

	t.Run("checks each distinct experiment only once", func(t *testing.T) {
		var checked []string
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				checked = append(checked, in.GetName())
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, xidMock, nil)

		charts := []*dpb.Chart{
			chartWithXid("charts/a", 7),
			chartWithXid("charts/b", 7),
			chartWithXid("charts/c", 8),
			chartWithXid("charts/d", 7),
		}
		got, err := s.filterAuthorizedCharts(ctx, "user@example.com", charts)
		if err != nil {
			t.Fatalf("filterAuthorizedCharts failed: %v", err)
		}
		if len(got) != 4 {
			t.Errorf("filterAuthorizedCharts returned %d charts, want 4", len(got))
		}
		if len(checked) != 2 {
			t.Errorf("XID service called %d times (%v), want 2 (one per distinct xid)", len(checked), checked)
		}
	})

	t.Run("XID service failure is propagated, not treated as a denial", func(t *testing.T) {
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return nil, errors.New("xid service unreachable")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, xidMock, nil)

		_, err := s.filterAuthorizedCharts(ctx, "user@example.com", []*dpb.Chart{chartWithXid("charts/a", 5)})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("drops charts without a valid experiment XID", func(t *testing.T) {
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, &mockXidClient{}, nil)

		got, err := s.filterAuthorizedCharts(ctx, "user@example.com", []*dpb.Chart{
			chartWithXid("charts/no-xid", 0),
			chartWithXid("charts/negative", -1),
			chartWithXid("charts/valid", 3),
		})
		if err != nil {
			t.Fatalf("filterAuthorizedCharts failed: %v", err)
		}
		if len(got) != 1 || got[0].GetName() != "charts/valid" {
			t.Errorf("filterAuthorizedCharts returned %v, want only charts/valid", chartNames(got))
		}
	})

	t.Run("empty input returns a non-nil slice without consulting the XID service", func(t *testing.T) {
		called := false
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				called = true
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, xidMock, nil)

		got, err := s.filterAuthorizedCharts(ctx, "user@example.com", nil)
		if err != nil {
			t.Fatalf("filterAuthorizedCharts failed: %v", err)
		}
		if got == nil {
			t.Errorf("filterAuthorizedCharts returned a nil slice, want empty non-nil")
		}
		if called {
			t.Errorf("XID service consulted for an empty chart list")
		}
	})

	t.Run("nil XID client returns FailedPrecondition", func(t *testing.T) {
		s := &Server{auth: &mockAuthorizer{}, repo: &mockRepository{}}

		_, err := s.filterAuthorizedCharts(ctx, "user@example.com", []*dpb.Chart{chartWithXid("charts/a", 5)})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("Expected code FailedPrecondition, got %v", status.Code(err))
		}
	})
}

func TestAuthorizeChartReferences(t *testing.T) {
	ctx := context.Background()

	// chartsByID answers GetChart from a fixed table, so a reference to an unknown ID reads as a
	// missing chart rather than a repository failure.
	chartsByID := func(charts map[string]*dpb.Chart) *mockRepository {
		return &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				chart, ok := charts[chartId]
				if !ok {
					return nil, status.Errorf(codes.NotFound, "chart %q not found", chartId)
				}
				return chart, nil
			},
		}
	}

	t.Run("passes when the user can read every referenced experiment", func(t *testing.T) {
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		repo := chartsByID(map[string]*dpb.Chart{
			"c1": chartWithXid("charts/c1", 11),
			"c2": chartWithXid("charts/c2", 22),
		})
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, nil)

		if err := s.authorizeChartReferences(ctx, "user@example.com", []string{"c1", "c2"}); err != nil {
			t.Errorf("authorizeChartReferences failed: %v", err)
		}
	})

	t.Run("a chart from an unreadable experiment is denied", func(t *testing.T) {
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(in.GetName() == "xids/11")}, nil
			},
		}
		repo := chartsByID(map[string]*dpb.Chart{
			"mine":     chartWithXid("charts/mine", 11),
			"somebody": chartWithXid("charts/somebody", 22),
		})
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, nil)

		err := s.authorizeChartReferences(ctx, "user@example.com", []string{"mine", "somebody"})
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("Expected code PermissionDenied, got %v", status.Code(err))
		}
	})

	t.Run("an ID that names no chart is rejected as InvalidArgument", func(t *testing.T) {
		s := NewServer(&Config{}, chartsByID(nil), &mockAuthorizer{}, &mockXidClient{}, nil)

		err := s.authorizeChartReferences(ctx, "user@example.com", []string{"ghost"})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
		}
	})

	t.Run("a chart without a valid experiment XID is rejected", func(t *testing.T) {
		repo := chartsByID(map[string]*dpb.Chart{"orphan": chartWithXid("charts/orphan", 0)})
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		err := s.authorizeChartReferences(ctx, "user@example.com", []string{"orphan"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("Expected code FailedPrecondition, got %v", status.Code(err))
		}
	})

	t.Run("a repository failure is not mistaken for a missing chart", func(t *testing.T) {
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				return nil, errors.New("spanner unavailable")
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		err := s.authorizeChartReferences(ctx, "user@example.com", []string{"c1"})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("checks each distinct experiment only once", func(t *testing.T) {
		var checked []string
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				checked = append(checked, in.GetName())
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		repo := chartsByID(map[string]*dpb.Chart{
			"a": chartWithXid("charts/a", 7),
			"b": chartWithXid("charts/b", 7),
			"c": chartWithXid("charts/c", 8),
		})
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, xidMock, nil)

		if err := s.authorizeChartReferences(ctx, "user@example.com", []string{"a", "b", "c"}); err != nil {
			t.Fatalf("authorizeChartReferences failed: %v", err)
		}
		if len(checked) != 2 {
			t.Errorf("XID service called %d times (%v), want 2 (one per distinct xid)", len(checked), checked)
		}
	})

	t.Run("empty input passes without touching the repository", func(t *testing.T) {
		called := false
		repo := &mockRepository{
			getChartFn: func(ctx context.Context, chartId string) (*dpb.Chart, error) {
				called = true
				return chartWithXid("charts/a", 1), nil
			},
		}
		s := NewServer(&Config{}, repo, &mockAuthorizer{}, &mockXidClient{}, nil)

		if err := s.authorizeChartReferences(ctx, "user@example.com", nil); err != nil {
			t.Errorf("authorizeChartReferences failed: %v", err)
		}
		if called {
			t.Errorf("repository consulted for an empty chart ID list")
		}
	})

	t.Run("nil XID client returns FailedPrecondition", func(t *testing.T) {
		s := &Server{auth: &mockAuthorizer{}, repo: &mockRepository{}}

		err := s.authorizeChartReferences(ctx, "user@example.com", []string{"c1"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("Expected code FailedPrecondition, got %v", status.Code(err))
		}
	})
}

func TestAuthorizeChartExperimentWrites(t *testing.T) {
	ctx := context.Background()

	t.Run("passes when the user can write every experiment", func(t *testing.T) {
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, xidMock, nil)

		charts := []*dpb.Chart{chartWithXid("", 11), chartWithXid("", 22)}
		if err := s.authorizeChartExperimentWrites(ctx, "user@example.com", charts); err != nil {
			t.Errorf("authorizeChartExperimentWrites failed: %v", err)
		}
	})

	t.Run("a single denied experiment aborts the whole request", func(t *testing.T) {
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(in.GetName() == "xids/11")}, nil
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, xidMock, nil)

		charts := []*dpb.Chart{chartWithXid("", 11), chartWithXid("", 22)}
		err := s.authorizeChartExperimentWrites(ctx, "user@example.com", charts)
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("Expected code PermissionDenied, got %v", status.Code(err))
		}
	})

	t.Run("checks each distinct experiment only once", func(t *testing.T) {
		var checked []string
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				checked = append(checked, in.GetName())
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, xidMock, nil)

		charts := []*dpb.Chart{chartWithXid("", 7), chartWithXid("", 7), chartWithXid("", 8), chartWithXid("", 7)}
		if err := s.authorizeChartExperimentWrites(ctx, "user@example.com", charts); err != nil {
			t.Fatalf("authorizeChartExperimentWrites failed: %v", err)
		}
		if len(checked) != 2 {
			t.Errorf("XID service called %d times (%v), want 2 (one per distinct xid)", len(checked), checked)
		}
	})

	t.Run("XID service failure is propagated", func(t *testing.T) {
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				return nil, errors.New("xid service unreachable")
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, xidMock, nil)

		err := s.authorizeChartExperimentWrites(ctx, "user@example.com", []*dpb.Chart{chartWithXid("", 5)})
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("empty input passes without consulting the XID service", func(t *testing.T) {
		called := false
		xidMock := &mockXidClient{
			checkUserAuthorizationFn: func(ctx context.Context, in *xidapipb.CheckUserAuthorizationRequest, opts ...grpc.CallOption) (*xidapipb.CheckUserAuthorizationResponse, error) {
				called = true
				return &xidapipb.CheckUserAuthorizationResponse{Authorized: proto.Bool(true)}, nil
			},
		}
		s := NewServer(&Config{}, &mockRepository{}, &mockAuthorizer{}, xidMock, nil)

		if err := s.authorizeChartExperimentWrites(ctx, "user@example.com", nil); err != nil {
			t.Errorf("authorizeChartExperimentWrites failed: %v", err)
		}
		if called {
			t.Errorf("XID service consulted for an empty chart list")
		}
	})

	t.Run("nil XID client returns FailedPrecondition", func(t *testing.T) {
		s := &Server{auth: &mockAuthorizer{}, repo: &mockRepository{}}

		err := s.authorizeChartExperimentWrites(ctx, "user@example.com", []*dpb.Chart{chartWithXid("", 5)})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("Expected code FailedPrecondition, got %v", status.Code(err))
		}
	})
}
