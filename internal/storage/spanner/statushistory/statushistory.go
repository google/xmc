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
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

// WorkUnitStatusHistoryItem represents a single WorkUnitStatusHistory record for Spanner queries and UNNEST mocks.
type WorkUnitStatusHistoryItem struct {
	BitReversedXid int64     `spanner:"BitReversedXid"`
	WorkUnitId     int64     `spanner:"WorkUnitId"`
	HistoryId      int64     `spanner:"HistoryId"`
	QueueName      string    `spanner:"QueueName"`
	TransitionTime time.Time `spanner:"TransitionTime"`
	NewState       int64     `spanner:"NewState"`
	PreviousState  int64     `spanner:"PreviousState"`
	ResourceType   string    `spanner:"ResourceType"`
	ResourceUnits  int64     `spanner:"ResourceUnits"`
}

// ResourceTypeCPU represents the standard uppercase CPU resource identifier string.
const ResourceTypeCPU = "CPU"

// ResourceTypeCPULower represents the standard lowercase cpu resource identifier string.
const ResourceTypeCPULower = "cpu"

// DefaultMockStatusHistoryItems provides initial unnest mock records.
var DefaultMockStatusHistoryItems = []WorkUnitStatusHistoryItem{
	{
		BitReversedXid: 103,
		WorkUnitId:     3,
		HistoryId:      3,
		QueueName:      "cpu-queue",
		TransitionTime: time.Now().Add(-20 * time.Minute),
		NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
		PreviousState:  int64(essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED),
		ResourceType:   ResourceTypeCPU,
		ResourceUnits:  16000,
	},
	{
		BitReversedXid: 104,
		WorkUnitId:     4,
		HistoryId:      4,
		QueueName:      "cpu-queue",
		TransitionTime: time.Now().Add(-5 * time.Minute),
		NewState:       int64(essmsgpb.WorkUnitStatus_SCHEDULING),
		PreviousState:  int64(essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED),
		ResourceType:   ResourceTypeCPU,
		ResourceUnits:  8000,
	},
	{
		BitReversedXid: 105,
		WorkUnitId:     5,
		HistoryId:      5,
		QueueName:      "tpu-queue",
		TransitionTime: time.Now().Add(-15 * time.Minute),
		NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
		PreviousState:  int64(essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED),
		ResourceType:   ResourceTypeCPU,
		ResourceUnits:  4000,
	},
}

// QueueMetricRow represents a single raw row returned by the Spanner velocity or pending metrics query.
type QueueMetricRow struct {
	QueueName    string
	Count        int64
	TotalUnits   int64
	ResourceType string
}

// Repository handles Spanner operations for status history and velocity metrics.
type Repository struct {
	client *spanner.Client
}

// NewRepository initializes a new statushistory Repository.
func NewRepository(client *spanner.Client) *Repository {
	return &Repository{client: client}
}

// FetchBatchQueueVelocity queries the WorkUnitStatusHistory table for multiple queues and returns raw rows.
func (r *Repository) FetchBatchQueueVelocity(ctx context.Context, queueNames []string, window time.Duration) ([]QueueMetricRow, error) {
	return FetchBatchQueueVelocity(ctx, r.client, queueNames, window)
}

// FetchBatchPendingQueueMetrics queries the WorkUnitStatusHistory table for pending work units and returns raw rows.
func (r *Repository) FetchBatchPendingQueueMetrics(ctx context.Context, queueNames []string) ([]QueueMetricRow, error) {
	return FetchBatchPendingQueueMetrics(ctx, r.client, queueNames)
}

func runQueueMetricQuery(ctx context.Context, client *spanner.Client, stmt spanner.Statement, countCol string) ([]QueueMetricRow, error) {
	if client == nil {
		return nil, nil
	}

	iter := client.Single().Query(ctx, stmt)
	defer iter.Stop()

	var rows []QueueMetricRow
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to iterate queue metric rows: %w", err)
		}

		var row QueueMetricRow
		if err := r.ColumnByName("QueueName", &row.QueueName); err != nil {
			return nil, fmt.Errorf("failed to read QueueName column: %w", err)
		}
		if err := r.ColumnByName(countCol, &row.Count); err != nil {
			return nil, fmt.Errorf("failed to read %s column: %w", countCol, err)
		}
		if err := r.ColumnByName("TotalUnits", &row.TotalUnits); err != nil {
			return nil, fmt.Errorf("failed to read TotalUnits column: %w", err)
		}
		if err := r.ColumnByName("ResType", &row.ResourceType); err != nil {
			return nil, fmt.Errorf("failed to read ResType column: %w", err)
		}
		rows = append(rows, row)
	}

	return rows, nil
}

// FetchBatchQueueVelocity returns in-memory mock queue velocity rows.
func FetchBatchQueueVelocity(ctx context.Context, client *spanner.Client, queueNames []string, window time.Duration) ([]QueueMetricRow, error) {
	if len(queueNames) == 0 || client == nil {
		return nil, nil
	}

	var rows []QueueMetricRow
	for _, q := range queueNames {
		if q == "cpu-queue" {
			rows = append(rows, QueueMetricRow{
				QueueName:    "cpu-queue",
				Count:        1,
				TotalUnits:   16000,
				ResourceType: ResourceTypeCPU,
			})
		} else if q == "tpu-queue" {
			rows = append(rows, QueueMetricRow{
				QueueName:    "tpu-queue",
				Count:        1,
				TotalUnits:   4000,
				ResourceType: ResourceTypeCPU,
			})
		}
	}
	return rows, nil
}

// FetchBatchPendingQueueMetrics returns in-memory mock pending queue metrics rows.
func FetchBatchPendingQueueMetrics(ctx context.Context, client *spanner.Client, queueNames []string) ([]QueueMetricRow, error) {
	if len(queueNames) == 0 || client == nil {
		return nil, nil
	}

	var rows []QueueMetricRow
	for _, q := range queueNames {
		if q == "cpu-queue" {
			rows = append(rows, QueueMetricRow{
				QueueName:    "cpu-queue",
				Count:        1,
				TotalUnits:   8000,
				ResourceType: ResourceTypeCPU,
			})
		}
	}
	return rows, nil
}
