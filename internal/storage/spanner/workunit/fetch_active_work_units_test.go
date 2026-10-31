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
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
)

func TestFetchActiveWorkUnitsByQueues_Uninitialized(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	t.Run("canceled context error", func(t *testing.T) {
		repo := &spanner.Storage{}
		_, err := repo.FetchActiveWorkUnitsByQueues(canceledCtx, []string{"gpu-queue"}, 10, "", storage.ActiveWorkUnitQuery{})
		if err == nil {
			t.Fatalf("FetchActiveWorkUnitsByQueues() expected error for canceled context, got nil")
		}
	})

	t.Run("nil client guard returns error", func(t *testing.T) {
		repo := &spanner.Storage{}
		_, err := repo.FetchActiveWorkUnitsByQueues(context.Background(), []string{"gpu-queue"}, 10, "", storage.ActiveWorkUnitQuery{})
		if err == nil {
			t.Fatalf("FetchActiveWorkUnitsByQueues() expected error for nil spanner client, got nil")
		}
	})

	t.Run("empty queue names returns empty result", func(t *testing.T) {
		ctx := context.Background()
		_, essClient := emulator.Setup(ctx, t)
		store, err := spanner.NewStorage(essClient)
		if err != nil {
			t.Fatalf("spanner.NewStorage failed: %v", err)
		}
		res, err := store.FetchActiveWorkUnitsByQueues(ctx, []string{}, 10, "", storage.ActiveWorkUnitQuery{})
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
	store, err := spanner.NewStorage(essClient)
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
			_, err := store.FetchActiveWorkUnitsByQueues(ctx, []string{"gpu-queue"}, 10, token, storage.ActiveWorkUnitQuery{})
			if err == nil {
				t.Fatalf("FetchActiveWorkUnitsByQueues() expected error for token %q, got nil", token)
			}
			if !errors.Is(err, storage.ErrInvalidPageToken) {
				t.Errorf("FetchActiveWorkUnitsByQueues() error = %v, want errors.Is(ErrInvalidPageToken)", err)
			}
		})
	}
}

func TestFetchActiveWorkUnitsByQueues_EmulatorIntegration(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	store, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	xid := int64(100)
	_ = spanner.CreateBlankExperiment(ctx, t, store, xid)
	for i := int64(1); i <= 5; i++ {
		_ = spanner.CreateBlankWorkUnit(ctx, t, store, xid, i)
	}

	type pageExpectation struct {
		wantCount     int
		wantHasNext   bool
		wantTotalSize int64
	}

	pages := []pageExpectation{
		{wantCount: 2, wantHasNext: true, wantTotalSize: 5},
		{wantCount: 2, wantHasNext: true, wantTotalSize: 5},
		{wantCount: 1, wantHasNext: false, wantTotalSize: 5},
	}

	token := ""
	for i, page := range pages {
		res, err := store.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 2, token, storage.ActiveWorkUnitQuery{})
		if err != nil {
			t.Fatalf("Page %d FetchActiveWorkUnitsByQueues failed: %v", i+1, err)
		}
		if len(res.WorkUnits) != page.wantCount {
			t.Errorf("Page %d work units count = %d, want %d", i+1, len(res.WorkUnits), page.wantCount)
		}
		if (res.NextPageToken != "") != page.wantHasNext {
			t.Errorf("Page %d NextPageToken = %q, wantHasNext = %t", i+1, res.NextPageToken, page.wantHasNext)
		}
		if res.TotalQueueSize != page.wantTotalSize {
			t.Errorf("Page %d TotalQueueSize = %d, want %d", i+1, res.TotalQueueSize, page.wantTotalSize)
		}
		token = res.NextPageToken
	}

	// Test boundary conditions for pageSize (<= 0 uses default 100, > 1000 clamped to 1000)
	pageDefault, err := store.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 0, "", storage.ActiveWorkUnitQuery{})
	if err != nil {
		t.Fatalf("FetchActiveWorkUnitsByQueues with pageSize 0 failed: %v", err)
	}
	if len(pageDefault.WorkUnits) != 5 {
		t.Errorf("pageSize 0 returned count = %d, want 5", len(pageDefault.WorkUnits))
	}
	if len(pageDefault.WorkUnits) == 0 {
		t.Fatalf("FetchActiveWorkUnitsByQueues returned empty work units, expected > 0")
	}
	if got, want := pageDefault.WorkUnits[0].Author, "test@example.com"; got != want {
		t.Errorf("pageDefault.WorkUnits[0].Author = %q, want %q", got, want)
	}
	if got, want := pageDefault.WorkUnits[0].ExperimentTitle, "test-title"; got != want {
		t.Errorf("pageDefault.WorkUnits[0].ExperimentTitle = %q, want %q", got, want)
	}
}

