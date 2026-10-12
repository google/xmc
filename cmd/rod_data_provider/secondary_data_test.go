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
	spb "github.com/google/xmc/api/protos/rod"
	"github.com/google/xmc/cmd/rod_data_provider/cache"
	"github.com/google/xmc/internal/storage/spanner/statushistory"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

type mockVelocityFetcher struct {
	velocityRows []statushistory.QueueMetricRow
	pendingRows  []statushistory.QueueMetricRow
	err          error
}

var _ QueueVelocityFetcher = (*mockVelocityFetcher)(nil)

func (m *mockVelocityFetcher) FetchBatchQueueVelocity(ctx context.Context, queueNames []string, window time.Duration) ([]statushistory.QueueMetricRow, error) {
	return m.velocityRows, m.err
}

func (m *mockVelocityFetcher) FetchBatchPendingQueueMetrics(ctx context.Context, queueNames []string) ([]statushistory.QueueMetricRow, error) {
	return m.pendingRows, m.err
}

func TestBatchGetQueueSecondaryData(t *testing.T) {
	ctx := context.Background()
	mockFetcher := &mockVelocityFetcher{
		velocityRows: []statushistory.QueueMetricRow{
			{
				QueueName:    "lq-1",
				Count:        12,
				TotalUnits:   96,
				ResourceType: "nvidia-tesla-h100",
			},
		},
		pendingRows: []statushistory.QueueMetricRow{
			{
				QueueName:    "lq-1",
				Count:        5,
				TotalUnits:   40,
				ResourceType: "nvidia-tesla-h100",
			},
		},
	}

	c := cache.NewKueueCache()
	c.LocalQueues.Set("team-a/lq-1", &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "lq-1", Namespace: "team-a"},
		Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "cq-gpu"},
	})

	srv := &server{
		cache:         c,
		velocityStore: mockFetcher,
	}

	req := &spb.BatchGetQueueSecondaryDataRequest{
		QueueNames: []string{"cq-gpu"},
		Range:      spb.RangePreset_RANGE_PRESET_LAST_HOUR,
	}

	want := &spb.BatchGetQueueSecondaryDataResponse{
		QueuePredictability: map[string]*spb.ClusterQueueSecondaryMetrics{
			"cq-gpu": {
				QueuePredictability: []*spb.QueuePredictability{
					{
						VelocityRange: spb.RangePreset_RANGE_PRESET_LAST_HOUR,
						JobVelocity:   12.0,
						ResourceVelocity: &spb.ResourceVelocity{
							ResourceType: "nvidia-tesla-h100",
							Velocity:     96.0,
						},
						PendingJobs: 5,
						PendingResourceRequests: &spb.Resource{
							ResourceType: "nvidia-tesla-h100",
							Count:        40.0,
						},
					},
				},
			},
		},
	}

	got, err := srv.BatchGetQueueSecondaryData(ctx, req)
	if err != nil {
		t.Fatalf("BatchGetQueueSecondaryData failed: %v", err)
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("BatchGetQueueSecondaryData mismatch (-want +got):\n%s", diff)
	}
}

