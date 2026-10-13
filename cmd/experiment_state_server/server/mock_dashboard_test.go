// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	dashboard_pb "xmanager_cloud/api/protos/dashboard"
)

type mockDashboardClient struct {
	dashboard_pb.DashboardServiceClient
	createChartResp *dashboard_pb.Chart
	createChartErr  error
	reqs            []*dashboard_pb.CreateChartRequest
	// outgoingMD records the metadata each call carried, so tests can assert that the caller's
	// credentials were forwarded.
	outgoingMD []metadata.MD
}

func (m *mockDashboardClient) CreateChart(ctx context.Context, in *dashboard_pb.CreateChartRequest, opts ...grpc.CallOption) (*dashboard_pb.Chart, error) {
	m.reqs = append(m.reqs, in)
	md, _ := metadata.FromOutgoingContext(ctx)
	m.outgoingMD = append(m.outgoingMD, md)
	if m.createChartErr != nil {
		return nil, m.createChartErr
	}
	return m.createChartResp, nil
}
