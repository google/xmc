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
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/protobuf/v2/proto/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/workunit"
)

var (
	baseReq = &spb.GetRunningAndPendingJobsRequest{
		QueueName: "gpu-cluster-queue",
	}

	baseWorkUnit = &essmsgpb.WorkUnit{
		Name:     "experiments/100/workUnits/1",
		Priority: essmsgpb.Priority(100),
		Notes:    "test@example.com",
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
		Executable: &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
						{
							Spec: &essmsgpb.ExecutableSpec{
								Resources: &essmsgpb.ResourceSet{
									Resources: &essmsgpb.ResourceSet_CpuResources{
										CpuResources: &essmsgpb.CpuResources{
											CpuMillicores: 4000,
											MemoryBytes:   8192,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
)

type mockWorkUnitFetcher struct {
	workUnits      []*essmsgpb.WorkUnit
	nextPageToken  string
	err            error
	lastQueueNames []string
	lastPageSize   int32
	lastPageToken  string
}

func (m *mockWorkUnitFetcher) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) (*workunit.ActiveWorkUnitsResult, error) {
	m.lastQueueNames = queueNames
	m.lastPageSize = pageSize
	m.lastPageToken = pageToken
	if m.err != nil {
		return nil, m.err
	}
	if len(queueNames) == 0 {
		return &workunit.ActiveWorkUnitsResult{
			WorkUnits: []*essmsgpb.WorkUnit{},
		}, nil
	}
	return &workunit.ActiveWorkUnitsResult{
		WorkUnits:     m.workUnits,
		NextPageToken: m.nextPageToken,
	}, nil
}

func TestGetRunningAndPendingJobs(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051"}
	now := time.Now()

	tests := []struct {
		name       string
		ctx        context.Context
		req        *spb.GetRunningAndPendingJobsRequest
		setupCache func() *cache.KueueCache
		fetcher    WorkUnitFetcher
		wantCode   codes.Code
		wantCount  int
	}{
		{
			name: "Success With Fetcher",
			ctx:  context.Background(),
			req:  baseReq,
			setupCache: func() *cache.KueueCache {
				c := cache.NewKueueCache()
				c.LocalQueues.Set("lq-1", &kueuev1beta1.LocalQueue{
					ObjectMeta: metav1.ObjectMeta{Name: "lq-1"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "gpu-cluster-queue"},
				})
				return c
			},
			fetcher: &mockWorkUnitFetcher{
				workUnits: []*essmsgpb.WorkUnit{
					proto.Modify(baseWorkUnit, func(wu *essmsgpb.WorkUnit) {
						wu.CreateTime = timestamppb.New(now.Add(-10 * time.Minute))
					}),
				},
			},
			wantCode:  codes.OK,
			wantCount: 1,
		},
		{
			name: "Success With PageSize and PageToken",
			ctx:  context.Background(),
			req: &spb.GetRunningAndPendingJobsRequest{
				QueueName: "gpu-cluster-queue",
				PageSize:  10,
				PageToken: "token-abc",
			},
			setupCache: func() *cache.KueueCache {
				c := cache.NewKueueCache()
				c.LocalQueues.Set("lq-1", &kueuev1beta1.LocalQueue{
					ObjectMeta: metav1.ObjectMeta{Name: "lq-1"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "gpu-cluster-queue"},
				})
				return c
			},
			fetcher: &mockWorkUnitFetcher{
				workUnits: []*essmsgpb.WorkUnit{
					proto.Modify(baseWorkUnit, func(wu *essmsgpb.WorkUnit) {
						wu.CreateTime = timestamppb.New(now.Add(-10 * time.Minute))
					}),
				},
				nextPageToken: "token-xyz",
			},
			wantCode:  codes.OK,
			wantCount: 1,
		},
		{
			name:      "Unavailable With Nil Fetcher",
			ctx:       context.Background(),
			req:       baseReq,
			fetcher:   nil,
			wantCode:  codes.Unavailable,
			wantCount: 0,
		},
		{
			name: "Missing Queue Name",
			ctx:  context.Background(),
			req: proto.Modify(baseReq, func(r *spb.GetRunningAndPendingJobsRequest) {
				r.QueueName = ""
			}),
			fetcher:   &mockWorkUnitFetcher{},
			wantCode:  codes.InvalidArgument,
			wantCount: 0,
		},
		{
			name: "Negative Page Size",
			ctx:  context.Background(),
			req: proto.Modify(baseReq, func(r *spb.GetRunningAndPendingJobsRequest) {
				r.PageSize = -1
			}),
			fetcher:   &mockWorkUnitFetcher{},
			wantCode:  codes.InvalidArgument,
			wantCount: 0,
		},
		{
			name:      "Fetcher Error",
			ctx:       context.Background(),
			req:       baseReq,
			fetcher:   &mockWorkUnitFetcher{err: fmt.Errorf("spanner connection failure")},
			wantCode:  codes.Internal,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var kCache *cache.KueueCache
			if tt.setupCache != nil {
				kCache = tt.setupCache()
			}
			srv := newServer(cfg, kCache, tt.fetcher, nil)
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
			if fetcherImpl, ok := tt.fetcher.(*mockWorkUnitFetcher); ok && tt.req.PageSize > 0 {
				if fetcherImpl.lastPageSize != tt.req.PageSize {
					t.Errorf("PageSize = %d, want %d", fetcherImpl.lastPageSize, tt.req.PageSize)
				}
				if fetcherImpl.lastPageToken != tt.req.PageToken {
					t.Errorf("PageToken = %q, want %q", fetcherImpl.lastPageToken, tt.req.PageToken)
				}
				if resp.NextPageToken != fetcherImpl.nextPageToken {
					t.Errorf("NextPageToken = %q, want %q", resp.NextPageToken, fetcherImpl.nextPageToken)
				}
			}
			if tt.wantCount == 0 {
				return
			}

			gotJob := resp.Jobs[0]
			if gotJob.GetTimeInQueueSeconds() < 599 || gotJob.GetTimeInQueueSeconds() > 601 {
				t.Errorf("TimeInQueueSeconds = %d, want ~600", gotJob.GetTimeInQueueSeconds())
			}
			if gotJob.GetCreateTime() == nil {
				t.Errorf("CreateTime is nil, want non-nil timestamp")
			}
			wantJob := &spb.DashboardJob{
				Xid:             100,
				WorkUnitId:      1,
				User:            "",
				Priority:        100,
				Status:          essmsgpb.WorkUnitStatus_SCHEDULING,
				PositionInQueue: 1,
				RequestedResources: []*spb.Resource{
					{ResourceType: "cpu", Count: 4.0},
					{ResourceType: "memory", Count: 8192.0},
				},
			}
			if diff := cmp.Diff(wantJob, gotJob, protocmp.Transform(), protocmp.IgnoreFields(&spb.DashboardJob{}, "time_in_queue_seconds", "create_time")); diff != "" {
				t.Errorf("DashboardJob mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetRunningAndPendingJobs_ContextCancelled(t *testing.T) {
	cfg := &Config{Port: "8080", GRPCPort: "50051"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	srv := newServer(cfg, nil, &mockWorkUnitFetcher{}, nil)
	req := baseReq
	_, err := srv.GetRunningAndPendingJobs(ctx, req)

	if status.Code(err) != codes.Canceled {
		t.Errorf("GetRunningAndPendingJobs status code = %v, want %v", status.Code(err), codes.Canceled)
	}
}

func TestMapWorkUnitToDashboardJob_RequestedResources(t *testing.T) {
	now := time.Now()
	wu := proto.Modify(baseWorkUnit, func(w *essmsgpb.WorkUnit) {
		w.Executable = &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
						{
							Replicas: 2,
							Spec: &essmsgpb.ExecutableSpec{
								Resources: &essmsgpb.ResourceSet{
									Resources: &essmsgpb.ResourceSet_GpuResources{
										GpuResources: &essmsgpb.GpuResources{
											Type:  "nvidia-tesla-h100",
											Count: 4,
										},
									},
								},
							},
						},
						{
							Replicas: 1,
							Spec: &essmsgpb.ExecutableSpec{
								Resources: &essmsgpb.ResourceSet{
									Resources: &essmsgpb.ResourceSet_GpuResources{
										GpuResources: &essmsgpb.GpuResources{
											Type:  "nvidia-tesla-h100",
											Count: 2,
										},
									},
								},
							},
						},
					},
				},
			},
		}
	})

	job := mapWorkUnitToDashboardJob(context.Background(), wu, now)
	if job == nil {
		t.Fatalf("mapWorkUnitToDashboardJob returned nil")
	}
	if len(job.GetRequestedResources()) != 1 {
		t.Fatalf("RequestedResources len = %d, want 1", len(job.GetRequestedResources()))
	}
	res := job.GetRequestedResources()[0]
	if res.GetResourceType() != "nvidia-tesla-h100" || res.GetCount() != 10.0 {
		t.Errorf("RequestedResource = %v %v, want nvidia-tesla-h100 10.0", res.GetResourceType(), res.GetCount())
	}
}

func TestGetRunningAndPendingJobs_TargetQueuesResolution(t *testing.T) {
	tests := []struct {
		name       string
		setupCache func() *cache.KueueCache
		queueName  string
		wantQueues []string
	}{
		{
			name: "single mapped local queue",
			setupCache: func() *cache.KueueCache {
				c := cache.NewKueueCache()
				c.LocalQueues.Set("lq-1", &kueuev1beta1.LocalQueue{
					ObjectMeta: metav1.ObjectMeta{Name: "lq-1"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "cq-test"},
				})
				return c
			},
			queueName:  "cq-test",
			wantQueues: []string{"lq-1"},
		},
		{
			name: "multiple mapped local queues sorted and deduplicated",
			setupCache: func() *cache.KueueCache {
				c := cache.NewKueueCache()
				c.LocalQueues.Set("team-a/lq-2", &kueuev1beta1.LocalQueue{
					ObjectMeta: metav1.ObjectMeta{Name: "lq-2", Namespace: "team-a"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "cq-test"},
				})
				c.LocalQueues.Set("team-a/lq-1", &kueuev1beta1.LocalQueue{
					ObjectMeta: metav1.ObjectMeta{Name: "lq-1", Namespace: "team-a"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "cq-test"},
				})
				c.LocalQueues.Set("team-b/lq-1", &kueuev1beta1.LocalQueue{
					ObjectMeta: metav1.ObjectMeta{Name: "lq-1", Namespace: "team-b"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "cq-test"},
				})
				c.LocalQueues.Set("team-c/other-lq", &kueuev1beta1.LocalQueue{
					ObjectMeta: metav1.ObjectMeta{Name: "other-lq", Namespace: "team-c"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "other-cq"},
				})
				return c
			},
			queueName:  "cq-test",
			wantQueues: []string{"lq-1", "lq-2"},
		},
		{
			name: "returns nil when no local queues match",
			setupCache: func() *cache.KueueCache {
				c := cache.NewKueueCache()
				c.LocalQueues.Set("team-c/other-lq", &kueuev1beta1.LocalQueue{
					ObjectMeta: metav1.ObjectMeta{Name: "other-lq", Namespace: "team-c"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "other-cq"},
				})
				return c
			},
			queueName:  "cq-test",
			wantQueues: nil,
		},
		{
			name:       "returns nil when cache is nil",
			setupCache: func() *cache.KueueCache { return nil },
			queueName:  "cq-test",
			wantQueues: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := &mockWorkUnitFetcher{}
			srv := newServer(&Config{}, tt.setupCache(), fetcher, nil)

			_, err := srv.GetRunningAndPendingJobs(context.Background(), &spb.GetRunningAndPendingJobsRequest{
				QueueName: tt.queueName,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.wantQueues, fetcher.lastQueueNames); diff != "" {
				t.Errorf("targetQueues mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
