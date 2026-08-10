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
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/rod"
)

func TestGetRunningAndPendingJobs(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051"}

	tests := []struct {
		name      string
		ctx       context.Context
		req       *spb.GetRunningAndPendingJobsRequest
		wantCode  codes.Code
		wantCount int
	}{
		{
			name: "Success Returns Mock Jobs",
			ctx:  context.Background(),
			req: &spb.GetRunningAndPendingJobsRequest{
				QueueName: "gpu-cluster-queue",
			},
			wantCode:  codes.OK,
			wantCount: 2,
		},
		{
			name: "Missing Queue Name",
			ctx:  context.Background(),
			req: &spb.GetRunningAndPendingJobsRequest{
				QueueName: "",
			},
			wantCode:  codes.InvalidArgument,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(cfg, nil)
			resp, err := srv.GetRunningAndPendingJobs(tt.ctx, tt.req)

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

func TestGetRunningAndPendingJobs_ContextCancelled(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	srv := newServer(cfg, nil)
	req := &spb.GetRunningAndPendingJobsRequest{QueueName: "gpu-cluster-queue"}
	_, err := srv.GetRunningAndPendingJobs(ctx, req)

	if status.Code(err) != codes.Canceled {
		t.Errorf("GetRunningAndPendingJobs status code = %v, want %v", status.Code(err), codes.Canceled)
	}
}
