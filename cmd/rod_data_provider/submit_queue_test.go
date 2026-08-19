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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

func TestGetSubmitQueue_Success(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		clusterName string
		localQueues []*kueuev1beta1.LocalQueue
		wantQueue   string
	}{
		{
			name:        "single matching local queue",
			clusterName: "gpu-cluster-queue",
			localQueues: []*kueuev1beta1.LocalQueue{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "lq-gpu-1", Namespace: "default"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "gpu-cluster-queue"},
				},
			},
			wantQueue: "lq-gpu-1",
		},
		{
			name:        "selects local queue with least scheduled jobs",
			clusterName: "tpu-cluster-queue",
			localQueues: []*kueuev1beta1.LocalQueue{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "lq-tpu-busy", Namespace: "team-a"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "tpu-cluster-queue"},
					Status:     kueuev1beta1.LocalQueueStatus{PendingWorkloads: 5, AdmittedWorkloads: 3},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "lq-tpu-idle", Namespace: "team-b"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "tpu-cluster-queue"},
					Status:     kueuev1beta1.LocalQueueStatus{PendingWorkloads: 1, AdmittedWorkloads: 0},
				},
			},
			wantQueue: "lq-tpu-idle",
		},
		{
			name:        "tie breaker selects alphabetically first local queue using namespaced key",
			clusterName: "cpu-cluster-queue",
			localQueues: []*kueuev1beta1.LocalQueue{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "shared-name", Namespace: "team-z"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "cpu-cluster-queue"},
					Status:     kueuev1beta1.LocalQueueStatus{PendingWorkloads: 2, AdmittedWorkloads: 1},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "shared-name", Namespace: "team-a"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "cpu-cluster-queue"},
					Status:     kueuev1beta1.LocalQueueStatus{PendingWorkloads: 2, AdmittedWorkloads: 1},
				},
			},
			wantQueue: "shared-name",
		},
		{
			name:        "integer overflow guard with large workload counts",
			clusterName: "high-throughput-cq",
			localQueues: []*kueuev1beta1.LocalQueue{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "lq-overflow-1", Namespace: "default"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "high-throughput-cq"},
					Status:     kueuev1beta1.LocalQueueStatus{PendingWorkloads: 2147483647, AdmittedWorkloads: 100},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "lq-overflow-2", Namespace: "default"},
					Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "high-throughput-cq"},
					Status:     kueuev1beta1.LocalQueueStatus{PendingWorkloads: 100, AdmittedWorkloads: 100},
				},
			},
			wantQueue: "lq-overflow-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := cache.NewKueueCache()
			for _, lq := range tt.localQueues {
				c.LocalQueues.Set(lq.Namespace+"/"+lq.Name, lq)
			}
			srv := newServer(&Config{}, c, nil, nil)

			resp, err := srv.GetSubmitQueue(ctx, &spb.GetSubmitQueueRequest{QueueName: tt.clusterName})
			if err != nil {
				t.Fatalf("GetSubmitQueue(%q) unexpected error: %v", tt.clusterName, err)
			}
			if resp.GetSubmitQueue() != tt.wantQueue {
				t.Errorf("GetSubmitQueue(%q) = %q, want %q", tt.clusterName, resp.GetSubmitQueue(), tt.wantQueue)
			}
		})
	}
}

func TestGetSubmitQueue_Errors(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	validCache := cache.NewKueueCache()
	validCache.LocalQueues.Set("default/lq-1", &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "lq-1", Namespace: "default"},
		Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "cq-exist"},
	})
	srv := newServer(&Config{}, validCache, nil, nil)

	tests := []struct {
		name     string
		ctx      context.Context
		server   *server
		req      *spb.GetSubmitQueueRequest
		wantCode codes.Code
	}{
		{
			name:     "canceled context",
			ctx:      canceledCtx,
			server:   srv,
			req:      &spb.GetSubmitQueueRequest{QueueName: "cq-exist"},
			wantCode: codes.Canceled,
		},
		{
			name:     "nil request",
			ctx:      context.Background(),
			server:   srv,
			req:      nil,
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "empty queue_name",
			ctx:      context.Background(),
			server:   srv,
			req:      &spb.GetSubmitQueueRequest{QueueName: ""},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "nil cache",
			ctx:      context.Background(),
			server:   newServer(&Config{}, nil, nil, nil),
			req:      &spb.GetSubmitQueueRequest{QueueName: "cq-exist"},
			wantCode: codes.Unavailable,
		},
		{
			name:     "uninitialized cache local queues",
			ctx:      context.Background(),
			server:   newServer(&Config{}, &cache.KueueCache{}, nil, nil),
			req:      &spb.GetSubmitQueueRequest{QueueName: "cq-exist"},
			wantCode: codes.Unavailable,
		},
		{
			name:     "no matching local queue",
			ctx:      context.Background(),
			server:   srv,
			req:      &spb.GetSubmitQueueRequest{QueueName: "non-existent-cq"},
			wantCode: codes.NotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.server.GetSubmitQueue(tt.ctx, tt.req)
			if err == nil {
				t.Fatalf("expected error code %v, got nil", tt.wantCode)
			}
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected status error, got %v", err)
			}
			if st.Code() != tt.wantCode {
				t.Errorf("got code %v, want %v (error: %v)", st.Code(), tt.wantCode, err)
			}
		})
	}
}