func TestBatchGetQueueSecondaryData_MultiResource(t *testing.T) {
	ctx := context.Background()
	mockFetcher := &mockVelocityFetcher{
		velocityRows: []statushistory.QueueMetricRow{
			{QueueName: "lq-mixed", Count: 10, TotalUnits: 16000, ResourceType: "CPU"},
			{QueueName: "lq-mixed", Count: 10, TotalUnits: 32, ResourceType: "nvidia-tesla-h100"},
		},
		pendingRows: []statushistory.QueueMetricRow{
			{QueueName: "lq-mixed", Count: 4, TotalUnits: 8000, ResourceType: "CPU"},
			{QueueName: "lq-mixed", Count: 4, TotalUnits: 16, ResourceType: "nvidia-tesla-h100"},
		},
	}

	c := cache.NewKueueCache()
	c.LocalQueues.Set("team-a/lq-mixed", &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "lq-mixed", Namespace: "team-a"},
		Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "cq-mixed"},
	})

	srv := &server{cache: c, velocityStore: mockFetcher}
	req := &spb.BatchGetQueueSecondaryDataRequest{
		QueueNames: []string{"cq-mixed"},
		Range:      spb.RangePreset_RANGE_PRESET_LAST_HOUR,
	}

	want := &spb.BatchGetQueueSecondaryDataResponse{
		QueuePredictability: map[string]*spb.ClusterQueueSecondaryMetrics{
			"cq-mixed": {
				QueuePredictability: []*spb.QueuePredictability{
					{
						VelocityRange: spb.RangePreset_RANGE_PRESET_LAST_HOUR,
						JobVelocity:   10.0,
						ResourceVelocity: &spb.ResourceVelocity{
							ResourceType: "CPU",
							Velocity:     16.0,
						},
						PendingJobs: 4,
						PendingResourceRequests: &spb.Resource{
							ResourceType: "CPU",
							Count:        8.0,
						},
					},
					{
						VelocityRange: spb.RangePreset_RANGE_PRESET_LAST_HOUR,
						JobVelocity:   10.0,
						ResourceVelocity: &spb.ResourceVelocity{
							ResourceType: "nvidia-tesla-h100",
							Velocity:     32.0,
						},
						PendingJobs: 4,
						PendingResourceRequests: &spb.Resource{
							ResourceType: "nvidia-tesla-h100",
							Count:        16.0,
						},
					},
				},
			},
		},
	}

	got, err := srv.BatchGetQueueSecondaryData(ctx, req)
	if err != nil {
		t.Fatalf("BatchGetQueueSecondaryData failed: %v", err)
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("BatchGetQueueSecondaryData multi-resource mismatch (-want +got):\n%s", diff)
	}
}

func TestBatchGetQueueSecondaryData_NilRequest(t *testing.T) {
	srv := &server{}
	_, err := srv.BatchGetQueueSecondaryData(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestBatchGetQueueSecondaryData_StorageError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
	}{
		{
			name:     "Context Canceled",
			err:      context.Canceled,
			wantCode: codes.Canceled,
		},
		{
			name:     "Wrapped Context Canceled",
			err:      fmt.Errorf("storage error: %w", context.Canceled),
			wantCode: codes.Canceled,
		},
		{
			name:     "Context DeadlineExceeded",
			err:      context.DeadlineExceeded,
			wantCode: codes.DeadlineExceeded,
		},
		{
			name:     "Wrapped Context DeadlineExceeded",
			err:      fmt.Errorf("storage timeout: %w", context.DeadlineExceeded),
			wantCode: codes.DeadlineExceeded,
		},
		{
			name:     "Generic Storage Error",
			err:      status.Error(codes.Internal, "database error"),
			wantCode: codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockFetcher := &mockVelocityFetcher{err: tt.err}
			srv := &server{velocityStore: mockFetcher}
			req := &spb.BatchGetQueueSecondaryDataRequest{
				QueueNames: []string{"cq-gpu"},
				Range:      spb.RangePreset_RANGE_PRESET_LAST_HOUR,
			}
			_, err := srv.BatchGetQueueSecondaryData(context.Background(), req)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected gRPC status error, got %v", err)
			}
			if st.Code() != tt.wantCode {
				t.Errorf("status code = %v, want %v", st.Code(), tt.wantCode)
			}
		})
	}
}

func TestRangePresetToDuration(t *testing.T) {
	tests := []struct {
		preset spb.RangePreset
		want   time.Duration
	}{
		{spb.RangePreset_RANGE_PRESET_LAST_HOUR, 1 * time.Hour},
		{spb.RangePreset_RANGE_PRESET_LAST_DAY, 24 * time.Hour},
		{spb.RangePreset_RANGE_PRESET_LAST_WEEK, 168 * time.Hour},
		{spb.RangePreset_RANGE_PRESET_UNSPECIFIED, 1 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.preset.String(), func(t *testing.T) {
			got := rangePresetToDuration(tt.preset)
			if got != tt.want {
				t.Errorf("rangePresetToDuration(%v) = %v, want %v", tt.preset, got, tt.want)
			}
		})
	}
}