func TestFetchActiveWorkUnitsByQueues_CrossExperimentXidTieBreaker(t *testing.T) {
	ctx := t.Context()
	_, essClient := emulator.Setup(ctx, t)
	store, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	// ReverseBits orders [101, 104, 102], whereas logical Xid ASC orders [101, 102, 104].
	seedTiedActiveWorkUnits(ctx, t, store, []int64{104, 102, 101}, "tie-queue")

	var gotNames []string
	token := ""
	for i := range 3 {
		res, err := store.FetchActiveWorkUnitsByQueues(ctx, []string{"tie-queue"}, 1, token, storage.ActiveWorkUnitQuery{})
		if err != nil {
			t.Fatalf("page %d FetchActiveWorkUnitsByQueues failed: %v", i+1, err)
		}
		for _, item := range res.WorkUnits {
			gotNames = append(gotNames, item.WorkUnit.GetName())
		}
		token = res.NextPageToken
	}
	wantNames := []string{"experiments/101/workUnits/1", "experiments/102/workUnits/1", "experiments/104/workUnits/1"}
	if diff := cmp.Diff(wantNames, gotNames); diff != "" {
		t.Errorf("cross-experiment Xid tie-breaker mismatch (-want +got):\n%s", diff)
	}
}

func seedTiedActiveWorkUnits(ctx context.Context, t *testing.T, store *spanner.Storage, xids []int64, queueName string) {
	t.Helper()
	fixedTS := timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	for _, xid := range xids {
		_ = spanner.CreateBlankExperiment(ctx, t, store, xid)
	}
	err := store.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		for _, xid := range xids {
			wu := spanner.BlankWorkUnitProto(xid, 1)
			wu.CreateTime = fixedTS
			wu.UpdateTime = fixedTS
			wu.Executable.SchedulingConstraints.QueueName = queueName
			wu.CurrentWorkUnitExecution.CreateTime = fixedTS
			wu.CurrentWorkUnitExecution.UpdateTime = fixedTS
			wu.CurrentWorkUnitExecution.Status.State = essmsgpb.WorkUnitStatus_RUNNING
			if err := tx.WorkUnits.InsertWorkUnit(ctx, wu); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seedTiedActiveWorkUnits RunInTransaction failed: %v", err)
	}
}

