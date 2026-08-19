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
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

func TestCreateChart(t *testing.T) {
	ctx := context.Background()

	t.Run("successful creation with auto-generated UUID under work unit parent", func(t *testing.T) {
		var savedChart *dpb.Chart
		repo := &mockRepository{
			createChartFn: func(ctx context.Context, chart *dpb.Chart) error {
				savedChart = chart
				return nil
			},
		}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "author@example.com", "user-1", nil
			},
		}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &spb.CreateChartRequest{
			Parent: "experiments/100/workUnits/2",
			Chart: &dpb.Chart{
				Title:       "Loss Curve",
				Description: "Training loss over time",
			},
		}

		resp, err := s.CreateChart(ctx, req)
		if err != nil {
			t.Fatalf("CreateChart failed: %v", err)
		}
		const prefix = "experiments/100/workUnits/2/charts/"
		if !strings.HasPrefix(resp.GetName(), prefix) {
			t.Errorf("Expected name with prefix %q, got %q", prefix, resp.GetName())
		}
		extractedUUID := strings.TrimPrefix(resp.GetName(), prefix)
		parsedUUID, err := uuid.Parse(extractedUUID)
		if err != nil {
			t.Errorf("Expected valid UUID in chart name, got %q (err: %v)", extractedUUID, err)
		} else if parsedUUID.Version() != 7 {
			t.Errorf("Expected UUID v7, got version %d", parsedUUID.Version())
		}
		if resp.GetXid() != 100 {
			t.Errorf("Expected Xid 100, got %d", resp.GetXid())
		}
		if resp.GetWid() != 2 {
			t.Errorf("Expected Wid 2, got %d", resp.GetWid())
		}
		if savedChart == nil || savedChart.GetName() != resp.GetName() {
			t.Errorf("Expected saved chart name %q, got %v", resp.GetName(), savedChart)
		}
	})

	t.Run("successful creation with explicit chart_id under work unit parent", func(t *testing.T) {
		repo := &mockRepository{}
		authMock := &mockAuthorizer{}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &spb.CreateChartRequest{
			Parent:  "experiments/100/workUnits/2",
			ChartId: "custom-chart-1",
			Chart: &dpb.Chart{
				Title: "Accuracy Chart",
			},
		}

		resp, err := s.CreateChart(ctx, req)
		if err != nil {
			t.Fatalf("CreateChart failed: %v", err)
		}
		expectedName := "experiments/100/workUnits/2/charts/custom-chart-1"
		if resp.GetName() != expectedName {
			t.Errorf("Expected name %q, got %q", expectedName, resp.GetName())
		}
		if resp.GetXid() != 100 || resp.GetWid() != 2 {
			t.Errorf("Expected Xid=100 Wid=2, got Xid=%d Wid=%d", resp.GetXid(), resp.GetWid())
		}
	})

	t.Run("unauthenticated caller returns Unauthenticated", func(t *testing.T) {
		repo := &mockRepository{}
		authMock := &mockAuthorizer{
			authenticateFn: func(ctx context.Context) (string, string, error) {
				return "", "", errors.New("missing token")
			},
		}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &spb.CreateChartRequest{
			Parent: "experiments/100/workUnits/1",
			Chart:  &dpb.Chart{Title: "Title"},
		}

		_, err := s.CreateChart(ctx, req)
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("Expected code Unauthenticated, got %v", status.Code(err))
		}
	})

	t.Run("invalid request returns InvalidArgument", func(t *testing.T) {
		repo := &mockRepository{}
		authMock := &mockAuthorizer{}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		tests := []struct {
			name string
			req  *spb.CreateChartRequest
		}{
			{name: "nil request", req: nil},
			{name: "nil chart", req: &spb.CreateChartRequest{Parent: "experiments/100/workUnits/1"}},
			{name: "empty title", req: &spb.CreateChartRequest{
				Parent: "experiments/100/workUnits/1",
				Chart:  &dpb.Chart{Title: "   "},
			}},
			{name: "invalid chart ID with slash", req: &spb.CreateChartRequest{
				Parent:  "experiments/100/workUnits/1",
				ChartId: "invalid/id",
				Chart:   &dpb.Chart{Title: "Title"},
			}},
			{name: "empty parent", req: &spb.CreateChartRequest{
				Parent: "",
				Chart:  &dpb.Chart{Title: "Title"},
			}},
			{name: "invalid parent zero wid", req: &spb.CreateChartRequest{
				Parent: "experiments/100/workUnits/0",
				Chart:  &dpb.Chart{Title: "Title"},
			}},
			{name: "invalid parent", req: &spb.CreateChartRequest{
				Parent: "invalid-parent",
				Chart:  &dpb.Chart{Title: "Title"},
			}},
			{name: "dashboard parent rejected", req: &spb.CreateChartRequest{
				Parent: "dashboards/d1",
				Chart:  &dpb.Chart{Title: "Title"},
			}},
			{name: "invalid chart name in request", req: &spb.CreateChartRequest{
				Parent: "experiments/100/workUnits/1",
				Chart:  &dpb.Chart{Name: "invalid-name", Title: "Title"},
			}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, err := s.CreateChart(ctx, tc.req)
				if status.Code(err) != codes.InvalidArgument {
					t.Errorf("Expected code InvalidArgument, got %v", status.Code(err))
				}
			})
		}
	})

	t.Run("repo error returns Internal", func(t *testing.T) {
		repo := &mockRepository{
			createChartFn: func(ctx context.Context, chart *dpb.Chart) error {
				return errors.New("spanner write failed")
			},
		}
		authMock := &mockAuthorizer{}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &spb.CreateChartRequest{
			Parent: "experiments/100/workUnits/1",
			Chart:  &dpb.Chart{Title: "Title"},
		}

		_, err := s.CreateChart(ctx, req)
		if status.Code(err) != codes.Internal {
			t.Errorf("Expected code Internal, got %v", status.Code(err))
		}
	})

	t.Run("repo AlreadyExists error returns AlreadyExists", func(t *testing.T) {
		repo := &mockRepository{
			createChartFn: func(ctx context.Context, chart *dpb.Chart) error {
				return status.Error(codes.AlreadyExists, "chart already exists")
			},
		}
		authMock := &mockAuthorizer{}
		s := NewServer(&Config{}, repo, authMock, &mockXidClient{}, nil)

		req := &spb.CreateChartRequest{
			Parent:  "experiments/100/workUnits/1",
			ChartId: "dup-id",
			Chart:   &dpb.Chart{Title: "Title"},
		}

		_, err := s.CreateChart(ctx, req)
		if status.Code(err) != codes.AlreadyExists {
			t.Errorf("Expected code AlreadyExists, got %v", status.Code(err))
		}
	})
}
