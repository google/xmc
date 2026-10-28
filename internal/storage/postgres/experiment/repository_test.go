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

package experiment

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
)

var (
	baseTime = time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)

	baseExperiment = &essmsgpb.Experiment{
		Name:     "experiments/100",
		Title:    "Baseline Experiment",
		Creator:  "researcher@example.com",
		Priority: 2,
		Urls:     []*essmsgpb.Url{{Url: "https://example.com/exp/100", DisplayName: "Dashboard"}},
		Tags:     []string{"baseline", "llm"},
		Notes:    "Initial baseline notes",
		Resources: []*essmsgpb.ResourceSet{
			{
				Resources: &essmsgpb.ResourceSet_CpuResources{
					CpuResources: &essmsgpb.CpuResources{CpuMillicores: 2000, MemoryBytes: 4096},
				},
			},
		},
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
			LaunchState: essmsgpb.ExperimentStatus_LAUNCHING,
			UpdateTime:  timestamppb.New(baseTime),
		},
		CreateTime: timestamppb.New(baseTime),
		UpdateTime: timestamppb.New(baseTime),
	}
)

func seedExperiment(ctx context.Context, t *testing.T, pool *pgxpool.Pool, exp *essmsgpb.Experiment) *essmsgpb.Experiment {
	t.Helper()
	err := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		return NewTxRepository(tx).InsertExperiment(ctx, exp)
	})
	if err != nil {
		t.Fatalf("InsertExperiment(%q) failed: %v", exp.GetName(), err)
	}
	return exp
}