func TestActiveQueueOrderingAcrossPages(t *testing.T) {
	ctx := context.Background()
	_, client := emulator.Setup(ctx, t)
	repo, err := spanner.NewStorage(client)
	if err != nil {
		t.Fatal(err)
	}
	for _, xid := range []int64{100, 200} {
		exp := spanner.BlankExperimentProto(xid)
		exp.Creator = "z@example.com"
		if xid == 200 {
			exp.Creator = "a@example.com"
		}
		spanner.CreateAndStoreExperiment(ctx, t, repo, exp)
		for i, state := range []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_SCHEDULING, essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.WorkUnitStatus_INITIALIZING} {
			wu := spanner.BlankWorkUnitProto(xid, int64(i+1))
			wu.Priority = essmsgpb.Priority(i + 1)
			wu.CurrentWorkUnitExecution.Status.State = state
			spanner.CreateAndStoreWorkUnit(ctx, t, repo, wu)
		}
	}

	t.Run("combined filter and count", func(t *testing.T) {
		result, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 1, "", storage.ActiveWorkUnitQuery{Filter: storage.ActiveWorkUnitFilter{Author: "a@example.com", HasAuthor: true, Status: "RUNNING"}})
		if err != nil {
			t.Fatal(err)
		}
		if result.TotalQueueSize != 1 || len(result.WorkUnits) != 1 || result.NextPageToken != "" {
			t.Fatalf("filtered result = %+v", result)
		}
		if got := result.WorkUnits[0].WorkUnit.GetName(); got != "experiments/200/workUnits/2" {
			t.Errorf("work unit = %s", got)
		}
	})
	t.Run("filtered pagination", func(t *testing.T) {
		token := ""
		for page := 0; page < 3; page++ {
			result, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 1, token, storage.ActiveWorkUnitQuery{OrderBy: "priority desc", Filter: storage.ActiveWorkUnitFilter{Author: "a@example.com", HasAuthor: true}})
			if err != nil {
				t.Fatal(err)
			}
			if result.TotalQueueSize != 3 || len(result.WorkUnits) != 1 {
				t.Fatalf("filtered page = %+v", result)
			}
			token = result.NextPageToken
			if (token != "") != (page < 2) {
				t.Fatalf("page %d token = %q", page, token)
			}
		}
	})
	t.Run("unmatched author", func(t *testing.T) {
		result, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 1, "", storage.ActiveWorkUnitQuery{Filter: storage.ActiveWorkUnitFilter{Author: "missing@example.com", HasAuthor: true}})
		if err != nil {
			t.Fatal(err)
		}
		if result.TotalQueueSize != 0 || len(result.WorkUnits) != 0 {
			t.Fatalf("unmatched result = %+v", result)
		}
	})

	cases := []struct {
		order string
		want  []string
	}{
		{"author asc", []string{"experiments/200/workUnits/1", "experiments/200/workUnits/2", "experiments/200/workUnits/3", "experiments/100/workUnits/1", "experiments/100/workUnits/2", "experiments/100/workUnits/3"}},
		{"author desc", []string{"experiments/100/workUnits/1", "experiments/100/workUnits/2", "experiments/100/workUnits/3", "experiments/200/workUnits/1", "experiments/200/workUnits/2", "experiments/200/workUnits/3"}},
		{"priority asc", []string{"experiments/100/workUnits/1", "experiments/200/workUnits/1", "experiments/100/workUnits/2", "experiments/200/workUnits/2", "experiments/100/workUnits/3", "experiments/200/workUnits/3"}},
		{"priority desc", []string{"experiments/100/workUnits/3", "experiments/200/workUnits/3", "experiments/100/workUnits/2", "experiments/200/workUnits/2", "experiments/100/workUnits/1", "experiments/200/workUnits/1"}},
		{"status asc", []string{"experiments/100/workUnits/2", "experiments/200/workUnits/2", "experiments/100/workUnits/3", "experiments/200/workUnits/3", "experiments/100/workUnits/1", "experiments/200/workUnits/1"}},
		{"status desc", []string{"experiments/100/workUnits/1", "experiments/200/workUnits/1", "experiments/100/workUnits/3", "experiments/200/workUnits/3", "experiments/100/workUnits/2", "experiments/200/workUnits/2"}},
	}
	for _, tt := range cases {
		t.Run(tt.order, func(t *testing.T) {
			var names []string
			token := ""
			for page := 0; page < 3; page++ {
				result, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 2, token, storage.ActiveWorkUnitQuery{OrderBy: tt.order})
				if err != nil {
					t.Fatal(err)
				}
				if result.TotalQueueSize != 6 {
					t.Fatalf("total = %d, want 6", result.TotalQueueSize)
				}
				for _, item := range result.WorkUnits {
					names = append(names, item.WorkUnit.GetName())
				}
				token = result.NextPageToken
				if page < 2 && token == "" {
					t.Fatal("missing next page")
				}
			}
			if token != "" {
				t.Fatal("unexpected final page token")
			}
			if !slices.Equal(names, tt.want) {
				t.Errorf("order = %v, want %v", names, tt.want)
			}
		})
	}
}
