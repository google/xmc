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
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	storagetest "xmanager_cloud/internal/storage/testing"
)

func newEmulatorStorage(ctx context.Context, t *testing.T) *spanner.Storage {
	t.Helper()
	_, essClient := emulator.Setup(ctx, t)
	store, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}
	return store
}

func seedExperimentWithCreator(ctx context.Context, t *testing.T, store *spanner.Storage, xid int64, creator string) {
	t.Helper()
	exp := spanner.BlankExperimentProto(xid)
	exp.Creator = creator
	_ = spanner.CreateAndStoreExperiment(ctx, t, store, exp)
}

func seedActiveResourceWorkUnits(ctx context.Context, t *testing.T, store *spanner.Storage, specs []storagetest.ActiveResourceSpec) {
	t.Helper()
	wus := make([]*essmsgpb.WorkUnit, 0, len(specs))
	for _, spec := range specs {
		wu := spanner.BlankWorkUnitProto(spec.XID, spec.WID)
		wu.CurrentWorkUnitExecution.Status.State = spec.State
		wu.Executable = spec.Executable()
		wus = append(wus, wu)
	}
	insertWorkUnitsAt(ctx, t, store, time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC), wus)
}

func aggregateInSnapshot(ctx context.Context, store *spanner.Storage, queues []string) ([]storage.AuthorResourceAllocation, error) {
	var got []storage.AuthorResourceAllocation
	err := store.WithReadOnlyWorkUnitSnapshot(ctx, func(txCtx context.Context, repo storage.WorkUnitSnapshotRepository) error {
		var innerErr error
		got, innerErr = repo.AggregateActiveResourcesByAuthor(txCtx, queues)
		return innerErr
	})
	return got, err
}

func TestAggregateActiveResourcesByAuthor_GuardsAndEmpty(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := aggregateInSnapshot(canceledCtx, &spanner.Storage{}, []string{"q"}); !errors.Is(err, context.Canceled) {
		t.Errorf("aggregate(canceled) = %v, want context.Canceled", err)
	}
	if _, err := aggregateInSnapshot(t.Context(), &spanner.Storage{}, []string{"q"}); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("aggregate(nil client) = %v, want ErrFailedPrecondition", err)
	}

	ctx := t.Context()
	store := newEmulatorStorage(ctx, t)
	got, err := aggregateInSnapshot(ctx, store, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("aggregate(nil queues) = (%v, %v), want (empty, nil)", got, err)
	}
}

func TestAggregateActiveResourcesByAuthor_MultiPageSnapshotAndNormalization(t *testing.T) {
	ctx := t.Context()
	store := newEmulatorStorage(ctx, t)
	seedExperimentWithCreator(ctx, t, store, 201, "alice@example.com")
	seedExperimentWithCreator(ctx, t, store, 202, "bob@example.com")
	seedActiveResourceWorkUnits(ctx, t, store, storagetest.ActiveResourceSpecs(201, 202))

	queues := []string{storagetest.ActiveResourceQueue}
	var pageOne *storage.ActiveWorkUnitsResult
	var gotAllocations []storage.AuthorResourceAllocation
	err := store.WithReadOnlyWorkUnitSnapshot(ctx, func(txCtx context.Context, repo storage.WorkUnitSnapshotRepository) error {
		var innerErr error
		if pageOne, innerErr = repo.FetchActiveWorkUnitsByQueues(txCtx, queues, 2, ""); innerErr != nil {
			return innerErr
		}
		gotAllocations, innerErr = repo.AggregateActiveResourcesByAuthor(txCtx, queues)
		return innerErr
	})
	if err != nil {
		t.Fatalf("WithReadOnlyWorkUnitSnapshot failed: %v", err)
	}
	if len(pageOne.WorkUnits) != 2 || pageOne.TotalQueueSize != storagetest.ActiveResourceSpecsActiveCount || pageOne.NextPageToken == "" {
		t.Fatalf("pageOne = (len=%d, total=%d, next=%q), want (2, %d, non-empty)", len(pageOne.WorkUnits), pageOne.TotalQueueSize, pageOne.NextPageToken, storagetest.ActiveResourceSpecsActiveCount)
	}
	if diff := cmp.Diff(storagetest.WantActiveResourceAllocations(), gotAllocations, storagetest.AuthorAllocationOrder); diff != "" {
		t.Errorf("AggregateActiveResourcesByAuthor() mismatch (-want +got):\n%s", diff)
	}
}
