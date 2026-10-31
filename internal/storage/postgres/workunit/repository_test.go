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
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/internal/validator"
)

var baseTestTime = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func TestNewRepository_BindsPoolAndTx(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool || repo.pool != pool {
		t.Errorf("NewRepository(pool) = (db=%v, pool=%v), want %p", repo.db, repo.pool, pool)
	}
	var tx pgdb.DBTX = &pgxpool.Pool{}
	txRepo := repo.WithTx(tx)
	if txRepo.tx != tx {
		t.Errorf("WithTx(tx).tx = %v, want %v", txRepo.tx, tx)
	}
}

func TestRepository_ValidationAndNilGuards(t *testing.T) {
	ctx := t.Context()
	repo := NewRepository(nil)
	txRepo := repo.WithTx(nil)

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.FetchActiveWorkUnitsByQueues(canceledCtx, []string{"q"}, 10, "", storage.ActiveWorkUnitQuery{}); !errors.Is(err, context.Canceled) {
		t.Errorf("FetchActiveWorkUnitsByQueues(canceled) = %v, want context.Canceled", err)
	}

	preconditionCalls := []func() error{
		func() error { _, err := repo.FetchWorkUnit(ctx, 1, 1); return err },
		func() error {
			_, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{
				XID: 1, WID: 1, WorkUnit: &essmsgpb.WorkUnit{},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			})
			return err
		},
		func() error { _, err := repo.FetchCurrentWorkUnitExecution(ctx, 1, 1); return err },
		func() error { _, err := repo.ListWorkUnitExecutions(ctx, 1, 1); return err },
		func() error { _, _, err := repo.ListWorkUnits(ctx, "experiments/1", 10, "", "", "", 0); return err },
		func() error {
			_, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"q"}, 10, "", storage.ActiveWorkUnitQuery{})
			return err
		},
		func() error { _, err := txRepo.FetchWorkUnit(ctx, 1, 1); return err },
		func() error { _, err := txRepo.FetchUpdatingWorkUnits(ctx, 1, []int64{1}); return err },
		func() error { _, err := txRepo.FetchNextWorkUnitID(ctx, 1); return err },
		func() error { return txRepo.InsertWorkUnit(ctx, &essmsgpb.WorkUnit{}) },
		func() error { return txRepo.SaveWorkUnitExecution(ctx, &essmsgpb.WorkUnitExecution{}) },
		func() error {
			return txRepo.UpdateCurrentWorkUnitExecution(ctx, 1, 1, 1, &essmsgpb.WorkUnitExecution{})
		},
		func() error {
			_, err := txRepo.ListWorkUnitMessagesBySeverity(ctx, 1, essmsgpb.StructuredMessage_ERROR)
			return err
		},
	}
	for i, call := range preconditionCalls {
		if got := status.Code(call()); got != codes.FailedPrecondition {
			t.Errorf("preconditionCalls[%d] code = %v, want FailedPrecondition", i, got)
		}
	}

	invalidArgCalls := []func() error{
		func() error { _, err := repo.UpdateWorkUnit(ctx, nil); return err },
		func() error { _, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{XID: 1, WID: 1}); return err },
		func() error {
			_, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{XID: 1, WID: 1, WorkUnit: &essmsgpb.WorkUnit{}})
			return err
		},
		func() error {
			_, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{
				XID: 1, WID: 1, WorkUnit: &essmsgpb.WorkUnit{}, UpdateMask: &fieldmaskpb.FieldMask{},
			})
			return err
		},
		func() error { return txRepo.InsertWorkUnit(ctx, nil) },
		func() error { return txRepo.SaveWorkUnitExecution(ctx, nil) },
		func() error { return txRepo.UpdateCurrentWorkUnitExecution(ctx, 1, 1, 1, nil) },
	}
	for i, call := range invalidArgCalls {
		if got := status.Code(call()); got != codes.InvalidArgument {
			t.Errorf("invalidArgCalls[%d] code = %v, want InvalidArgument", i, got)
		}
	}
}

