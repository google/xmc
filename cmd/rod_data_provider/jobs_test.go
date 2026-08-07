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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/rod"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/validator/validator"
)

type mockWorkUnitFetcher struct {
	workUnits         []*essmsgpb.WorkUnit
	nextPageToken     string
	err               error
	capturedQueueName string
	capturedPageSize  int32
	capturedPageToken string
}

func (m *mockWorkUnitFetcher) FetchActiveWorkUnitsByQueue(ctx context.Context, queueName string, pageSize int32, pageToken string) ([]*essmsgpb.WorkUnit, string, error) {
	m.capturedQueueName = queueName
	m.capturedPageSize = pageSize
	m.capturedPageToken = pageToken
	if m.err != nil {
		return nil, "", m.err
	}
	return m.workUnits, m.nextPageToken, nil
}

func TestGetRunningAndPendingJobs(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051"}

	tests := []struct {
		name         string
		req          *spb.GetRunningAndPendingJobsRequest
		fetcher      *mockWorkUnitFetcher
		wantCode     codes.Code
		wantCount    int
		wantUser     string
		wantPriority string
		wantPos      string
	}{
		{
			name: "Success With Fetcher",
			req: &spb.GetRunningAndPendingJobsRequest{
				QueueName: "gpu-cluster-queue",
				PageSize:  10,
				PageToken: "token-123",
			},
			fetcher: &mockWorkUnitFetcher{
				workUnits: []*essmsgpb.WorkUnit{
					{
						Name:     validator.ConstructWorkUnitName(100, 1),
						Priority: essmsgpb.Priority_HIGH,
						CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
							Status: &essmsgpb.WorkUnitStatus{
								State: essmsgpb.WorkUnitStatus_SCHEDULING,
							},
						},
					},
				},
				nextPageToken: "next-token-456",
			},
			wantCode:     codes.OK,
			wantCount:    1,
			wantUser:     "",
			wantPriority: "High",
			wantPos:      "1",
		},
		{
			name: "Nil Request",
			req:  nil,
			fetcher: &mockWorkUnitFetcher{
				workUnits: []*essmsgpb.WorkUnit{},
			},
			wantCode:  codes.InvalidArgument,
			wantCount: 0,
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
			var fetcher WorkUnitFetcher
			if tt.fetcher != nil {
				fetcher = tt.fetcher
			}

			srv := newServerWithFetcher(cfg, fetcher)
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
				if tt.fetcher != nil {
					if tt.fetcher.capturedQueueName != tt.req.GetQueueName() {
						t.Errorf("Captured queueName = %q, want %q", tt.fetcher.capturedQueueName, tt.req.GetQueueName())
					}
					if tt.fetcher.capturedPageSize != tt.req.GetPageSize() {
						t.Errorf("Captured pageSize = %d, want %d", tt.fetcher.capturedPageSize, tt.req.GetPageSize())
					}
					if tt.fetcher.capturedPageToken != tt.req.GetPageToken() {
						t.Errorf("Captured pageToken = %q, want %q", tt.fetcher.capturedPageToken, tt.req.GetPageToken())
					}
					if len(resp.Jobs) > 0 {
						job := resp.Jobs[0]
						if job.GetUser() != tt.wantUser {
							t.Errorf("Job User = %q, want %q", job.GetUser(), tt.wantUser)
						}
						if job.GetPriority() != tt.wantPriority {
							t.Errorf("Job Priority = %q, want %q", job.GetPriority(), tt.wantPriority)
						}
						if job.GetPosition() != tt.wantPos {
							t.Errorf("Job Position = %q, want %q", job.GetPosition(), tt.wantPos)
						}
					}
				}
			}
		})
	}
}

func TestGetRunningAndPendingJobs_MixedPositions(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051"}
	fetcher := &mockWorkUnitFetcher{
		workUnits: []*essmsgpb.WorkUnit{
			{
				Name:     validator.ConstructWorkUnitName(100, 1),
				Priority: essmsgpb.Priority_HIGH,
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						State: essmsgpb.WorkUnitStatus_SCHEDULING,
					},
				},
			},
			{
				Name:     validator.ConstructWorkUnitName(100, 2),
				Priority: essmsgpb.Priority_NORMAL,
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						State: essmsgpb.WorkUnitStatus_RUNNING,
					},
				},
			},
			{
				Name:     validator.ConstructWorkUnitName(100, 3),
				Priority: essmsgpb.Priority_LOW,
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						State: essmsgpb.WorkUnitStatus_INITIALIZING,
					},
				},
			},
		},
	}

	srv := newServerWithFetcher(cfg, fetcher)
	resp, err := srv.GetRunningAndPendingJobs(context.Background(), &spb.GetRunningAndPendingJobsRequest{
		QueueName: "gpu-queue",
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(resp.Jobs) != 3 {
		t.Fatalf("Expected 3 jobs, got %d", len(resp.Jobs))
	}

	wantPositions := []string{"1", "N/A-running", "2"}
	wantPriorities := []string{"High", "Normal", "Low"}

	for i, job := range resp.Jobs {
		if job.GetPosition() != wantPositions[i] {
			t.Errorf("Job[%d] position = %q, want %q", i, job.GetPosition(), wantPositions[i])
		}
		if job.GetPriority() != wantPriorities[i] {
			t.Errorf("Job[%d] priority = %q, want %q", i, job.GetPriority(), wantPriorities[i])
		}
	}
}

func TestFormatPriority(t *testing.T) {
	tests := []struct {
		name     string
		prio     int64
		wantText string
	}{
		{name: "High Priority", prio: 250, wantText: "High"},
		{name: "Normal Priority", prio: 100, wantText: "Normal"},
		{name: "Low Priority", prio: 10, wantText: "Low"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPriority(tt.prio); got != tt.wantText {
				t.Errorf("formatPriority(%d) = %q, want %q", tt.prio, got, tt.wantText)
			}
		})
	}
}

func TestFormatTimeInQueue(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		t        time.Time
		wantText string
	}{
		{name: "Just Now", t: now.Add(-10 * time.Second), wantText: "just now"},
		{name: "Minutes Ago", t: now.Add(-15 * time.Minute), wantText: "15m ago"},
		{name: "Hours Ago", t: now.Add(-3 * time.Hour), wantText: "3h ago"},
		{name: "Days Ago", t: now.Add(-48 * time.Hour), wantText: "2d ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTimeInQueue(tt.t); got != tt.wantText {
				t.Errorf("formatTimeInQueue(%v) = %q, want %q", tt.t, got, tt.wantText)
			}
		})
	}
}
