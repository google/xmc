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
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
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

func TestRepository_ValidationAndUnimplementedStubs(t *testing.T) {
	ctx := t.Context()
	repo := NewRepository(nil)
	txRepo := repo.WithTx(nil)

	preconditionCalls := []func() error{
		func() error { _, err := repo.FetchWorkUnit(ctx, 1, 1); return err },
		func() error { _, err := repo.FetchCurrentWorkUnitExecution(ctx, 1, 1); return err },
		func() error { _, err := repo.ListWorkUnitExecutions(ctx, 1, 1); return err },
		func() error { _, err := txRepo.FetchWorkUnit(ctx, 1, 1); return err },
		func() error { return txRepo.SaveWorkUnitExecution(ctx, &essmsgpb.WorkUnitExecution{}) },
		func() error {
			return txRepo.UpdateCurrentWorkUnitExecution(ctx, 1, 1, 1, &essmsgpb.WorkUnitExecution{})
		},
	}
	for i, call := range preconditionCalls {
		if got := status.Code(call()); got != codes.FailedPrecondition {
			t.Errorf("preconditionCalls[%d] code = %v, want FailedPrecondition", i, got)
		}
	}

	invalidArgCalls := []func() error{
		func() error { return txRepo.SaveWorkUnitExecution(ctx, nil) },
		func() error { return txRepo.UpdateCurrentWorkUnitExecution(ctx, 1, 1, 1, nil) },
	}
	for i, call := range invalidArgCalls {
		if got := status.Code(call()); got != codes.InvalidArgument {
			t.Errorf("invalidArgCalls[%d] code = %v, want InvalidArgument", i, got)
		}
	}

	unimplementedCalls := []func() error{
		func() error { _, err := repo.UpdateWorkUnit(ctx, nil); return err },
		func() error { _, _, err := repo.ListWorkUnits(ctx, "experiments/1", 10, "", "", "", 0); return err },
		func() error { _, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"q"}, 10, ""); return err },
		func() error { _, err := txRepo.FetchUpdatingWorkUnits(ctx, 1, []int64{1}); return err },
		func() error { _, err := txRepo.FetchNextWorkUnitID(ctx, 1); return err },
		func() error { return txRepo.InsertWorkUnit(ctx, nil) },
		func() error {
			_, err := txRepo.ListWorkUnitMessagesBySeverity(ctx, 1, essmsgpb.StructuredMessage_ERROR)
			return err
		},
	}
	for i, call := range unimplementedCalls {
		if got := status.Code(call()); got != codes.Unimplemented {
			t.Errorf("unimplementedCalls[%d] code = %v, want Unimplemented", i, got)
		}
	}
}

func seedWorkUnitWithCurrentExecution(t *testing.T, pool *pgxpool.Pool, repo *Repository, xid, wid int64, ts time.Time) *essmsgpb.WorkUnitExecution {
	t.Helper()
	ctx := t.Context()
	postgrestest.SeedWorkUnit(ctx, t, pool, xid, wid)
	tsProto := timestamppb.New(ts)
	exec := &essmsgpb.WorkUnitExecution{
		Name:       validator.ConstructWorkUnitExecutionName(xid, wid, 1),
		State:      essmsgpb.WorkUnitExecution_ACTIVE,
		Metadata:   &essmsgpb.WorkUnitExecutionMetadata{ResourceName: "jobset-1"},
		CreateTime: tsProto,
		UpdateTime: tsProto,
		Status: &essmsgpb.WorkUnitStatus{
			State:           essmsgpb.WorkUnitStatus_INITIALIZING,
			UpdateTime:      tsProto,
			StateUpdateTime: tsProto,
		},
	}
	err := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		return repo.WithTx(tx).UpdateCurrentWorkUnitExecution(ctx, xid, wid, 1, exec)
	})
	if err != nil {
		t.Fatalf("UpdateCurrentWorkUnitExecution(%d, %d) failed: %v", xid, wid, err)
	}
	return exec
}

