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

	"google.golang.org/grpc"

	apipb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	pb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

// DashboardToolClient is a wrapper for communicating with xmc-dashboard-service.
type DashboardToolClient struct {
	client pb.DashboardServiceClient
}

// NewDashboardToolClient creates a new DashboardToolClient.
func NewDashboardToolClient(client pb.DashboardServiceClient) *DashboardToolClient {
	return &DashboardToolClient{
		client: client,
	}
}

// CreateChart issues a CreateChart RPC to the xmc-dashboard-service.
func (c *DashboardToolClient) CreateChart(ctx context.Context, req *pb.CreateChartRequest, opts ...grpc.CallOption) (*apipb.Chart, error) {
	return c.client.CreateChart(ctx, req, opts...)
}
