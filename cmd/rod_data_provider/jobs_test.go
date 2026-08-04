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

package main

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/rod"
)

type mockWorkUnitFetcher struct {
	jobs          []*spb.JobInfo
	nextPageToken string
	err           error
}

func (m *mockWorkUnitFetcher) FetchActiveWorkUnitsByQueue(ctx context.Context, queueName string, pageSize int32, pageToken string) ([]*spb.JobInfo, string, error) {
	if m.err != nil {
		return nil, "", m.err
	}
	return m.jobs, m.nextPageToken, nil
}

func TestGetRunningAndPendingJobs(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051"}

	tests := []struct {
		name      string
		req       *spb.GetRunningAndPendingJobsRequest
		fetcher   WorkUnitFetcher
		wantCode  codes.Code
		wantCount int
	}{
		{
			name: "Success With Fetcher",
			req: &spb.GetRunningAndPendingJobsRequest{
				QueueName: "gpu-cluster-queue",
			},
			fetcher: &mockWorkUnitFetcher{
				jobs: []*spb.JobInfo{
					{
						Position:   "1",
						JobName:    "xmc-operation-100-1",
						QueueName:  "gpu-cluster-queue",
						Status:     "SCHEDULING",
						WorkUnitId: "1",
						Xid:        "100",
					},
				},
			},
			wantCode:  codes.OK,
			wantCount: 1,
		},
		{
			name: "Fallback Mock With Nil Fetcher",
			req: &spb.GetRunningAndPendingJobsRequest{
				QueueName: "gpu-cluster-queue",
			},
			fetcher:   nil,
			wantCode:  codes.OK,
			wantCount: 2,
		},
		{
			name: "Missing Queue Name",
			req: &spb.GetRunningAndPendingJobsRequest{
				QueueName: "",
			},
			fetcher:   nil,
			wantCode:  codes.InvalidArgument,
			wantCount: 0,
		},
		{
			name: "Fetcher Error",
			req: &spb.GetRunningAndPendingJobsRequest{
				QueueName: "gpu-cluster-queue",
			},
			fetcher: &mockWorkUnitFetcher{
				err: fmt.Errorf("spanner connection failure"),
			},
			wantCode:  codes.Internal,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServerWithFetcher(cfg, tt.fetcher)
			resp, err := srv.GetRunningAndPendingJobs(context.Background(), tt.req)

			gotCode := status.Code(err)
			if gotCode != tt.wantCode {
				t.Fatalf("GetRunningAndPendingJobs code = %v, want %v (err: %v)", gotCode, tt.wantCode, err)
			}

			if tt.wantCode == codes.OK {
				if resp == nil {
					t.Fatalf("Expected non-nil response")
				}
				if len(resp.Jobs) != tt.wantCount {
					t.Errorf("Jobs count = %d, want %d", len(resp.Jobs), tt.wantCount)
				}
			}
		})
	}
}
