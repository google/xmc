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

package statushistory

import (
	"context"
	"sort"
	"testing"
	"time"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/emulator"
)

func TestQueueMetricRow(t *testing.T) {
	row := QueueMetricRow{
		QueueName:    "cq-gpu",
		Count:        12,
		TotalUnits:   96,
		ResourceType: "GPU H100",
	}
	window := 1 * time.Hour
	rate := float64(row.Count) / window.Hours()

	if rate != 12.0 {
		t.Errorf("rate = %f, want 12.0", rate)
	}
}

func TestFetchBatchQueueMetrics_EmptyQueues(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(nil)
	batchVel, err := repo.FetchBatchQueueVelocity(ctx, nil, 1*time.Hour)
	if err != nil {
		t.Fatalf("expected no error for empty queueNames, got %v", err)
	}
	if len(batchVel) != 0 {
		t.Errorf("expected empty batch for empty queueNames, got %+v", batchVel)
	}

	batchPending, err := repo.FetchBatchPendingQueueMetrics(ctx, nil)
	if err != nil {
		t.Fatalf("expected no error for empty queueNames, got %v", err)
	}
	if len(batchPending) != 0 {
		t.Errorf("expected empty batch for empty queueNames, got %+v", batchPending)
	}
}

func sortQueueMetricRows(rows []QueueMetricRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].QueueName != rows[j].QueueName {
			return rows[i].QueueName < rows[j].QueueName
		}
		return rows[i].ResourceType < rows[j].ResourceType
	})
}

func compareQueueMetricRows(t *testing.T, got, want []QueueMetricRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d rows. Got: %+v, Want: %+v", len(got), len(want), got, want)
	}

	sortQueueMetricRows(got)
	sortQueueMetricRows(want)

	for i := range got {
		if got[i] != want[i] {
			t.Errorf("row %d mismatch:\n  got:  %+v\n  want: %+v", i, got[i], want[i])
		}
	}
}

func TestFetchBatchQueueVelocity_DefaultMockData(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)

	queueNames := []string{"cpu-queue", "tpu-queue"}
	got, err := FetchBatchQueueVelocity(ctx, essClient, queueNames, 1*time.Hour)
	if err != nil {
		t.Fatalf("FetchBatchQueueVelocity failed: %v", err)
	}

	want := []QueueMetricRow{
		{
			QueueName:    "cpu-queue",
			Count:        1,
			TotalUnits:   16000,
			ResourceType: ResourceTypeCPU,
		},
		{
			QueueName:    "tpu-queue",
			Count:        2,
			TotalUnits:   48,
			ResourceType: "v5p",
		},
	}

	compareQueueMetricRows(t, got, want)
}

func TestFetchBatchPendingQueueMetrics_DefaultMockData(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)

	queueNames := []string{"cpu-queue", "tpu-queue"}
	got, err := FetchBatchPendingQueueMetrics(ctx, essClient, queueNames)
	if err != nil {
		t.Fatalf("FetchBatchPendingQueueMetrics failed: %v", err)
	}

	want := []QueueMetricRow{
		{
			QueueName:    "cpu-queue",
			Count:        1,
			TotalUnits:   8000,
			ResourceType: ResourceTypeCPU,
		},
	}

	compareQueueMetricRows(t, got, want)
}

