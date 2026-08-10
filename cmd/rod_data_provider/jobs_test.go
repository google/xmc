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
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"
	spb "xmanager_cloud/api/protos/rod"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

type mockWorkUnitFetcher struct {
	workUnits     []*essmsgpb.WorkUnit
	nextPageToken string
	err           error
}

//nolint:revive // mock implementation matching WorkUnitFetcher interface
func (m *mockWorkUnitFetcher) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) ([]*essmsgpb.WorkUnit, string, error) {
	if m.err != nil {
		return nil, "", m.err
	}
	return m.workUnits, m.nextPageToken, nil
}

func TestGetRunningAndPendingJobs(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051"}
	now := time.Now()

	tests := []struct {
		name      string
		ctx       context.Context
		req       *spb.GetRunningAndPendingJobsRequest
		fetcher   WorkUnitFetcher
		wantCode  codes.Code
		wantCount int
	}{
		{
			name: "Success With Fetcher",
			ctx:  context.Background(),
			req: &spb.GetRunningAndPendingJobsRequest{
				QueueName: "gpu-cluster-queue",
			},
			fetcher: &mockWorkUnitFetcher{
				workUnits: []*essmsgpb.WorkUnit{
					{
						Name:       "experiments/100/workUnits/1",
						Priority:   essmsgpb.Priority(100),
						CreateTime: timestamppb.New(now.Add(-10 * time.Minute)),
						Notes:      "test@example.com",
						CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
							Status: &essmsgpb.WorkUnitStatus{
								State: essmsgpb.WorkUnitStatus_SCHEDULING,
								SchedulingInformation: &essmsgpb.StructuredMessage{
									AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
										Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{
											QueueInfo: &essmsgpb.QueueInfo{
												PositionInQueue: 1,
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wantCode:  codes.OK,
			wantCount: 1,
		},
		{
			name: "Unavailable With Nil Fetcher",
			ctx:  context.Background(),
			req: &spb.GetRunningAndPendingJobsRequest{
				QueueName: "gpu-cluster-queue",
			},
			fetcher:   nil,
			wantCode:  codes.Unavailable,
			wantCount: 0,
		},
		{
			name: "Missing Queue Name",
			ctx:  context.Background(),
			req: &spb.GetRunningAndPendingJobsRequest{
				QueueName: "",
			},
			fetcher:   &mockWorkUnitFetcher{},
			wantCode:  codes.InvalidArgument,
			wantCount: 0,
		},
		{
			name: "Fetcher Error",
			ctx:  context.Background(),
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
			srv := newServer(cfg, nil, tt.fetcher)
			resp, err := srv.GetRunningAndPendingJobs(tt.ctx, tt.req)

			gotCode := status.Code(err)
			if gotCode != tt.wantCode {
				t.Fatalf("GetRunningAndPendingJobs code = %v, want %v (err: %v)", gotCode, tt.wantCode, err)
			}

			if tt.wantCode != codes.OK {
				return
			}
			if resp == nil {
				t.Fatalf("Expected non-nil response")
			}
			if len(resp.Jobs) != tt.wantCount {
				t.Errorf("Jobs count = %d, want %d", len(resp.Jobs), tt.wantCount)
			}
			if tt.wantCount == 0 {
				return
			}

			gotJob := resp.Jobs[0]
			wantJob := &spb.DashboardJob{
				Xid:                100,
				WorkUnitId:         1,
				User:               "test@example.com",
				Priority:           100,
				Status:             essmsgpb.WorkUnitStatus_SCHEDULING,
				PositionInQueue:    1,
				TimeInQueueSeconds: gotJob.GetTimeInQueueSeconds(),
				CreateTime:         gotJob.GetCreateTime(),
			}
			if diff := cmp.Diff(wantJob, gotJob, protocmp.Transform()); diff != "" {
				t.Errorf("DashboardJob mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetRunningAndPendingJobs_ContextCancelled(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	srv := newServer(cfg, nil, &mockWorkUnitFetcher{})
	req := &spb.GetRunningAndPendingJobsRequest{QueueName: "gpu-cluster-queue"}
	_, err := srv.GetRunningAndPendingJobs(ctx, req)

	if status.Code(err) != codes.Canceled {
		t.Errorf("GetRunningAndPendingJobs status code = %v, want %v", status.Code(err), codes.Canceled)
	}
}