func TestAggregateQueueMetricRows(t *testing.T) {
	tests := []struct {
		name string
		rows []statushistory.QueueMetricRow
		want map[string]aggregatedQueueMetric
	}{
		{
			name: "empty rows slice returns empty map",
			rows: nil,
			want: map[string]aggregatedQueueMetric{},
		},
		{
			name: "aggregates single queue with multiple resources",
			rows: []statushistory.QueueMetricRow{
				{QueueName: "cq-mixed", Count: 10, TotalUnits: 16000, ResourceType: "CPU"},
				{QueueName: "cq-mixed", Count: 10, TotalUnits: 32, ResourceType: "nvidia-tesla-h100"},
			},
			want: map[string]aggregatedQueueMetric{
				"cq-mixed": {
					jobCount: 10,
					resourceUnitsMap: map[string]int64{
						"CPU":               16000,
						"nvidia-tesla-h100": 32,
					},
				},
			},
		},
		{
			name: "ignores empty resource type",
			rows: []statushistory.QueueMetricRow{
				{QueueName: "cq-empty-res", Count: 5, TotalUnits: 0, ResourceType: ""},
			},
			want: map[string]aggregatedQueueMetric{
				"cq-empty-res": {
					jobCount:         5,
					resourceUnitsMap: map[string]int64{},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateQueueMetricRows(tt.rows)
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(aggregatedQueueMetric{})); diff != "" {
				t.Errorf("aggregateQueueMetricRows() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMergeClusterQueueMetrics(t *testing.T) {
	mapping := map[string][]string{
		"cq-gpu": {"cq-gpu", "team-a/lq-1", "team-b/lq-2"},
		"cq-cpu": {"cq-cpu"},
	}
	metrics := map[string]aggregatedQueueMetric{
		"cq-gpu": {
			jobCount:         2,
			resourceUnitsMap: map[string]int64{"nvidia-tesla-h100": 16},
		},
		"team-a/lq-1": {
			jobCount:         5,
			resourceUnitsMap: map[string]int64{"nvidia-tesla-h100": 40},
		},
		"team-b/lq-2": {
			jobCount:         3,
			resourceUnitsMap: map[string]int64{"nvidia-tesla-h100": 24},
		},
		"cq-cpu": {
			jobCount:         10,
			resourceUnitsMap: map[string]int64{"CPU": 8000},
		},
	}

	got := mergeClusterQueueMetrics(mapping, metrics)
	want := map[string]aggregatedQueueMetric{
		"cq-gpu": {
			jobCount:         10, // 2 + 5 + 3
			resourceUnitsMap: map[string]int64{"nvidia-tesla-h100": 80},
		},
		"cq-cpu": {
			jobCount:         10,
			resourceUnitsMap: map[string]int64{"CPU": 8000},
		},
	}

	if diff := cmp.Diff(want, got, cmp.AllowUnexported(aggregatedQueueMetric{})); diff != "" {
		t.Errorf("mergeClusterQueueMetrics() mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildQueuePredictabilities_NoResources(t *testing.T) {
	vel := aggregatedQueueMetric{jobCount: 6, resourceUnitsMap: map[string]int64{}}
	pending := aggregatedQueueMetric{jobCount: 2, resourceUnitsMap: map[string]int64{}}

	got := buildQueuePredictabilities(spb.RangePreset_RANGE_PRESET_LAST_HOUR, 2.0, vel, pending)
	want := []*spb.QueuePredictability{
		{
			VelocityRange: spb.RangePreset_RANGE_PRESET_LAST_HOUR,
			JobVelocity:   3.0,
			PendingJobs:   2,
		},
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("buildQueuePredictabilities() no resources mismatch (-want +got):\n%s", diff)
	}
}
