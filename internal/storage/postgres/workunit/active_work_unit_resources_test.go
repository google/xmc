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
	"google.golang.org/protobuf/testing/protocmp"
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

func TestRepository_FetchActiveWorkUnitResources(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)
	postgrestest.SeedExperimentWithCreator(ctx, t, essPool, 701, "alice@example.com")
	postgrestest.SeedExperimentWithCreator(ctx, t, essPool, 702, "bob@example.com")
	specs := storagetest.ActiveResourceSpecs(701, 702)
	insertActiveResourceWorkUnits(t, essPool, repo, specs)

	if got, err := repo.FetchActiveWorkUnitResources(ctx, nil); err != nil || len(got) != 0 {
		t.Errorf("FetchActiveWorkUnitResources(nil queues) = (%v, %v), want (empty, nil)", got, err)
	}
	got, err := repo.FetchActiveWorkUnitResources(ctx, []string{storagetest.ActiveResourceQueue})
	if err != nil {
		t.Fatalf("FetchActiveWorkUnitResources failed: %v", err)
	}
	want := storagetest.WantActiveWorkUnitResources(specs, map[int64]string{701: "alice@example.com", 702: "bob@example.com"})
	if diff := cmp.Diff(want, got, storagetest.ActiveWorkUnitResourcesOrder, protocmp.Transform()); diff != "" {
		t.Errorf("FetchActiveWorkUnitResources() mismatch (-want +got):\n%s", diff)
	}
}
