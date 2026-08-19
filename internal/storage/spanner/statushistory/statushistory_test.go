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
	"testing"
	"time"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestQueueMetricRow(t *testing.T) {
	row := QueueMetricRow{
		QueueName:    "cpu-queue",
		Count:        12,
		TotalUnits:   96000,
		ResourceType: ResourceTypeCPU,
	}
	window := 1 * time.Hour
	rate := float64(row.Count) / window.Hours()

	if rate != 12.0 {
		t.Errorf("rate = %f, want 12.0", rate)
	}
}

func TestFetchBatchQueueMetrics_NilClient(t *testing.T) {
	repo := NewRepository(nil)
	batchVel, err := repo.FetchBatchQueueVelocity(nil, []string{"cpu-queue"}, 1*time.Hour)
	if err != nil {
		t.Fatalf("expected no error for nil client batch velocity, got %v", err)
	}
	if len(batchVel) != 0 {
		t.Errorf("expected empty batch map for nil client, got %+v", batchVel)
	}

	batchPending, err := repo.FetchBatchPendingQueueMetrics(nil, []string{"cpu-queue"})
	if err != nil {
		t.Fatalf("expected no error for nil client batch pending, got %v", err)
	}
	if len(batchPending) != 0 {
		t.Errorf("expected empty batch map for nil client, got %+v", batchPending)
	}
}

func TestDefaultMockStatusHistoryItems(t *testing.T) {
	hasCPUQueueScheduling := false
	hasCPUQueueInitializing := false
	hasTPUQueueCPUResource := false

	for _, item := range DefaultMockStatusHistoryItems {
		if item.QueueName == "cq-gpu" {
			t.Errorf("found unwanted cq-gpu entry: %+v", item)
		}
		if item.QueueName == "cpu-queue" {
			if item.NewState == int64(essmsgpb.WorkUnitStatus_SCHEDULING) && item.ResourceType == ResourceTypeCPU {
				hasCPUQueueScheduling = true
			}
			if item.NewState == int64(essmsgpb.WorkUnitStatus_INITIALIZING) && item.ResourceType == ResourceTypeCPU {
				hasCPUQueueInitializing = true
			}
		}
		if item.QueueName == "tpu-queue" {
			if item.ResourceType == ResourceTypeCPU {
				hasTPUQueueCPUResource = true
			}
		}
	}

	if !hasCPUQueueScheduling {
		t.Errorf("DefaultMockStatusHistoryItems missing cpu-queue SCHEDULING record")
	}
	if !hasCPUQueueInitializing {
		t.Errorf("DefaultMockStatusHistoryItems missing cpu-queue INITIALIZING record")
	}
	if !hasTPUQueueCPUResource {
		t.Errorf("DefaultMockStatusHistoryItems missing tpu-queue CPU resource record")
	}
}
