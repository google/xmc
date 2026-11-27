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
	mu                            sync.Mutex
	createChartResp, getChartResp *dashboard_pb.Chart
	createChartErr, getChartErr   error
	createChartFn                 func(ctx context.Context, in *dashboard_pb.CreateChartRequest) (*dashboard_pb.Chart, error)
	reqs                          []*dashboard_pb.CreateChartRequest
	outgoingMD, getChartMD        []metadata.MD // records forwarded credential metadata
	ctxErrs, getChartCtxErrs      []error
	getChartReqs                  []string
}

func appendMetadataCall(ctx context.Context, mds *[]metadata.MD, errs *[]error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	*mds = append(*mds, md)
	*errs = append(*errs, ctx.Err())
}

func (m *mockDashboardClient) GetChart(ctx context.Context, in *dashboard_pb.GetChartRequest, _ ...grpc.CallOption) (*dashboard_pb.Chart, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getChartReqs = append(m.getChartReqs, in.GetName())
	appendMetadataCall(ctx, &m.getChartMD, &m.getChartCtxErrs)
	return m.getChartResp, m.getChartErr
}

func (m *mockDashboardClient) CreateChart(ctx context.Context, in *dashboard_pb.CreateChartRequest, _ ...grpc.CallOption) (*dashboard_pb.Chart, error) {
	m.mu.Lock()
	fn, err, resp := m.createChartFn, m.createChartErr, m.createChartResp
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.reqs = append(m.reqs, in)
		appendMetadataCall(ctx, &m.outgoingMD, &m.ctxErrs)
		m.mu.Unlock()
	}()
	if fn != nil {
		return fn(ctx, in)
	}
	return resp, err
}
