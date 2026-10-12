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

	"cloud.google.com/go/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
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

func TestFetchBatchQueueMetrics_NilClient(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(nil)
	batchVel, err := repo.FetchBatchQueueVelocity(ctx, []string{"cq-gpu"}, 1*time.Hour)
	if err != nil {
		t.Fatalf("expected no error for nil client batch velocity, got %v", err)
	}
	if len(batchVel) != 0 {
		t.Errorf("expected empty batch map for nil client, got %+v", batchVel)
	}

	batchPending, err := repo.FetchBatchPendingQueueMetrics(ctx, []string{"cq-gpu"})
	if err != nil {
		t.Fatalf("expected no error for nil client batch pending, got %v", err)
	}
	if len(batchPending) != 0 {
		t.Errorf("expected empty batch map for nil client, got %+v", batchPending)
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

func newMockItem(wid int64, queue string, offset time.Duration, state essmsgpb.WorkUnitStatus_WorkUnitState, resType string, units int64) WorkUnitStatusHistoryItem {
	return WorkUnitStatusHistoryItem{
		BitReversedXid: 1,
		WorkUnitId:     wid,
		HistoryId:      0,
		QueueName:      queue,
		CreateTime:     time.Now().Add(offset),
		NewState:       int64(state),
		ResourceType:   resType,
		ResourceUnits:  units,
	}
}

func insertMockItems(ctx context.Context, t *testing.T, client *spanner.Client, mockItems []WorkUnitStatusHistoryItem) {
	t.Helper()
	if len(mockItems) == 0 {
		return
	}

	var historyMutations []*spanner.Mutation
	for i, item := range mockItems {
		xid := item.BitReversedXid
		historyID := item.HistoryId
		if historyID == 0 {
			historyID = int64(i + 1)
		}
		var eid *int64 = &historyID
		var prevState *essmsgpb.WorkUnitStatus_WorkUnitState
		newState := essmsgpb.WorkUnitStatus_WorkUnitState(item.NewState).Enum()
		m, err := spannerdata.WorkUnitStatusHistoryToInsertMutation(
			xid, item.WorkUnitId, historyID, eid, item.QueueName, prevState, newState, item.ResourceType, item.ResourceUnits, item.CreateTime,
		)
		if err != nil {
			t.Fatalf("WorkUnitStatusHistoryToInsertMutation failed: %v", err)
		}
		historyMutations = append(historyMutations, m)
	}

	if len(historyMutations) > 0 {
		if _, err := client.Apply(ctx, historyMutations); err != nil {
			t.Fatalf("failed to insert WorkUnitStatusHistory into Spanner emulator: %v", err)
		}
	}
}
func TestFetchBatchQueueVelocity_Parameterized(t *testing.T) {
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
				newMockItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, ResourceTypeCPU, 10),
				newMockItem(102, "q1", -5*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, ResourceTypeCPU, 20),
				newMockItem(103, "q1", -2*time.Minute, essmsgpb.WorkUnitStatus_INITIALIZING, ResourceTypeCPU, 30),
			},
			want: []QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: ResourceTypeCPU},
			},
		},
		{
			name:       "LookbackCutoffWindowFiltering",
			queueNames: []string{"q1"},
			window:     30 * time.Minute,
			mockItems: []WorkUnitStatusHistoryItem{
				newMockItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, ResourceTypeCPU, 10),
				newMockItem(102, "q1", -45*time.Minute, essmsgpb.WorkUnitStatus_STARTING, ResourceTypeCPU, 20),
			},
			want: []QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: ResourceTypeCPU},
			},
		},
		{
			name:       "QueueNameFiltering",
			queueNames: []string{"q1"},
			window:     1 * time.Hour,
			mockItems: []WorkUnitStatusHistoryItem{
				newMockItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, ResourceTypeCPU, 10),
				newMockItem(201, "q2", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, ResourceTypeCPU, 20),
			},
			want: []QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: ResourceTypeCPU},
			},
		},
		{
			name:       "MultiResourceAggregation",
			queueNames: []string{"q1"},
			window:     1 * time.Hour,
			mockItems: []WorkUnitStatusHistoryItem{
				newMockItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, ResourceTypeCPU, 10),
				newMockItem(102, "q1", -8*time.Minute, essmsgpb.WorkUnitStatus_STARTING, ResourceTypeCPU, 20),
				newMockItem(103, "q1", -5*time.Minute, essmsgpb.WorkUnitStatus_RUNNING, "GPU", 4),
			},
			want: []QueueMetricRow{
				{QueueName: "q1", Count: 3, TotalUnits: 30, ResourceType: ResourceTypeCPU},
				{QueueName: "q1", Count: 3, TotalUnits: 4, ResourceType: "GPU"},
			},
		},
		{
			name:       "EmptyQueueNames",
			queueNames: []string{},
			window:     1 * time.Hour,
			mockItems: []WorkUnitStatusHistoryItem{
				newMockItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, ResourceTypeCPU, 10),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, essClient := emulator.Setup(ctx, t)
			insertMockItems(ctx, t, essClient, tt.mockItems)

			got, err := FetchBatchQueueVelocity(ctx, essClient, tt.queueNames, tt.window)
			if err != nil {
				t.Fatalf("FetchBatchQueueVelocity failed: %v", err)
			}
			compareQueueMetricRows(t, got, tt.want)
		})
	}
}

func TestFetchBatchPendingQueueMetrics_Parameterized(t *testing.T) {
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
				newMockItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, ResourceTypeCPU, 10),
				newMockItem(102, "q1", -5*time.Minute, essmsgpb.WorkUnitStatus_STARTING, ResourceTypeCPU, 20),
			},
			want: []QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: ResourceTypeCPU},
			},
		},
		{
			name:       "LatestStateTransition",
			queueNames: []string{"q1"},
			mockItems: []WorkUnitStatusHistoryItem{
				newMockItem(101, "q1", -20*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, ResourceTypeCPU, 10),
				newMockItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_RUNNING, ResourceTypeCPU, 10),
			},
			want: nil,
		},
		{
			name:       "QueueNameFiltering",
			queueNames: []string{"q1"},
			mockItems: []WorkUnitStatusHistoryItem{
				newMockItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, ResourceTypeCPU, 10),
				newMockItem(201, "q2", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, ResourceTypeCPU, 20),
			},
			want: []QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: ResourceTypeCPU},
			},
		},
		{
			name:       "MultiResourceAggregation",
			queueNames: []string{"q1"},
			mockItems: []WorkUnitStatusHistoryItem{
				newMockItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, ResourceTypeCPU, 10),
				newMockItem(102, "q1", -8*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, ResourceTypeCPU, 20),
				newMockItem(103, "q1", -5*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, "GPU", 4),
			},
			want: []QueueMetricRow{
				{QueueName: "q1", Count: 3, TotalUnits: 30, ResourceType: ResourceTypeCPU},
				{QueueName: "q1", Count: 3, TotalUnits: 4, ResourceType: "GPU"},
			},
		},
		{
			name:       "EmptyQueueNames",
			queueNames: []string{},
			mockItems: []WorkUnitStatusHistoryItem{
				newMockItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, ResourceTypeCPU, 10),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, essClient := emulator.Setup(ctx, t)
			insertMockItems(ctx, t, essClient, tt.mockItems)

			got, err := FetchBatchPendingQueueMetrics(ctx, essClient, tt.queueNames)
			if err != nil {
				t.Fatalf("FetchBatchPendingQueueMetrics failed: %v", err)
			}
			compareQueueMetricRows(t, got, tt.want)
		})
	}
}