func blankWorkUnitProto(xid, wid int64, ts time.Time) *essmsgpb.WorkUnit {
	tsProto := timestamppb.New(ts)
	return &essmsgpb.WorkUnit{
		Name:         validator.ConstructWorkUnitName(xid, wid),
		WorkUnitType: essmsgpb.WorkUnit_STANDARD,
		Priority:     1,
		Urls:         []*essmsgpb.Url{{Url: "http://test.com"}},
		Tags:         []string{"tag1", "tag2"},
		Notes:        "test-notes",
		Executable: &essmsgpb.Executable{
			SchedulingConstraints: &essmsgpb.SchedulingConstraints{QueueName: "test-queue"},
		},
		CreateTime: tsProto,
		UpdateTime: tsProto,
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name:       validator.ConstructWorkUnitExecutionName(xid, wid, 1),
			State:      essmsgpb.WorkUnitExecution_ACTIVE,
			Metadata:   &essmsgpb.WorkUnitExecutionMetadata{},
			CreateTime: tsProto,
			UpdateTime: tsProto,
			Status: &essmsgpb.WorkUnitStatus{
				State:           essmsgpb.WorkUnitStatus_INITIALIZING,
				UpdateTime:      tsProto,
				StateUpdateTime: tsProto,
				SystemMessage: &essmsgpb.StructuredMessage{
					Severity:       essmsgpb.StructuredMessage_INFO,
					Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
					Parent:         validator.ConstructWorkUnitName(xid, wid),
					UserFacingText: "test-message",
					CreateTime:     tsProto,
				},
			},
		},
	}
}

func mustInsertWorkUnit(t *testing.T, pool *pgxpool.Pool, repo *Repository, wu *essmsgpb.WorkUnit) {
	t.Helper()
	err := pgdb.RunInTx(t.Context(), pool, func(ctx context.Context, tx pgx.Tx) error {
		return repo.WithTx(tx).InsertWorkUnit(ctx, wu)
	})
	if err != nil {
		t.Fatalf("InsertWorkUnit(%s) failed: %v", wu.GetName(), err)
	}
}

