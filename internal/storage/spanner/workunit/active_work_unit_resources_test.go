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
	"time"

	essmsgpb "xmanager_cloud/api/protos/experiment"
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

func TestFetchActiveWorkUnitResources_EmptyQueues(t *testing.T) {
	ctx := t.Context()
	storagetest.VerifyEmptyActiveResourcesSnapshot(ctx, t, newEmulatorStorage(ctx, t))
}

func TestFetchActiveWorkUnitResources_MultiPageSnapshot(t *testing.T) {
	ctx := t.Context()
	store := newEmulatorStorage(ctx, t)
	seedExperimentWithCreator(ctx, t, store, 201, "alice@example.com")
	seedExperimentWithCreator(ctx, t, store, 202, "bob@example.com")
	specs := storagetest.ActiveResourceSpecs(201, 202)
	seedActiveResourceWorkUnits(ctx, t, store, specs)
	want := storagetest.WantActiveWorkUnitResources(specs, map[int64]string{201: "alice@example.com", 202: "bob@example.com"})
	storagetest.VerifyActiveResourcesSnapshot(ctx, t, store, want)
}
