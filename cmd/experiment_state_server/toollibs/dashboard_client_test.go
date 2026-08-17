// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package toollibs

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"

	apipb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	pb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

var _ pb.DashboardServiceClient = (*mockDashboardServiceClient)(nil)

type mockDashboardServiceClient struct {
	pb.DashboardServiceClient
	createChartFunc func(ctx context.Context, in *pb.CreateChartRequest, opts ...grpc.CallOption) (*apipb.Chart, error)
}

func (m *mockDashboardServiceClient) CreateChart(ctx context.Context, in *pb.CreateChartRequest, opts ...grpc.CallOption) (*apipb.Chart, error) {
	if m.createChartFunc != nil {
		return m.createChartFunc(ctx, in, opts...)
	}
	return nil, nil
}

func TestDashboardToolClient_CreateChart(t *testing.T) {
	expectedChart := &apipb.Chart{Name: "test-chart"}

	tests := []struct {
		name      string
		req       *pb.CreateChartRequest
		setupMock func(t *testing.T) *mockDashboardServiceClient
		wantErr   bool
	}{
		{
			name: "success",
			req:  &pb.CreateChartRequest{Parent: "dashboards/123"},
			setupMock: func(t *testing.T) *mockDashboardServiceClient {
				return &mockDashboardServiceClient{
					createChartFunc: func(ctx context.Context, in *pb.CreateChartRequest, opts ...grpc.CallOption) (*apipb.Chart, error) {
						if in.Parent != "dashboards/123" {
							t.Errorf("expected parent dashboards/123, got %s", in.Parent)
						}
						return expectedChart, nil
					},
				}
			},
			wantErr: false,
		},
		{
			name: "rpc_error",
			req:  &pb.CreateChartRequest{Parent: "dashboards/123"},
			setupMock: func(t *testing.T) *mockDashboardServiceClient {
				return &mockDashboardServiceClient{
					createChartFunc: func(ctx context.Context, in *pb.CreateChartRequest, opts ...grpc.CallOption) (*apipb.Chart, error) {
						return nil, errors.New("rpc failed")
					},
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := tt.setupMock(t)
			client := NewDashboardToolClient(mockClient)

			resp, err := client.CreateChart(context.Background(), tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
			if err != nil {
				return
			}

			if resp != expectedChart {
				t.Errorf("expected chart %v, got %v", expectedChart, resp)
			}
		})
	}
}