func TestRepository_CRUDAndExecutions(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	t.Run("FetchWorkUnit and FetchCurrentWorkUnitExecution", func(t *testing.T) {
		const xid, wid = 101, 1
		postgrestest.SeedExperiment(ctx, t, essPool, xid)
		wantWU := blankWorkUnitProto(xid, wid, baseTestTime)
		mustInsertWorkUnit(t, essPool, repo, wantWU)

		gotWU, err := repo.FetchWorkUnit(ctx, xid, wid)
		if err != nil {
			t.Fatalf("FetchWorkUnit() failed: %v", err)
		}
		if diff := cmp.Diff(wantWU, gotWU, protocmp.Transform()); diff != "" {
			t.Errorf("FetchWorkUnit() mismatch (-want +got):\n%s", diff)
		}

		err = pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
			txWU, err := repo.WithTx(tx).FetchWorkUnit(ctx, xid, wid)
			if err != nil {
				return err
			}
			if diff := cmp.Diff(wantWU, txWU, protocmp.Transform()); diff != "" {
				t.Errorf("TxRepository.FetchWorkUnit() mismatch (-want +got):\n%s", diff)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("TxRepository.FetchWorkUnit() failed: %v", err)
		}

		gotExec, err := repo.FetchCurrentWorkUnitExecution(ctx, xid, wid)
		if err != nil {
			t.Fatalf("FetchCurrentWorkUnitExecution() failed: %v", err)
		}
		if diff := cmp.Diff(wantWU.CurrentWorkUnitExecution, gotExec, protocmp.Transform()); diff != "" {
			t.Errorf("FetchCurrentWorkUnitExecution() mismatch (-want +got):\n%s", diff)
		}

		if _, err := repo.FetchWorkUnit(ctx, xid, 999); !errors.Is(err, grpcerr.ErrNotFound) {
			t.Errorf("FetchWorkUnit(missing) = %v, want ErrNotFound", err)
		}
		if _, err := repo.FetchCurrentWorkUnitExecution(ctx, xid, 999); !errors.Is(err, grpcerr.ErrNotFound) {
			t.Errorf("FetchCurrentWorkUnitExecution(missing) = %v, want ErrNotFound", err)
		}
	})

	t.Run("ListWorkUnitExecutions and ExecutionArchiving", func(t *testing.T) {
		const xid, wid = 102, 1
		postgrestest.SeedExperiment(ctx, t, essPool, xid)
		wantWU := blankWorkUnitProto(xid, wid, baseTestTime)
		mustInsertWorkUnit(t, essPool, repo, wantWU)

		execs, err := repo.ListWorkUnitExecutions(ctx, xid, wid)
		if err != nil || len(execs) != 1 {
			t.Fatalf("ListWorkUnitExecutions() = (%d, %v), want 1 execution", len(execs), err)
		}

		archivedFirst := proto.Clone(wantWU.CurrentWorkUnitExecution).(*essmsgpb.WorkUnitExecution)
		archivedFirst.State = essmsgpb.WorkUnitExecution_INACTIVE
		secondTime := timestamppb.New(baseTestTime.Add(time.Minute))
		archivedSecond := &essmsgpb.WorkUnitExecution{
			Name:       validator.ConstructWorkUnitExecutionName(xid, wid, 2),
			State:      essmsgpb.WorkUnitExecution_INACTIVE,
			Metadata:   &essmsgpb.WorkUnitExecutionMetadata{},
			Status:     &essmsgpb.WorkUnitStatus{State: essmsgpb.WorkUnitStatus_FAILED, UpdateTime: secondTime},
			CreateTime: secondTime,
			UpdateTime: secondTime,
		}
		thirdTime := timestamppb.New(baseTestTime.Add(2 * time.Minute))
		thirdExec := &essmsgpb.WorkUnitExecution{
			Name:       validator.ConstructWorkUnitExecutionName(xid, wid, 3),
			State:      essmsgpb.WorkUnitExecution_ACTIVE,
			Metadata:   &essmsgpb.WorkUnitExecutionMetadata{},
			Status:     &essmsgpb.WorkUnitStatus{State: essmsgpb.WorkUnitStatus_RUNNING, UpdateTime: thirdTime},
			CreateTime: thirdTime,
			UpdateTime: thirdTime,
		}
		err = pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
			txRepo := repo.WithTx(tx)
			if err := txRepo.SaveWorkUnitExecution(ctx, archivedFirst); err != nil {
				return err
			}
			if err := txRepo.SaveWorkUnitExecution(ctx, archivedSecond); err != nil {
				return err
			}
			return txRepo.UpdateCurrentWorkUnitExecution(ctx, xid, wid, 3, thirdExec)
		})
		if err != nil {
			t.Fatalf("SaveWorkUnitExecution + UpdateCurrentWorkUnitExecution failed: %v", err)
		}

		execs, err = repo.ListWorkUnitExecutions(ctx, xid, wid)
		if err != nil {
			t.Fatalf("ListWorkUnitExecutions() after restarts failed: %v", err)
		}
		wantExecs := []*essmsgpb.WorkUnitExecution{thirdExec, archivedSecond, archivedFirst}
		if diff := cmp.Diff(wantExecs, execs, protocmp.Transform()); diff != "" {
			t.Errorf("ListWorkUnitExecutions() mismatch (-want +got):\n%s", diff)
		}
		if gotWU, err := repo.FetchWorkUnit(ctx, xid, wid); err != nil || !gotWU.GetUpdateTime().AsTime().Equal(thirdTime.AsTime()) {
			t.Errorf("FetchWorkUnit().UpdateTime after UpdateCurrentWorkUnitExecution = (%v, %v), want %v", gotWU.GetUpdateTime(), err, thirdTime)
		}
		if _, err := repo.ListWorkUnitExecutions(ctx, xid, 999); !errors.Is(err, grpcerr.ErrNotFound) {
			t.Errorf("ListWorkUnitExecutions(missing) = %v, want ErrNotFound", err)
		}
		if err := repo.WithTx(essPool).UpdateCurrentWorkUnitExecution(ctx, xid, 999, 1, execs[0]); !errors.Is(err, grpcerr.ErrNotFound) {
			t.Errorf("UpdateCurrentWorkUnitExecution(missing) = %v, want ErrNotFound", err)
		}
		if got := status.Code(repo.WithTx(essPool).SaveWorkUnitExecution(ctx, &essmsgpb.WorkUnitExecution{Name: "invalid"})); got != codes.Internal {
			t.Errorf("SaveWorkUnitExecution(invalid) code = %v, want Internal", got)
		}
	})

	t.Run("FetchUpdatingWorkUnits", func(t *testing.T) {
		const xid = 103
		postgrestest.SeedExperiment(ctx, t, essPool, xid)
		mustInsertWorkUnit(t, essPool, repo, blankWorkUnitProto(xid, 1, baseTestTime))
		mustInsertWorkUnit(t, essPool, repo, blankWorkUnitProto(xid, 2, baseTestTime))

		holdTx, err := essPool.Begin(ctx)
		if err != nil {
			t.Fatalf("essPool.Begin() failed: %v", err)
		}
		defer func() { _ = holdTx.Rollback(context.Background()) }()

		txRepo := repo.WithTx(holdTx)
		empty, err := txRepo.FetchUpdatingWorkUnits(ctx, xid, nil)
		if err != nil || len(empty) != 0 {
			t.Fatalf("FetchUpdatingWorkUnits(nil) = (%v, %v), want empty", empty, err)
		}
		got, err := txRepo.FetchUpdatingWorkUnits(ctx, xid, []int64{2, 1, 999})
		if err != nil {
			t.Fatalf("FetchUpdatingWorkUnits() failed: %v", err)
		}
		want := map[int64]*essmsgpb.WorkUnit{}
		for _, wid := range []int64{1, 2} {
			full := blankWorkUnitProto(xid, wid, baseTestTime)
			exec := proto.Clone(full.CurrentWorkUnitExecution).(*essmsgpb.WorkUnitExecution)
			exec.State = essmsgpb.WorkUnitExecution_INACTIVE
			want[wid] = &essmsgpb.WorkUnit{Name: full.Name, Executable: full.Executable, CurrentWorkUnitExecution: exec}
		}
		if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
			t.Errorf("FetchUpdatingWorkUnits() mismatch (-want +got):\n%s", diff)
		}

		for _, wid := range []int64{1, 2} {
			_, lockErr := essPool.Exec(ctx, "SELECT 1 FROM WorkUnits WHERE Xid = $1 AND WorkUnitId = $2 FOR NO KEY UPDATE NOWAIT", xid, wid)
			var pgErr *pgconn.PgError
			if !errors.As(lockErr, &pgErr) || pgErr.Code != "55P03" {
				t.Errorf("concurrent FOR NO KEY UPDATE NOWAIT on wid=%d err = %v, want SQLSTATE 55P03", wid, lockErr)
			}
			if _, err := essPool.Exec(ctx, "SELECT 1 FROM WorkUnits WHERE Xid = $1 AND WorkUnitId = $2 FOR KEY SHARE NOWAIT", xid, wid); err != nil {
				t.Errorf("concurrent FOR KEY SHARE NOWAIT on wid=%d failed (must lock with FOR NO KEY UPDATE, not FOR UPDATE): %v", wid, err)
			}
		}
	})

	t.Run("PositionInQueue zero sentinel sorts after positive queue positions", func(t *testing.T) {
		const (
			xid       = 105
			queueName = "queue-pos-sentinel-test"
		)
		postgrestest.SeedExperiment(ctx, t, essPool, xid)
		for _, tc := range []struct {
			wid int64
			pos int32
			ts  time.Time
		}{
			{wid: 1, pos: 0, ts: baseTestTime},
			{wid: 2, pos: 1, ts: baseTestTime.Add(time.Minute)},
		} {
			wu := blankWorkUnitProto(xid, tc.wid, tc.ts)
			wu.Executable.SchedulingConstraints.QueueName = queueName
			wu.CurrentWorkUnitExecution.Status.State = essmsgpb.WorkUnitStatus_SCHEDULING
			wu.CurrentWorkUnitExecution.Status.SchedulingInformation = &essmsgpb.StructuredMessage{
				AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
					Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{QueueInfo: &essmsgpb.QueueInfo{PositionInQueue: tc.pos}},
				},
			}
			mustInsertWorkUnit(t, essPool, repo, wu)
		}

		activeRes, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{queueName}, 10, "", storage.ActiveWorkUnitQuery{})
		if err != nil {
			t.Fatalf("FetchActiveWorkUnitsByQueues() failed: %v", err)
		}
		var gotWIDs []int64
		for _, item := range activeRes.WorkUnits {
			_, wid, _ := validator.ValidateWorkUnitName(item.WorkUnit.GetName())
			gotWIDs = append(gotWIDs, wid)
		}
		if diff := cmp.Diff([]int64{2, 1}, gotWIDs); diff != "" {
			t.Errorf("FetchActiveWorkUnitsByQueues() WID order mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestRepository_UpdateWorkUnitAndConcurrency(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	const xid, wid = 201, 1
	postgrestest.SeedExperiment(ctx, t, essPool, xid)
	created := blankWorkUnitProto(xid, wid, baseTestTime)
	mustInsertWorkUnit(t, essPool, repo, created)

	update := &storage.WorkUnitUpdate{
		XID: xid,
		WID: wid,
		WorkUnit: &essmsgpb.WorkUnit{
			Priority: 2,
			Notes:    "new-notes",
			Tags:     []string{"tag3"},
			Urls:     []*essmsgpb.Url{{Url: "http://new.com"}},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority", "notes", "tags", "urls"}},
	}
	res, err := repo.UpdateWorkUnit(ctx, update)
	if err != nil {
		t.Fatalf("UpdateWorkUnit() failed: %v", err)
	}
	if diff := cmp.Diff(created, res.Original, protocmp.Transform()); diff != "" {
		t.Errorf("Original mismatch (-want +got):\n%s", diff)
	}
	want := proto.Clone(created).(*essmsgpb.WorkUnit)
	want.Priority, want.Notes, want.Tags = 2, "new-notes", []string{"tag3"}
	want.Urls = []*essmsgpb.Url{{Url: "http://new.com"}}
	if diff := cmp.Diff(want, res.Updated, protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.WorkUnit{}, "update_time")); diff != "" {
		t.Errorf("Updated mismatch (-want +got):\n%s", diff)
	}
	if !res.Updated.GetUpdateTime().AsTime().After(created.GetUpdateTime().AsTime()) {
		t.Errorf("Updated.UpdateTime = %v, want after %v", res.Updated.GetUpdateTime(), created.GetUpdateTime())
	}
	gotWU, err := repo.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		t.Fatalf("FetchWorkUnit() failed: %v", err)
	}
	if diff := cmp.Diff(res.Updated, gotWU, protocmp.Transform()); diff != "" {
		t.Errorf("FetchWorkUnit() after update mismatch (-want +got):\n%s", diff)
	}

	noopRes, err := repo.UpdateWorkUnit(ctx, update)
	if err != nil {
		t.Fatalf("no-op UpdateWorkUnit() failed: %v", err)
	}
	if !noopRes.Updated.GetUpdateTime().AsTime().Equal(res.Updated.GetUpdateTime().AsTime()) {
		t.Errorf("no-op UpdateWorkUnit changed UpdateTime: got %v, want %v", noopRes.Updated.GetUpdateTime(), res.Updated.GetUpdateTime())
	}

	futureTime := time.Now().UTC().Truncate(time.Microsecond).Add(time.Minute)
	mustInsertWorkUnit(t, essPool, repo, blankWorkUnitProto(xid, 2, futureTime))
	for i, note := range []string{"back-to-back-1", "back-to-back-2"} {
		stepRes, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{
			XID:        xid,
			WID:        2,
			WorkUnit:   &essmsgpb.WorkUnit{Notes: note},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
		})
		if err != nil {
			t.Fatalf("UpdateWorkUnit(back-to-back %d) failed: %v", i+1, err)
		}
		wantUpdateTime := futureTime.Add(time.Duration(i+1) * time.Microsecond)
		if got := stepRes.Updated.GetUpdateTime().AsTime(); !got.Equal(wantUpdateTime) {
			t.Errorf("UpdateWorkUnit(back-to-back %d) UpdateTime = %v, want %v", i+1, got, wantUpdateTime)
		}
	}

	immutableUpdate := &storage.WorkUnitUpdate{
		XID:        xid,
		WID:        wid,
		WorkUnit:   &essmsgpb.WorkUnit{WorkUnitType: essmsgpb.WorkUnit_AUXILIARY},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"work_unit_type"}},
	}
	if _, err := repo.UpdateWorkUnit(ctx, immutableUpdate); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateWorkUnit(immutable) = %v, want ErrInvalidArgument", err)
	}
	emptyMaskUpdate := &storage.WorkUnitUpdate{
		XID:        xid,
		WID:        wid,
		WorkUnit:   &essmsgpb.WorkUnit{},
		UpdateMask: &fieldmaskpb.FieldMask{},
	}
	if _, err := repo.UpdateWorkUnit(ctx, emptyMaskUpdate); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateWorkUnit(empty mask) = %v, want ErrInvalidArgument", err)
	}
	if _, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{
		XID: 9999, WID: 1, WorkUnit: &essmsgpb.WorkUnit{Notes: "x"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
	}); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("UpdateWorkUnit(missing) = %v, want ErrNotFound", err)
	}

	holdTx, err := essPool.Begin(ctx)
	if err != nil {
		t.Fatalf("essPool.Begin() failed: %v", err)
	}
	defer func() { _ = holdTx.Rollback(context.Background()) }()
	if _, err := holdTx.Exec(ctx, lockWorkUnitSQL.String(), xid, wid); err != nil {
		t.Fatalf("holdTx lock failed: %v", err)
	}

	var started, done sync.WaitGroup
	errs := make(chan error, 2)
	started.Add(2)
	done.Go(func() {
		started.Done()
		_, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{
			XID: xid, WID: wid, WorkUnit: &essmsgpb.WorkUnit{Notes: "concurrent-notes"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
		})
		errs <- err
	})
	done.Go(func() {
		started.Done()
		_, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{
			XID: xid, WID: wid, WorkUnit: &essmsgpb.WorkUnit{Tags: []string{"concurrent-tag"}},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
		})
		errs <- err
	})
	started.Wait()
	time.Sleep(20 * time.Millisecond)
	if err := holdTx.Commit(ctx); err != nil {
		t.Fatalf("holdTx.Commit() failed: %v", err)
	}
	done.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent UpdateWorkUnit failed: %v", err)
		}
	}
	finalWU, err := repo.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		t.Fatalf("FetchWorkUnit() after concurrent updates failed: %v", err)
	}
	wantFinal := proto.Clone(res.Updated).(*essmsgpb.WorkUnit)
	wantFinal.Notes = "concurrent-notes"
	wantFinal.Tags = []string{"concurrent-tag"}
	if diff := cmp.Diff(wantFinal, finalWU, protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.WorkUnit{}, "update_time")); diff != "" {
		t.Errorf("FetchWorkUnit() after concurrent updates mismatch (-want +got):\n%s", diff)
	}
}