func TestRepository_ReadsAndExecutions(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	t.Run("FetchWorkUnit and FetchCurrentWorkUnitExecution", func(t *testing.T) {
		const xid, wid = 101, 1
		wantExec := seedWorkUnitWithCurrentExecution(t, essPool, repo, xid, wid, baseTestTime)
		wantURL := &essmsgpb.Url{Url: "http://test.com", Icon: "test-icon", DisplayName: "test-display-name"}
		urlBytes, err := postgresdata.MarshalProto(wantURL)
		if err != nil {
			t.Fatalf("MarshalProto(Url) failed: %v", err)
		}
		wantExecutable := &essmsgpb.Executable{
			SchedulingConstraints: &essmsgpb.SchedulingConstraints{QueueName: "batch-queue"},
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{Jobs: []*essmsgpb.KubernetesJob{{Name: "test-job"}}},
			},
		}
		execBytes, err := postgresdata.MarshalProto(wantExecutable)
		if err != nil {
			t.Fatalf("MarshalProto(Executable) failed: %v", err)
		}
		createTime := baseTestTime.Add(-time.Hour)
		updateTime := baseTestTime.Add(5 * time.Minute)
		const populateWorkUnitSQL = `UPDATE WorkUnits SET WorkUnitType = $1, Priority = $2, Urls = $3, Tags = $4, Notes = $5, Executable = $6, CreateTime = $7, UpdateTime = $8 WHERE Xid = $9 AND WorkUnitId = $10`
		if _, err := essPool.Exec(ctx, populateWorkUnitSQL, int64(essmsgpb.WorkUnit_STANDARD), int64(200), [][]byte{urlBytes}, []string{"tag-a", "tag-b"}, "unit-notes", execBytes, createTime, updateTime, xid, wid); err != nil {
			t.Fatalf("populate WorkUnits(%d, %d) failed: %v", xid, wid, err)
		}
		wantWU := &essmsgpb.WorkUnit{
			Name:                     validator.ConstructWorkUnitName(xid, wid),
			WorkUnitType:             essmsgpb.WorkUnit_STANDARD,
			Priority:                 200,
			Urls:                     []*essmsgpb.Url{wantURL},
			Tags:                     []string{"tag-a", "tag-b"},
			Notes:                    "unit-notes",
			Executable:               wantExecutable,
			CurrentWorkUnitExecution: wantExec,
			CreateTime:               timestamppb.New(createTime),
			UpdateTime:               timestamppb.New(updateTime),
		}

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
		if diff := cmp.Diff(wantExec, gotExec, protocmp.Transform()); diff != "" {
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
		firstExec := seedWorkUnitWithCurrentExecution(t, essPool, repo, xid, wid, baseTestTime)

		execs, err := repo.ListWorkUnitExecutions(ctx, xid, wid)
		if err != nil || len(execs) != 1 {
			t.Fatalf("ListWorkUnitExecutions() = (%d, %v), want 1 execution", len(execs), err)
		}

		archivedFirst := proto.Clone(firstExec).(*essmsgpb.WorkUnitExecution)
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
		if _, err := repo.ListWorkUnitExecutions(ctx, xid, 999); !errors.Is(err, grpcerr.ErrNotFound) {
			t.Errorf("ListWorkUnitExecutions(missing) = %v, want ErrNotFound", err)
		}
		if err := repo.WithTx(essPool).UpdateCurrentWorkUnitExecution(ctx, xid, 999, 1, firstExec); !errors.Is(err, grpcerr.ErrNotFound) {
			t.Errorf("UpdateCurrentWorkUnitExecution(missing) = %v, want ErrNotFound", err)
		}
		if got := status.Code(repo.WithTx(essPool).SaveWorkUnitExecution(ctx, &essmsgpb.WorkUnitExecution{Name: "invalid"})); got != codes.Internal {
			t.Errorf("SaveWorkUnitExecution(invalid) code = %v, want Internal", got)
		}
	})
}

// Concurrent restart transactions on the same WorkUnit must not lose executions:
// TxRepository.FetchWorkUnit locks the row with FOR NO KEY UPDATE, serializing concurrent writers
// without conflicting with FOR KEY SHARE foreign-key checks.
func TestTxRepository_ConcurrentExecutionRestartsNoLostUpdate(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	const xid, wid, workers = 104, 1, 4
	firstExec := seedWorkUnitWithCurrentExecution(t, essPool, repo, xid, wid, baseTestTime)
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
