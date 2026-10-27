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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
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
		func() error { _, err := txRepo.FetchUpdatingWorkUnits(ctx, 1, []int64{1}); return err },
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

		gotWU, err := repo.FetchWorkUnit(ctx, xid, wid)
		if err != nil {
			t.Fatalf("FetchWorkUnit() failed: %v", err)
		}
		if gotWU.GetName() != validator.ConstructWorkUnitName(xid, wid) {
			t.Errorf("FetchWorkUnit().Name = %q, want %q", gotWU.GetName(), validator.ConstructWorkUnitName(xid, wid))
		}
		if diff := cmp.Diff(wantExec, gotWU.GetCurrentWorkUnitExecution(), protocmp.Transform()); diff != "" {
			t.Errorf("CurrentWorkUnitExecution mismatch (-want +got):\n%s", diff)
		}

		err = pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
			txWU, err := repo.WithTx(tx).FetchWorkUnit(ctx, xid, wid)
			if err != nil {
				return err
			}
			if diff := cmp.Diff(gotWU, txWU, protocmp.Transform()); diff != "" {
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

		secondTime := timestamppb.New(baseTestTime.Add(time.Minute))
		err = pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
			txRepo := repo.WithTx(tx)
			archived := proto.Clone(firstExec).(*essmsgpb.WorkUnitExecution)
			archived.State = essmsgpb.WorkUnitExecution_INACTIVE
			if err := txRepo.SaveWorkUnitExecution(ctx, archived); err != nil {
				return err
			}
			secondExec := &essmsgpb.WorkUnitExecution{
				Name:       validator.ConstructWorkUnitExecutionName(xid, wid, 2),
				State:      essmsgpb.WorkUnitExecution_ACTIVE,
				Metadata:   &essmsgpb.WorkUnitExecutionMetadata{},
				Status:     &essmsgpb.WorkUnitStatus{State: essmsgpb.WorkUnitStatus_RUNNING, UpdateTime: secondTime},
				CreateTime: secondTime,
				UpdateTime: secondTime,
			}
			return txRepo.UpdateCurrentWorkUnitExecution(ctx, xid, wid, 2, secondExec)
		})
		if err != nil {
			t.Fatalf("SaveWorkUnitExecution + UpdateCurrentWorkUnitExecution failed: %v", err)
		}

		execs, err = repo.ListWorkUnitExecutions(ctx, xid, wid)
		if err != nil || len(execs) != 2 {
			t.Fatalf("ListWorkUnitExecutions() after restart = (%d, %v), want 2", len(execs), err)
		}
		if execs[0].GetState() != essmsgpb.WorkUnitExecution_ACTIVE || execs[1].GetState() != essmsgpb.WorkUnitExecution_INACTIVE {
			t.Errorf("states = (%v, %v), want (ACTIVE, INACTIVE)", execs[0].GetState(), execs[1].GetState())
		}
		if _, err := repo.ListWorkUnitExecutions(ctx, xid, 999); !errors.Is(err, grpcerr.ErrNotFound) {
			t.Errorf("ListWorkUnitExecutions(missing) = %v, want ErrNotFound", err)
		}
	})

	t.Run("FetchUpdatingWorkUnits", func(t *testing.T) {
		const xid = 103
		seedWorkUnitWithCurrentExecution(t, essPool, repo, xid, 1, baseTestTime)
		seedWorkUnitWithCurrentExecution(t, essPool, repo, xid, 2, baseTestTime)

		err := pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
			txRepo := repo.WithTx(tx)
			empty, err := txRepo.FetchUpdatingWorkUnits(ctx, xid, nil)
			if err != nil || len(empty) != 0 {
				t.Fatalf("FetchUpdatingWorkUnits(nil) = (%v, %v), want empty", empty, err)
			}
			got, err := txRepo.FetchUpdatingWorkUnits(ctx, xid, []int64{2, 1, 999})
			if err != nil {
				return err
			}
			if len(got) != 2 || got[1] == nil || got[2] == nil {
				t.Fatalf("FetchUpdatingWorkUnits() returned %v, want keys 1 and 2", got)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("FetchUpdatingWorkUnits failed: %v", err)
		}
	})
}
