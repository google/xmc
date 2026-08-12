// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package workunit_test

import (
	"context"
	"testing"

	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/storage/spanner/emulator/emulator"
)

func TestFetchActiveWorkUnitsByQueues_Uninitialized(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	t.Run("canceled context error", func(t *testing.T) {
		repo := &spanner.Storage{}
		_, err := repo.FetchActiveWorkUnitsByQueues(canceledCtx, []string{"gpu-queue"}, 10, "")
		if err == nil {
			t.Fatalf("FetchActiveWorkUnitsByQueues() expected error for canceled context, got nil")
		}
	})

	t.Run("nil client guard returns error", func(t *testing.T) {
		repo := &spanner.Storage{}
		_, err := repo.FetchActiveWorkUnitsByQueues(context.Background(), []string{"gpu-queue"}, 10, "")
		if err == nil {
			t.Fatalf("FetchActiveWorkUnitsByQueues() expected error for nil spanner client, got nil")
		}
	})

	t.Run("empty queue names returns empty result", func(t *testing.T) {
		ctx := context.Background()
		_, essClient := emulator.Setup(ctx, t)
		storage, err := spanner.NewStorage(essClient)
		if err != nil {
			t.Fatalf("spanner.NewStorage failed: %v", err)
		}
		res, err := storage.FetchActiveWorkUnitsByQueues(ctx, []string{}, 10, "")
		if err != nil {
			t.Fatalf("FetchActiveWorkUnitsByQueues() unexpected error: %v", err)
		}
		if len(res.WorkUnits) != 0 {
			t.Errorf("FetchActiveWorkUnitsByQueues() count = %d, want 0", len(res.WorkUnits))
		}
	})
}

func TestFetchActiveWorkUnitsByQueues_EmulatorIntegration(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	xid := int64(100)
	_ = spanner.CreateBlankExperiment(ctx, t, storage, xid)
	_ = spanner.CreateBlankWorkUnit(ctx, t, storage, xid, int64(1))

	res, err := storage.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 10, "")
	if err != nil {
		t.Fatalf("FetchActiveWorkUnitsByQueues failed: %v", err)
	}
	if res == nil {
		t.Fatalf("FetchActiveWorkUnitsByQueues returned nil result")
	}
}
