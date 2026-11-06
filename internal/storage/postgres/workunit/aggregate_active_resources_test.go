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

package workunit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/storage"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	storagetest "xmanager_cloud/internal/storage/testing"
)

func insertActiveResourceWorkUnits(t *testing.T, pool *pgxpool.Pool, repo *Repository, specs []storagetest.ActiveResourceSpec) {
	t.Helper()
	for i, spec := range specs {
		wu := blankWorkUnitProto(spec.XID, spec.WID, baseTestTime.Add(time.Duration(i)*time.Second))
		wu.CurrentWorkUnitExecution.Status.State = spec.State
		wu.Executable = spec.Executable()
		mustInsertWorkUnit(t, pool, repo, wu)
	}
}

func aggregateInSnapshot(ctx context.Context, repo *Repository, queues []string) ([]storage.AuthorResourceAllocation, error) {
	var got []storage.AuthorResourceAllocation
	err := repo.WithReadOnlyWorkUnitSnapshot(ctx, func(txCtx context.Context, snap storage.WorkUnitSnapshotRepository) error {
		var innerErr error
		got, innerErr = snap.AggregateActiveResourcesByAuthor(txCtx, queues)
		return innerErr
	})
	return got, err
}

func TestRepository_AggregateActiveResourcesByAuthor_GuardsAndEmpty(t *testing.T) {
	ctx := t.Context()
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	nilPoolRepo := NewRepository(nil)
	if _, err := aggregateInSnapshot(canceledCtx, nilPoolRepo, []string{"q"}); !errors.Is(err, context.Canceled) {
		t.Errorf("aggregate(canceled) = %v, want context.Canceled", err)
	}
	if _, err := aggregateInSnapshot(ctx, nil, []string{"q"}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("aggregate(nil Repository) code = %v, want FailedPrecondition", status.Code(err))
	}
	if _, err := aggregateInSnapshot(ctx, nilPoolRepo, []string{"q"}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("aggregate(nil pool) code = %v, want FailedPrecondition", status.Code(err))
	}

	_, essPool := postgrestest.Setup(ctx, t)
	got, err := aggregateInSnapshot(ctx, NewRepository(essPool), nil)
	if err != nil || len(got) != 0 {
		t.Errorf("aggregate(nil queues) = (%v, %v), want (empty, nil)", got, err)
	}
}

func TestRepository_AggregateActiveResourcesByAuthor_MultiPageSnapshot(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)
	postgrestest.SeedExperimentWithCreator(ctx, t, essPool, 701, "alice@example.com")
	postgrestest.SeedExperimentWithCreator(ctx, t, essPool, 702, "bob@example.com")
	insertActiveResourceWorkUnits(t, essPool, repo, storagetest.ActiveResourceSpecs(701, 702))

	queues := []string{storagetest.ActiveResourceQueue}
	var pageOne *storage.ActiveWorkUnitsResult
	var snapshotAllocations []storage.AuthorResourceAllocation
	err := repo.WithReadOnlyWorkUnitSnapshot(ctx, func(txCtx context.Context, snap storage.WorkUnitSnapshotRepository) error {
		var innerErr error
		if pageOne, innerErr = snap.FetchActiveWorkUnitsByQueues(txCtx, queues, 2, ""); innerErr != nil {
			return innerErr
		}
		snapshotAllocations, innerErr = snap.AggregateActiveResourcesByAuthor(txCtx, queues)
		return innerErr
	})
	if err != nil {
		t.Fatalf("WithReadOnlyWorkUnitSnapshot failed: %v", err)
	}
	if len(pageOne.WorkUnits) != 2 || pageOne.TotalQueueSize != storagetest.ActiveResourceSpecsActiveCount || pageOne.NextPageToken == "" {
		t.Fatalf("pageOne = (len=%d, total=%d, next=%q), want (2, %d, non-empty)", len(pageOne.WorkUnits), pageOne.TotalQueueSize, pageOne.NextPageToken, storagetest.ActiveResourceSpecsActiveCount)
	}
	if diff := cmp.Diff(storagetest.WantActiveResourceAllocations(), snapshotAllocations, storagetest.AuthorAllocationOrder); diff != "" {
		t.Errorf("snapshot AggregateActiveResourcesByAuthor() mismatch (-want +got):\n%s", diff)
	}
}
