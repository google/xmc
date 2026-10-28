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

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner/emulator"
)

func TestQueueMetricRow(t *testing.T) {
	row := storage.QueueMetricRow{
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

// Queue velocity and pending metric results are covered by the backend-agnostic
// storagetest.RunStatusHistoryTests suite in conformance_test.go.

func TestTxRepository_SaveWorkUnitStatusHistory(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	repo := NewRepository(essClient)

	running := essmsgpb.WorkUnitStatus_RUNNING
	eid := int64(1)

	t.Run("nil context propagates error", func(t *testing.T) {
		if err := repo.WithTx(nil).SaveWorkUnitStatusHistory(nil, 1, 1, &eid, "q1", nil, &running, map[string]int64{"": 0}, time.Now()); err == nil {
			t.Fatalf("SaveWorkUnitStatusHistory() with nil ctx succeeded, want error")
		}
	})

	t.Run("nil transaction propagates error", func(t *testing.T) {
		if err := repo.WithTx(nil).SaveWorkUnitStatusHistory(ctx, 1, 1, &eid, "q1", nil, &running, map[string]int64{"": 0}, time.Now()); err == nil {
			t.Fatalf("SaveWorkUnitStatusHistory() with nil tx succeeded, want error")
		}
	})

	t.Run("canceled context propagates error", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()
		if err := repo.WithTx(nil).SaveWorkUnitStatusHistory(canceledCtx, 1, 1, &eid, "q1", nil, &running, map[string]int64{"": 0}, time.Now()); err == nil {
			t.Fatalf("SaveWorkUnitStatusHistory() with canceled ctx succeeded, want error")
		}
	})
}