func TestFetchBatchQueueVelocity_Parameterized(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)

	now := time.Now()

	tests := []struct {
		name       string
		queueNames []string
		window     time.Duration
		mockItems  []WorkUnitStatusHistoryItem
		want       []QueueMetricRow
	}{
		{
			name:       "TransitionStateFiltering",
			queueNames: []string{"q1"},
			window:     1 * time.Hour,
			mockItems: []WorkUnitStatusHistoryItem{
				{
					BitReversedXid: 1,
					WorkUnitId:     101,
					HistoryId:      1,
					QueueName:      "q1",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  10,
				},
				{
					BitReversedXid: 1,
					WorkUnitId:     102,
					HistoryId:      2,
					QueueName:      "q1",
					TransitionTime: now.Add(-5 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_SCHEDULING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  20,
				},
				{
					BitReversedXid: 1,
					WorkUnitId:     103,
					HistoryId:      3,
					QueueName:      "q1",
					TransitionTime: now.Add(-2 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_RUNNING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  30,
				},
			},
			want: []QueueMetricRow{
				{
					QueueName:    "q1",
					Count:        1,
					TotalUnits:   10,
					ResourceType: ResourceTypeCPU,
				},
			},
		},
		{
			name:       "LookbackCutoffWindowFiltering",
			queueNames: []string{"q1"},
			window:     30 * time.Minute,
			mockItems: []WorkUnitStatusHistoryItem{
				{
					BitReversedXid: 1,
					WorkUnitId:     101,
					HistoryId:      1,
					QueueName:      "q1",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  10,
				},
				{
					BitReversedXid: 1,
					WorkUnitId:     102,
					HistoryId:      2,
					QueueName:      "q1",
					TransitionTime: now.Add(-45 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  20,
				},
			},
			want: []QueueMetricRow{
				{
					QueueName:    "q1",
					Count:        1,
					TotalUnits:   10,
					ResourceType: ResourceTypeCPU,
				},
			},
		},
		{
			name:       "QueueNameFiltering",
			queueNames: []string{"q1"},
			window:     1 * time.Hour,
			mockItems: []WorkUnitStatusHistoryItem{
				{
					BitReversedXid: 1,
					WorkUnitId:     101,
					HistoryId:      1,
					QueueName:      "q1",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  10,
				},
				{
					BitReversedXid: 2,
					WorkUnitId:     201,
					HistoryId:      2,
					QueueName:      "q2",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  20,
				},
			},
			want: []QueueMetricRow{
				{
					QueueName:    "q1",
					Count:        1,
					TotalUnits:   10,
					ResourceType: ResourceTypeCPU,
				},
			},
		},
		{
			name:       "MultiResourceAggregation",
			queueNames: []string{"q1"},
			window:     1 * time.Hour,
			mockItems: []WorkUnitStatusHistoryItem{
				{
					BitReversedXid: 1,
					WorkUnitId:     101,
					HistoryId:      1,
					QueueName:      "q1",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  10,
				},
				{
					BitReversedXid: 1,
					WorkUnitId:     102,
					HistoryId:      2,
					QueueName:      "q1",
					TransitionTime: now.Add(-8 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  20,
				},
				{
					BitReversedXid: 1,
					WorkUnitId:     103,
					HistoryId:      3,
					QueueName:      "q1",
					TransitionTime: now.Add(-5 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
					ResourceType:   "GPU",
					ResourceUnits:  4,
				},
			},
			want: []QueueMetricRow{
				{
					QueueName:    "q1",
					Count:        3,
					TotalUnits:   30,
					ResourceType: ResourceTypeCPU,
				},
				{
					QueueName:    "q1",
					Count:        3,
					TotalUnits:   4,
					ResourceType: "GPU",
				},
			},
		},
		{
			name:       "EmptyQueueNames",
			queueNames: []string{},
			window:     1 * time.Hour,
			mockItems: []WorkUnitStatusHistoryItem{
				{
					BitReversedXid: 1,
					WorkUnitId:     101,
					HistoryId:      1,
					QueueName:      "q1",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  10,
				},
			},
			want: nil,
		},
		{
			name:       "EmptyMockDataset",
			queueNames: []string{"q1"},
			window:     1 * time.Hour,
			mockItems:  []WorkUnitStatusHistoryItem{},
			want:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FetchBatchQueueVelocityWithMockItems(ctx, essClient, tt.queueNames, tt.window, tt.mockItems)
			if err != nil {
				t.Fatalf("FetchBatchQueueVelocityWithMockItems failed: %v", err)
			}
			compareQueueMetricRows(t, got, tt.want)
		})
	}
}

func TestFetchBatchPendingQueueMetrics_Parameterized(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)

	now := time.Now()

	tests := []struct {
		name       string
		queueNames []string
		mockItems  []WorkUnitStatusHistoryItem
		want       []QueueMetricRow
	}{
		{
			name:       "TransitionStateFiltering",
			queueNames: []string{"q1"},
			mockItems: []WorkUnitStatusHistoryItem{
				{
					BitReversedXid: 1,
					WorkUnitId:     101,
					HistoryId:      1,
					QueueName:      "q1",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_SCHEDULING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  10,
				},
				{
					BitReversedXid: 1,
					WorkUnitId:     102,
					HistoryId:      2,
					QueueName:      "q1",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  20,
				},
			},
			want: []QueueMetricRow{
				{
					QueueName:    "q1",
					Count:        1,
					TotalUnits:   10,
					ResourceType: ResourceTypeCPU,
				},
			},
		},
		{
			name:       "LatestStateTransition",
			queueNames: []string{"q1"},
			mockItems: []WorkUnitStatusHistoryItem{
				{
					BitReversedXid: 1,
					WorkUnitId:     101,
					HistoryId:      1,
					QueueName:      "q1",
					TransitionTime: now.Add(-20 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_SCHEDULING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  10,
				},
				{
					BitReversedXid: 1,
					WorkUnitId:     101,
					HistoryId:      2,
					QueueName:      "q1",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_RUNNING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  10,
				},
				{
					BitReversedXid: 1,
					WorkUnitId:     102,
					HistoryId:      3,
					QueueName:      "q1",
					TransitionTime: now.Add(-15 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  20,
				},
				{
					BitReversedXid: 1,
					WorkUnitId:     102,
					HistoryId:      4,
					QueueName:      "q1",
					TransitionTime: now.Add(-5 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_SCHEDULING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  20,
				},
			},
			want: []QueueMetricRow{
				{
					QueueName:    "q1",
					Count:        1,
					TotalUnits:   20,
					ResourceType: ResourceTypeCPU,
				},
			},
		},
		{
			name:       "QueueNameFiltering",
			queueNames: []string{"q1"},
			mockItems: []WorkUnitStatusHistoryItem{
				{
					BitReversedXid: 1,
					WorkUnitId:     101,
					HistoryId:      1,
					QueueName:      "q1",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_SCHEDULING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  10,
				},
				{
					BitReversedXid: 2,
					WorkUnitId:     201,
					HistoryId:      2,
					QueueName:      "q2",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_SCHEDULING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  20,
				},
			},
			want: []QueueMetricRow{
				{
					QueueName:    "q1",
					Count:        1,
					TotalUnits:   10,
					ResourceType: ResourceTypeCPU,
				},
			},
		},
		{
			name:       "MultiResourceAggregation",
			queueNames: []string{"q1"},
			mockItems: []WorkUnitStatusHistoryItem{
				{
					BitReversedXid: 1,
					WorkUnitId:     101,
					HistoryId:      1,
					QueueName:      "q1",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_SCHEDULING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  10,
				},
				{
					BitReversedXid: 1,
					WorkUnitId:     102,
					HistoryId:      2,
					QueueName:      "q1",
					TransitionTime: now.Add(-8 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_SCHEDULING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  20,
				},
				{
					BitReversedXid: 1,
					WorkUnitId:     103,
					HistoryId:      3,
					QueueName:      "q1",
					TransitionTime: now.Add(-5 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_SCHEDULING),
					ResourceType:   "GPU",
					ResourceUnits:  4,
				},
			},
			want: []QueueMetricRow{
				{
					QueueName:    "q1",
					Count:        3,
					TotalUnits:   30,
					ResourceType: ResourceTypeCPU,
				},
				{
					QueueName:    "q1",
					Count:        3,
					TotalUnits:   4,
					ResourceType: "GPU",
				},
			},
		},
		{
			name:       "EmptyQueueNames",
			queueNames: []string{},
			mockItems: []WorkUnitStatusHistoryItem{
				{
					BitReversedXid: 1,
					WorkUnitId:     101,
					HistoryId:      1,
					QueueName:      "q1",
					TransitionTime: now.Add(-10 * time.Minute),
					NewState:       int64(essmsgpb.WorkUnitStatus_SCHEDULING),
					ResourceType:   ResourceTypeCPU,
					ResourceUnits:  10,
				},
			},
			want: nil,
		},
		{
			name:       "EmptyMockDataset",
			queueNames: []string{"q1"},
			mockItems:  []WorkUnitStatusHistoryItem{},
			want:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FetchBatchPendingQueueMetricsWithMockItems(ctx, essClient, tt.queueNames, tt.mockItems)
			if err != nil {
				t.Fatalf("FetchBatchPendingQueueMetricsWithMockItems failed: %v", err)
			}
			compareQueueMetricRows(t, got, tt.want)
		})
	}
}
