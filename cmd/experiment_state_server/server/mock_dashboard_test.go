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
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	dashboard_pb "xmanager_cloud/api/protos/dashboard"
)

type mockDashboardClient struct {
	dashboard_pb.DashboardServiceClient
	mu              sync.Mutex
	createChartResp *dashboard_pb.Chart
	createChartErr  error
	createChartFn   func(ctx context.Context, in *dashboard_pb.CreateChartRequest) (*dashboard_pb.Chart, error)
	reqs            []*dashboard_pb.CreateChartRequest
	// outgoingMD records the metadata each call carried, so tests can assert that the caller's
	// credentials were forwarded.
	outgoingMD []metadata.MD
	ctxErrs    []error
	// getChartResp/getChartErr answer GetChart; getChartReqs records the names it was asked for.
	getChartResp *dashboard_pb.Chart
	getChartErr  error
	getChartReqs []string
}

func (m *mockDashboardClient) GetChart(ctx context.Context, in *dashboard_pb.GetChartRequest, _ ...grpc.CallOption) (*dashboard_pb.Chart, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getChartReqs = append(m.getChartReqs, in.GetName())
	return m.getChartResp, m.getChartErr
}

func (m *mockDashboardClient) CreateChart(ctx context.Context, in *dashboard_pb.CreateChartRequest, opts ...grpc.CallOption) (*dashboard_pb.Chart, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	m.mu.Lock()
	fn, err, resp := m.createChartFn, m.createChartErr, m.createChartResp
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.reqs, m.outgoingMD, m.ctxErrs = append(m.reqs, in), append(m.outgoingMD, md), append(m.ctxErrs, ctx.Err())
		m.mu.Unlock()
	}()
	if fn != nil {
		return fn(ctx, in)
	}
	return resp, err
}
