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
	"time"

	"cloud.google.com/go/spanner"
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

// ResourceTypeCPU represents the standard CPU resource identifier string.
const ResourceTypeCPU = "CPU"

// DefaultMockStatusHistoryItems provides initial unnest mock records.
var DefaultMockStatusHistoryItems = []WorkUnitStatusHistoryItem{
	{
		BitReversedXid: 101,
		WorkUnitId:     1,
		HistoryId:      1,
		QueueName:      "cq-gpu",
		TransitionTime: time.Now().Add(-30 * time.Minute),
		NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
		PreviousState:  int64(essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED),
		ResourceType:   "nvidia-tesla-h100",
		ResourceUnits:  96,
	},
	{
		BitReversedXid: 102,
		WorkUnitId:     2,
		HistoryId:      2,
		QueueName:      "cq-gpu",
		TransitionTime: time.Now().Add(-10 * time.Minute),
		NewState:       int64(essmsgpb.WorkUnitStatus_SCHEDULING),
		PreviousState:  int64(essmsgpb.WorkUnitStatus_INITIALIZING),
		ResourceType:   "nvidia-tesla-h100",
		ResourceUnits:  40,
	},
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
		PreviousState:  int64(essmsgpb.WorkUnitStatus_INITIALIZING),
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
		ResourceType:   "v5p",
		ResourceUnits:  16,
	},
	{
		BitReversedXid: 106,
		WorkUnitId:     6,
		HistoryId:      6,
		QueueName:      "tpu-queue",
		TransitionTime: time.Now().Add(-8 * time.Minute),
		NewState:       int64(essmsgpb.WorkUnitStatus_INITIALIZING),
		PreviousState:  int64(essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED),
		ResourceType:   "v5p",
		ResourceUnits:  32,
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

// FetchBatchQueueVelocity returns in-memory mock queue metric rows.
func FetchBatchQueueVelocity(ctx context.Context, client *spanner.Client, queueNames []string, window time.Duration) ([]QueueMetricRow, error) {
	if len(queueNames) == 0 {
		return nil, nil
	}
	return []QueueMetricRow{
		{QueueName: "cpu-queue", Count: 10, TotalUnits: 25000, ResourceType: ResourceTypeCPU},
		{QueueName: "tpu-queue", Count: 5, TotalUnits: 20, ResourceType: "TPU v5p"},
		{QueueName: "cpu-cluster-queue", Count: 10, TotalUnits: 25000, ResourceType: ResourceTypeCPU},
		{QueueName: "gpu-cluster-queue", Count: 12, TotalUnits: 96, ResourceType: "nvidia-tesla-h100"},
		{QueueName: "tpu-cluster-queue", Count: 5, TotalUnits: 20, ResourceType: "TPU v5p"},
	}, nil
}

// FetchBatchPendingQueueMetrics returns in-memory mock queue metric rows.
func FetchBatchPendingQueueMetrics(ctx context.Context, client *spanner.Client, queueNames []string) ([]QueueMetricRow, error) {
	if len(queueNames) == 0 {
		return nil, nil
	}
	return []QueueMetricRow{
		{QueueName: "cpu-queue", Count: 3, TotalUnits: 6000, ResourceType: ResourceTypeCPU},
		{QueueName: "tpu-queue", Count: 1, TotalUnits: 4, ResourceType: "TPU v5p"},
		{QueueName: "cpu-cluster-queue", Count: 3, TotalUnits: 6000, ResourceType: ResourceTypeCPU},
		{QueueName: "gpu-cluster-queue", Count: 5, TotalUnits: 40, ResourceType: "nvidia-tesla-h100"},
		{QueueName: "tpu-cluster-queue", Count: 1, TotalUnits: 4, ResourceType: "TPU v5p"},
	}, nil
}
