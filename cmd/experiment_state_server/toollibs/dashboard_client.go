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

	"google.golang.org/grpc"

	pb "xmanager_cloud/api/protos/dashboard"
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

func (c *DashboardToolClient) validateRequest(reqNil bool) error {
	if c == nil || c.client == nil {
		return errors.New("client is not initialized")
	}
	if reqNil {
		return errors.New("request cannot be nil")
	}
	return nil
}

// CreateChart issues a CreateChart RPC to the xmc-dashboard-service.
func (c *DashboardToolClient) CreateChart(ctx context.Context, req *pb.CreateChartRequest, opts ...grpc.CallOption) (*pb.Chart, error) {
	if err := c.validateRequest(req == nil); err != nil {
		return nil, err
	}
	return c.client.CreateChart(ctx, req, opts...)
}

// GetChart issues a GetChart RPC to the xmc-dashboard-service.
func (c *DashboardToolClient) GetChart(ctx context.Context, req *pb.GetChartRequest, opts ...grpc.CallOption) (*pb.Chart, error) {
	if err := c.validateRequest(req == nil); err != nil {
		return nil, err
	}
	return c.client.GetChart(ctx, req, opts...)
}
