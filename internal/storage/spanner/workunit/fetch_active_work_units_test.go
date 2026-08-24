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
	"encoding/base64"
	"errors"
	"testing"

	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/workunit"
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

func TestFetchActiveWorkUnitsByQueues_InvalidPageToken(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	invalidTokens := []string{
		"invalid-token-!!!",
		base64.URLEncoding.EncodeToString([]byte("-5")),
		base64.URLEncoding.EncodeToString([]byte("not-a-number")),
	}

	for _, token := range invalidTokens {
		t.Run("token="+token, func(t *testing.T) {
			_, err := storage.FetchActiveWorkUnitsByQueues(ctx, []string{"gpu-queue"}, 10, token)
			if err == nil {
				t.Fatalf("FetchActiveWorkUnitsByQueues() expected error for token %q, got nil", token)
			}
			if !errors.Is(err, workunit.ErrInvalidPageToken) {
				t.Errorf("FetchActiveWorkUnitsByQueues() error = %v, want errors.Is(ErrInvalidPageToken)", err)
			}
		})
	}
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
	for i := int64(1); i <= 5; i++ {
		_ = spanner.CreateBlankWorkUnit(ctx, t, storage, xid, i)
	}

	// Page 1 with pageSize = 2
	page1, err := storage.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 2, "")
	if err != nil {
		t.Fatalf("Page 1 FetchActiveWorkUnitsByQueues failed: %v", err)
	}
	if len(page1.WorkUnits) != 2 {
		t.Errorf("Page 1 work units count = %d, want 2", len(page1.WorkUnits))
	}
	if page1.NextPageToken == "" {
		t.Errorf("Page 1 NextPageToken is empty, want non-empty token")
	}
	if page1.TotalQueueSize != 5 {
		t.Errorf("Page 1 TotalQueueSize = %d, want 5", page1.TotalQueueSize)
	}

	// Page 2 with pageSize = 2
	page2, err := storage.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 2, page1.NextPageToken)
	if err != nil {
		t.Fatalf("Page 2 FetchActiveWorkUnitsByQueues failed: %v", err)
	}
	if len(page2.WorkUnits) != 2 {
		t.Errorf("Page 2 work units count = %d, want 2", len(page2.WorkUnits))
	}
	if page2.NextPageToken == "" {
		t.Errorf("Page 2 NextPageToken is empty, want non-empty token")
	}
	if page2.TotalQueueSize != 5 {
		t.Errorf("Page 2 TotalQueueSize = %d, want 5", page2.TotalQueueSize)
	}

	// Page 3 with pageSize = 2 (should return last item)
	page3, err := storage.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 2, page2.NextPageToken)
	if err != nil {
		t.Fatalf("Page 3 FetchActiveWorkUnitsByQueues failed: %v", err)
	}
	if len(page3.WorkUnits) != 1 {
		t.Errorf("Page 3 work units count = %d, want 1", len(page3.WorkUnits))
	}
	if page3.NextPageToken != "" {
		t.Errorf("Page 3 NextPageToken = %q, want empty", page3.NextPageToken)
	}
	if page3.TotalQueueSize != 5 {
		t.Errorf("Page 3 TotalQueueSize = %d, want 5", page3.TotalQueueSize)
	}

	// Test boundary conditions for pageSize (<= 0 uses default 100, > 1000 clamped to 1000)
	pageDefault, err := storage.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 0, "")
	if err != nil {
		t.Fatalf("FetchActiveWorkUnitsByQueues with pageSize 0 failed: %v", err)
	}
	if len(pageDefault.WorkUnits) != 5 {
		t.Errorf("pageSize 0 returned count = %d, want 5", len(pageDefault.WorkUnits))
	}
}