func TestRepository_ValidationAndNilHandle(t *testing.T) {
	ctx := context.Background()
	nilRepo := NewRepository(nil)
	nilTxRepo := nilRepo.WithTx(nil)

	if _, err := nilRepo.FetchExperiment(ctx, 0); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("FetchExperiment(0) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if _, err := nilRepo.FetchExperiment(ctx, 1); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("nilRepo.FetchExperiment(1) error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if _, err := nilRepo.FetchExperimentStatus(ctx, 0); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("FetchExperimentStatus(0) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if _, err := nilRepo.FetchExperimentStatus(ctx, 1); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("nilRepo.FetchExperimentStatus(1) error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if _, _, err := nilRepo.ListExperiments(ctx, 10, "", "", "", 0); status.Code(err) != codes.Unimplemented {
		t.Errorf("ListExperiments() code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
	if _, err := nilRepo.FetchDriftedComponents(ctx, 1, 0); status.Code(err) != codes.Unimplemented {
		t.Errorf("FetchDriftedComponents() code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
	if _, err := nilRepo.UpdateExperiment(ctx, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("nilRepo.UpdateExperiment(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	validUpdate := &storage.ExperimentUpdate{
		XID:        1,
		Experiment: &essmsgpb.Experiment{Title: "t"},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
	}
	if _, err := nilRepo.UpdateExperiment(ctx, validUpdate); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("nilRepo.UpdateExperiment(valid) error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if _, err := nilTxRepo.FetchExperiment(ctx, 1); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("nilTxRepo.FetchExperiment(1) error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if err := nilTxRepo.InsertExperiment(ctx, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("InsertExperiment(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if err := nilTxRepo.InsertExperiment(ctx, &essmsgpb.Experiment{Name: "bad"}); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("InsertExperiment(bad) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if err := nilTxRepo.InsertExperiment(ctx, baseExperiment); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("nilTxRepo.InsertExperiment() error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if err := nilTxRepo.UpdateExperimentStatus(ctx, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateExperimentStatus(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if err := nilTxRepo.UpdateExperimentResources(ctx, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateExperimentResources(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
}

func TestInsertAndFetchExperiment(t *testing.T) {
	ctx := context.Background()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	seedExperiment(ctx, t, essPool, baseExperiment)

	got, err := repo.FetchExperiment(ctx, 100)
	if err != nil {
		t.Fatalf("FetchExperiment(100) failed: %v", err)
	}
	if diff := cmp.Diff(baseExperiment, got, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperiment(100) diff (-want +got):\n%s", diff)
	}

	gotStatus, err := repo.FetchExperimentStatus(ctx, 100)
	if err != nil {
		t.Fatalf("FetchExperimentStatus(100) failed: %v", err)
	}
	if diff := cmp.Diff(baseExperiment.GetStatus(), gotStatus, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperimentStatus(100) diff (-want +got):\n%s", diff)
	}

	// Duplicate insert must return ErrAlreadyExists.
	err = pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
		return NewTxRepository(tx).InsertExperiment(ctx, baseExperiment)
	})
	if !errors.Is(err, grpcerr.ErrAlreadyExists) {
		t.Errorf("duplicate InsertExperiment error = %v, want %v", err, grpcerr.ErrAlreadyExists)
	}

	// Nil Status must serialize to empty NOT NULL bytea and round-trip as an empty ExperimentStatus.
	nilStatusExp := prototest.Modify(baseExperiment, func(e *essmsgpb.Experiment) {
		e.Name = "experiments/101"
		e.Status = nil
	})
	seedExperiment(ctx, t, essPool, nilStatusExp)
	gotNilStatus, err := repo.FetchExperiment(ctx, 101)
	if err != nil {
		t.Fatalf("FetchExperiment(101) failed: %v", err)
	}
	wantNilStatus := prototest.Modify(nilStatusExp, func(e *essmsgpb.Experiment) {
		e.Status = &essmsgpb.ExperimentStatus{}
	})
	if diff := cmp.Diff(wantNilStatus, gotNilStatus, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperiment(101) nil Status diff (-want +got):\n%s", diff)
	}

	if _, err := repo.FetchExperiment(ctx, 99999); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("FetchExperiment(non-existent) error = %v, want %v", err, grpcerr.ErrNotFound)
	}
	if _, err := repo.FetchExperimentStatus(ctx, 99999); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("FetchExperimentStatus(non-existent) error = %v, want %v", err, grpcerr.ErrNotFound)
	}
}

func TestUpdateExperiment(t *testing.T) {
	ctx := context.Background()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	seedExperiment(ctx, t, essPool, baseExperiment)

	update := &storage.ExperimentUpdate{
		XID: 100,
		Experiment: &essmsgpb.Experiment{
			Title:    "Updated Title",
			Notes:    "Updated Notes",
			Priority: 5,
			Tags:     []string{"updated-tag"},
			Urls:     []*essmsgpb.Url{{Url: "https://example.com/new"}},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"title", "notes", "priority", "tags", "urls"}},
	}
	res, err := repo.UpdateExperiment(ctx, update)
	if err != nil {
		t.Fatalf("UpdateExperiment() failed: %v", err)
	}
	if diff := cmp.Diff(baseExperiment, res.Original, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateExperiment Original diff (-want +got):\n%s", diff)
	}
	wantUpdated := prototest.Modify(baseExperiment, func(e *essmsgpb.Experiment) {
		e.Title = "Updated Title"
		e.Notes = "Updated Notes"
		e.Priority = 5
		e.Tags = []string{"updated-tag"}
		e.Urls = []*essmsgpb.Url{{Url: "https://example.com/new"}}
	})
	if diff := cmp.Diff(wantUpdated, res.Updated, protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.Experiment{}, "update_time")); diff != "" {
		t.Errorf("UpdateExperiment Updated diff (-want +got):\n%s", diff)
	}
	if !res.Updated.GetUpdateTime().AsTime().After(baseExperiment.GetUpdateTime().AsTime()) {
		t.Errorf("UpdateExperiment UpdateTime = %v, want > %v", res.Updated.GetUpdateTime(), baseExperiment.GetUpdateTime())
	}

	fetched, err := repo.FetchExperiment(ctx, 100)
	if err != nil {
		t.Fatalf("FetchExperiment(100) after update failed: %v", err)
	}
	if diff := cmp.Diff(res.Updated, fetched, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperiment(100) after update diff (-want +got):\n%s", diff)
	}

	// No-op update must preserve UpdateTime and return distinct pointers.
	noopRes, err := repo.UpdateExperiment(ctx, update)
	if err != nil {
		t.Fatalf("no-op UpdateExperiment() failed: %v", err)
	}
	if noopRes.Original == noopRes.Updated {
		t.Error("no-op UpdateExperiment returned aliased Original and Updated pointers")
	}
	if diff := cmp.Diff(res.Updated, noopRes.Updated, protocmp.Transform()); diff != "" {
		t.Errorf("no-op UpdateExperiment diff (-want +got):\n%s", diff)
	}
}

func TestUpdateExperiment_Errors(t *testing.T) {
	ctx := context.Background()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	seedExperiment(ctx, t, essPool, baseExperiment)

	tests := []struct {
		name    string
		update  *storage.ExperimentUpdate
		wantErr error
	}{
		{
			name:    "nil update",
			update:  nil,
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "empty update mask",
			update: &storage.ExperimentUpdate{
				XID:        100,
				Experiment: &essmsgpb.Experiment{Title: "x"},
				UpdateMask: &fmpb.FieldMask{},
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "zero XID",
			update: &storage.ExperimentUpdate{
				XID:        0,
				Experiment: &essmsgpb.Experiment{Title: "x"},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "immutable field creator",
			update: &storage.ExperimentUpdate{
				XID:        100,
				Experiment: &essmsgpb.Experiment{Creator: "other@example.com"},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"creator"}},
			},
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "non-existent experiment",
			update: &storage.ExperimentUpdate{
				XID:        99999,
				Experiment: &essmsgpb.Experiment{Title: "x"},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
			},
			wantErr: grpcerr.ErrNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repo.UpdateExperiment(ctx, tc.update); !errors.Is(err, tc.wantErr) {
				t.Errorf("UpdateExperiment() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestUpdateExperiment_Concurrent(t *testing.T) {
	ctx := context.Background()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	seedExperiment(ctx, t, essPool, baseExperiment)

	holdTx, err := essPool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin holdTx failed: %v", err)
	}
	defer func() { _ = holdTx.Rollback(ctx) }()
	if _, err := holdTx.Exec(ctx, "SELECT 1 FROM Experiments WHERE Xid = $1 FOR NO KEY UPDATE", 100); err != nil {
		t.Fatalf("lock row in holdTx failed: %v", err)
	}

	updates := []*storage.ExperimentUpdate{
		{
			XID:        100,
			Experiment: &essmsgpb.Experiment{Title: "Concurrent Title", Notes: "Concurrent Notes"},
			UpdateMask: &fmpb.FieldMask{Paths: []string{"title", "notes"}},
		},
		{
			XID:        100,
			Experiment: &essmsgpb.Experiment{Priority: 9, Tags: []string{"concurrent-tag"}},
			UpdateMask: &fmpb.FieldMask{Paths: []string{"priority", "tags"}},
		},
	}

	var wg sync.WaitGroup
	errs := make([]error, len(updates))
	for i, u := range updates {
		wg.Add(1)
		go func(idx int, upd *storage.ExperimentUpdate) {
			defer wg.Done()
			_, errs[idx] = repo.UpdateExperiment(ctx, upd)
		}(i, u)
	}
	time.Sleep(20 * time.Millisecond)
	if err := holdTx.Commit(ctx); err != nil {
		t.Fatalf("Commit holdTx failed: %v", err)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent UpdateExperiment[%d] failed: %v", i, err)
		}
	}

	got, err := repo.FetchExperiment(ctx, 100)
	if err != nil {
		t.Fatalf("FetchExperiment(100) failed: %v", err)
	}
	want := prototest.Modify(baseExperiment, func(e *essmsgpb.Experiment) {
		e.Title = "Concurrent Title"
		e.Notes = "Concurrent Notes"
		e.Priority = 9
		e.Tags = []string{"concurrent-tag"}
	})
	if diff := cmp.Diff(want, got, protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.Experiment{}, "update_time")); diff != "" {
		t.Errorf("FetchExperiment(100) after concurrent updates diff (-want +got):\n%s", diff)
	}
}

func TestTxRepository_StatusAndResourcesUpdates(t *testing.T) {
	ctx := context.Background()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	seedExperiment(ctx, t, essPool, baseExperiment)

	statusUpdateTime := baseTime.Add(time.Hour)
	resourcesUpdateTime := baseTime.Add(90 * time.Minute)
	completeTime := baseTime.Add(2 * time.Hour)
	newResources := []*essmsgpb.ResourceSet{
		{
			Resources: &essmsgpb.ResourceSet_CpuResources{
				CpuResources: &essmsgpb.CpuResources{CpuMillicores: 4000, MemoryBytes: 8192},
			},
		},
	}

	// UpdateExperimentStatus without bumping exp.UpdateTime must advance UpdateTime from Status.UpdateTime.
	err := pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
		txRepo := NewTxRepository(tx)
		exp, err := txRepo.FetchExperiment(ctx, 100)
		if err != nil {
			return err
		}
		exp.Status = &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_INACTIVE,
			LaunchState: essmsgpb.ExperimentStatus_COMPLETED,
			UpdateTime:  timestamppb.New(statusUpdateTime),
		}
		exp.CompleteTime = timestamppb.New(completeTime)
		return txRepo.UpdateExperimentStatus(ctx, exp)
	})
	if err != nil {
		t.Fatalf("UpdateExperimentStatus failed: %v", err)
	}
	afterStatus, err := repo.FetchExperiment(ctx, 100)
	if err != nil {
		t.Fatalf("FetchExperiment(100) after status update failed: %v", err)
	}
	if !afterStatus.GetUpdateTime().AsTime().Equal(statusUpdateTime) {
		t.Errorf("UpdateTime after status update = %v, want %v", afterStatus.GetUpdateTime().AsTime(), statusUpdateTime)
	}

	err = pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
		txRepo := NewTxRepository(tx)
		exp, err := txRepo.FetchExperiment(ctx, 100)
		if err != nil {
			return err
		}
		exp.Resources = newResources
		exp.UpdateTime = timestamppb.New(resourcesUpdateTime)
		return txRepo.UpdateExperimentResources(ctx, exp)
	})
	if err != nil {
		t.Fatalf("UpdateExperimentResources failed: %v", err)
	}

	got, err := repo.FetchExperiment(ctx, 100)
	if err != nil {
		t.Fatalf("FetchExperiment(100) failed: %v", err)
	}
	want := prototest.Modify(baseExperiment, func(e *essmsgpb.Experiment) {
		e.Status = &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_INACTIVE,
			LaunchState: essmsgpb.ExperimentStatus_COMPLETED,
			UpdateTime:  timestamppb.New(statusUpdateTime),
		}
		e.Resources = newResources
		e.UpdateTime = timestamppb.New(resourcesUpdateTime)
		e.CompleteTime = timestamppb.New(completeTime)
	})
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperiment(100) after Tx updates diff (-want +got):\n%s", diff)
	}

	// Updating status or resources of a non-existent experiment must return ErrNotFound.
	missingExp := prototest.Modify(baseExperiment, func(e *essmsgpb.Experiment) { e.Name = "experiments/99999" })
	err = pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
		return NewTxRepository(tx).UpdateExperimentStatus(ctx, missingExp)
	})
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("UpdateExperimentStatus(missing) error = %v, want %v", err, grpcerr.ErrNotFound)
	}
	err = pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
		return NewTxRepository(tx).UpdateExperimentResources(ctx, missingExp)
	})
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("UpdateExperimentResources(missing) error = %v, want %v", err, grpcerr.ErrNotFound)
	}
}