func TestRepository_IDAllocationAndConcurrentInserts(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	const xid = 301
	postgrestest.SeedExperiment(ctx, t, essPool, xid)

	err := pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
		return repo.WithTx(tx).InsertWorkUnit(ctx, blankWorkUnitProto(99999, 1, baseTestTime))
	})
	if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
		t.Errorf("InsertWorkUnit(missing experiment) = %v (code %v), want ErrNotFound", err, status.Code(err))
	}

	const workers = 8
	allocated := make([]int64, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			txErr := pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
				txRepo := repo.WithTx(tx)
				nextID, err := txRepo.FetchNextWorkUnitID(ctx, xid)
				if err != nil {
					return err
				}
				allocated[idx] = nextID
				return txRepo.InsertWorkUnit(ctx, blankWorkUnitProto(xid, nextID, baseTestTime))
			})
			if txErr != nil {
				t.Errorf("worker %d transaction failed: %v", idx, txErr)
			}
		}(i)
	}
	wg.Wait()

	slices.Sort(allocated)
	wantIDs := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	if !slices.Equal(allocated, wantIDs) {
		t.Errorf("concurrent allocated WIDs = %v, want %v", allocated, wantIDs)
	}

	holdTx, err := essPool.Begin(ctx)
	if err != nil {
		t.Fatalf("essPool.Begin() failed: %v", err)
	}
	defer func() { _ = holdTx.Rollback(context.Background()) }()
	if err := repo.WithTx(holdTx).InsertWorkUnit(ctx, blankWorkUnitProto(xid, workers+1, baseTestTime)); err != nil {
		t.Fatalf("InsertWorkUnit(caller-supplied wid) failed: %v", err)
	}
	var gotID int64
	var started sync.WaitGroup
	started.Add(1)
	done := make(chan error, 1)
	go func() {
		started.Done()
		done <- pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
			txRepo := repo.WithTx(tx)
			id, err := txRepo.FetchNextWorkUnitID(ctx, xid)
			if err != nil {
				return err
			}
			gotID = id
			return txRepo.InsertWorkUnit(ctx, blankWorkUnitProto(xid, id, baseTestTime))
		})
	}()
	started.Wait()
	time.Sleep(20 * time.Millisecond)
	if err := holdTx.Commit(ctx); err != nil {
		t.Fatalf("holdTx.Commit() failed: %v", err)
	}
	if err := <-done; err != nil || gotID != workers+2 {
		t.Errorf("allocator after caller-supplied insert = (%d, %v), want (%d, nil)", gotID, err, workers+2)
	}
}

