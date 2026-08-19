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
	"testing"
	"time"
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
