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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func TestRepository_AggregateActiveResourcesByAuthor_GuardsAndEmpty(t *testing.T) {
	ctx := t.Context()
	if _, err := NewRepository(nil).AggregateActiveResourcesByAuthor(ctx, []string{"q"}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("aggregate(nil pool) code = %v, want FailedPrecondition", status.Code(err))
	}

	_, essPool := postgrestest.Setup(ctx, t)
	got, err := NewRepository(essPool).AggregateActiveResourcesByAuthor(ctx, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("aggregate(nil queues) = (%v, %v), want (empty, nil)", got, err)
	}
}

func TestRepository_AggregateActiveResourcesByAuthor_AllPages(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)
	postgrestest.SeedExperimentWithCreator(ctx, t, essPool, 701, "alice@example.com")
	postgrestest.SeedExperimentWithCreator(ctx, t, essPool, 702, "bob@example.com")
	insertActiveResourceWorkUnits(t, essPool, repo, storagetest.ActiveResourceSpecs(701, 702))

	queues := []string{storagetest.ActiveResourceQueue}
	pageOne, err := repo.FetchActiveWorkUnitsByQueues(ctx, queues, 2, "")
	if err != nil {
		t.Fatalf("FetchActiveWorkUnitsByQueues failed: %v", err)
	}
	gotAllocations, err := repo.AggregateActiveResourcesByAuthor(ctx, queues)
	if err != nil {
		t.Fatalf("AggregateActiveResourcesByAuthor failed: %v", err)
	}
	if len(pageOne.WorkUnits) != 2 || pageOne.TotalQueueSize != storagetest.ActiveResourceSpecsActiveCount || pageOne.NextPageToken == "" {
		t.Fatalf("pageOne = (len=%d, total=%d, next=%q), want (2, %d, non-empty)", len(pageOne.WorkUnits), pageOne.TotalQueueSize, pageOne.NextPageToken, storagetest.ActiveResourceSpecsActiveCount)
	}
	if diff := cmp.Diff(storagetest.WantActiveResourceAllocations(), gotAllocations, storagetest.AuthorAllocationOrder); diff != "" {
		t.Errorf("AggregateActiveResourcesByAuthor() mismatch (-want +got):\n%s", diff)
	}
}