func TestRepository_ListWorkUnitMessagesBySeverity(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	const xid = 401
	postgrestest.SeedExperiment(ctx, t, essPool, xid)

	seedWithMsgs := func(wid int64, sysMsg, userMsg *essmsgpb.StructuredMessage) {
		wu := blankWorkUnitProto(xid, wid, baseTestTime)
		wu.CurrentWorkUnitExecution.Status.SystemMessage = sysMsg
		wu.CurrentWorkUnitExecution.Status.UserMessage = userMsg
		mustInsertWorkUnit(t, essPool, repo, wu)
	}
	msg := func(text string, sev essmsgpb.StructuredMessage_Severity) *essmsgpb.StructuredMessage {
		return &essmsgpb.StructuredMessage{UserFacingText: text, Severity: sev, CreateTime: timestamppb.New(baseTestTime)}
	}

	errSys3 := msg("err sys 3", essmsgpb.StructuredMessage_ERROR)
	errUser3 := msg("err user 3", essmsgpb.StructuredMessage_ERROR)
	errSys5 := msg("err sys 5", essmsgpb.StructuredMessage_ERROR)
	seedWithMsgs(1, msg("info 1", essmsgpb.StructuredMessage_INFO), nil)
	seedWithMsgs(2, msg("warn 1", essmsgpb.StructuredMessage_WARNING), nil)
	seedWithMsgs(3, errSys3, errUser3)
	seedWithMsgs(4, nil, nil)
	seedWithMsgs(5, errSys5, nil)

	if _, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{
		XID: xid, WID: 3, WorkUnit: &essmsgpb.WorkUnit{Notes: "moved-tuple"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
	}); err != nil {
		t.Fatalf("UpdateWorkUnit(wid=3) failed: %v", err)
	}

	err := pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
		txRepo := repo.WithTx(tx)
		errMsgs, err := txRepo.ListWorkUnitMessagesBySeverity(ctx, xid, essmsgpb.StructuredMessage_ERROR)
		if err != nil {
			t.Fatalf("ListWorkUnitMessagesBySeverity(ERROR) failed: %v", err)
		}
		wantErrMsgs := []*essmsgpb.StructuredMessage{errSys3, errUser3, errSys5}
		if diff := cmp.Diff(wantErrMsgs, errMsgs, protocmp.Transform()); diff != "" {
			t.Errorf("ListWorkUnitMessagesBySeverity(ERROR) mismatch (-want +got):\n%s", diff)
		}
		fatalMsgs, err := txRepo.ListWorkUnitMessagesBySeverity(ctx, xid, essmsgpb.StructuredMessage_FATAL)
		if err != nil || len(fatalMsgs) != 0 {
			t.Fatalf("ListWorkUnitMessagesBySeverity(FATAL) = (%d, %v), want 0", len(fatalMsgs), err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ListWorkUnitMessagesBySeverity failed: %v", err)
	}
}

// Concurrent restart transactions on the same WorkUnit must not lose executions:
// TxRepository.FetchWorkUnit locks the row with FOR NO KEY UPDATE, serializing concurrent writers
// without conflicting with FOR KEY SHARE foreign-key checks.
func TestTxRepository_ConcurrentExecutionRestartsNoLostUpdate(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	const xid, wid, workers = 104, 1, 4
	postgrestest.SeedExperiment(ctx, t, essPool, xid)
	initialWU := blankWorkUnitProto(xid, wid, baseTestTime)
	mustInsertWorkUnit(t, essPool, repo, initialWU)
	firstExec := initialWU.CurrentWorkUnitExecution
	assertNoKeyUpdateCompatibleWithKeyShare(t, essPool, repo, xid, wid)

	holdTx, err := essPool.Begin(ctx)
	if err != nil {
		t.Fatalf("essPool.Begin() failed: %v", err)
	}
	defer func() { _ = holdTx.Rollback(context.Background()) }()
	if _, err := holdTx.Exec(ctx, lockWorkUnitSQL.String(), xid, wid); err != nil {
		t.Fatalf("holdTx lock failed: %v", err)
	}

	errs := make([]error, workers)
	var started, done sync.WaitGroup
	for i := range workers {
		started.Add(1)
		done.Go(func() {
			started.Done()
			errs[i] = pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
				txRepo := repo.WithTx(tx)
				wu, err := txRepo.FetchWorkUnit(ctx, xid, wid)
				if err != nil {
					return err
				}
				curExec := wu.GetCurrentWorkUnitExecution()
				_, _, curEID, err := validator.ValidateWorkUnitExecutionName(curExec.GetName())
				if err != nil {
					return err
				}
				archived := proto.Clone(curExec).(*essmsgpb.WorkUnitExecution)
				archived.State = essmsgpb.WorkUnitExecution_INACTIVE
				if err := txRepo.SaveWorkUnitExecution(ctx, archived); err != nil {
					return err
				}
				nextEID := curEID + 1
				ts := timestamppb.New(baseTestTime.Add(time.Duration(nextEID) * time.Minute))
				nextExec := &essmsgpb.WorkUnitExecution{
					Name:       validator.ConstructWorkUnitExecutionName(xid, wid, nextEID),
					State:      essmsgpb.WorkUnitExecution_ACTIVE,
					Metadata:   &essmsgpb.WorkUnitExecutionMetadata{ResourceName: fmt.Sprintf("jobset-%d", nextEID)},
					Status:     &essmsgpb.WorkUnitStatus{State: essmsgpb.WorkUnitStatus_RUNNING, UpdateTime: ts},
					CreateTime: ts,
					UpdateTime: ts,
				}
				return txRepo.UpdateCurrentWorkUnitExecution(ctx, xid, wid, nextEID, nextExec)
			})
		})
	}
	started.Wait()
	time.Sleep(20 * time.Millisecond)
	if err := holdTx.Commit(ctx); err != nil {
		t.Fatalf("holdTx.Commit() failed: %v", err)
	}
	done.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("concurrent RunInTx() failed: %v", err)
	}

	gotExecs, err := repo.ListWorkUnitExecutions(ctx, xid, wid)
	if err != nil {
		t.Fatalf("ListWorkUnitExecutions() failed: %v", err)
	}
	archivedFirst := proto.Clone(firstExec).(*essmsgpb.WorkUnitExecution)
	archivedFirst.State = essmsgpb.WorkUnitExecution_INACTIVE
	wantExecs := make([]*essmsgpb.WorkUnitExecution, 0, workers+1)
	for eid := int64(workers + 1); eid >= 2; eid-- {
		ts := timestamppb.New(baseTestTime.Add(time.Duration(eid) * time.Minute))
		state := essmsgpb.WorkUnitExecution_INACTIVE
		if eid == int64(workers+1) {
			state = essmsgpb.WorkUnitExecution_ACTIVE
		}
		wantExecs = append(wantExecs, &essmsgpb.WorkUnitExecution{
			Name:       validator.ConstructWorkUnitExecutionName(xid, wid, eid),
			State:      state,
			Metadata:   &essmsgpb.WorkUnitExecutionMetadata{ResourceName: fmt.Sprintf("jobset-%d", eid)},
			Status:     &essmsgpb.WorkUnitStatus{State: essmsgpb.WorkUnitStatus_RUNNING, UpdateTime: ts},
			CreateTime: ts,
			UpdateTime: ts,
		})
	}
	wantExecs = append(wantExecs, archivedFirst)
	if diff := cmp.Diff(wantExecs, gotExecs, protocmp.Transform()); diff != "" {
		t.Errorf("ListWorkUnitExecutions() after concurrent restarts mismatch (-want +got):\n%s", diff)
	}
}

func assertNoKeyUpdateCompatibleWithKeyShare(t *testing.T, pool *pgxpool.Pool, repo *Repository, xid, wid int64) {
	t.Helper()
	ctx := t.Context()
	keyShareTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin() for FOR KEY SHARE failed: %v", err)
	}
	defer func() { _ = keyShareTx.Rollback(context.Background()) }()
	if _, err := keyShareTx.Exec(ctx, "SELECT 1 FROM WorkUnits WHERE Xid = $1 AND WorkUnitId = $2 FOR KEY SHARE", xid, wid); err != nil {
		t.Fatalf("FOR KEY SHARE lock failed: %v", err)
	}
	err = pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '500ms'"); err != nil {
			return err
		}
		_, err := repo.WithTx(tx).FetchWorkUnit(ctx, xid, wid)
		return err
	})
	if err != nil {
		t.Fatalf("TxRepository.FetchWorkUnit() blocked by FOR KEY SHARE (must use FOR NO KEY UPDATE, not FOR UPDATE): %v", err)
	}
}
