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

package operation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	longrunningpb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	anypb "google.golang.org/protobuf/types/known/anypb"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/internal/validator"
)

const (
	testXID int64 = 123
	testWID int64 = 1
)

func newTestRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	_, essPool := postgrestest.Setup(t.Context(), t)
	return NewRepository(essPool), essPool
}

func mustAddOperation(ctx context.Context, t *testing.T, repo *Repository) (*longrunningpb.Operation, string) {
	t.Helper()
	op, err := repo.AddWorkUnitOperation(ctx, testXID, testWID, &essmsgpb.OperationMetadata{
		ActionType: essmsgpb.OperationMetadata_CREATE,
		Target:     validator.ConstructWorkUnitName(testXID, testWID),
	})
	if err != nil {
		t.Fatalf("AddWorkUnitOperation() failed: %v", err)
	}
	_, _, oid, err := validator.ValidateOperationName(op.GetName())
	if err != nil {
		t.Fatalf("ValidateOperationName() failed: %v", err)
	}
	return op, oid
}

func queryUpdateTime(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid, wid int64, oid string) time.Time {
	t.Helper()
	const q = `SELECT UpdateTime FROM Operations WHERE Xid = $1 AND WorkUnitId = $2 AND OperationId = $3`
	var updateTime time.Time
	if err := pool.QueryRow(ctx, q, xid, wid, oid).Scan(&updateTime); err != nil {
		t.Fatalf("QueryRow(UpdateTime) failed: %v", err)
	}
	return updateTime
}

func TestRepository_NilArgumentValidation(t *testing.T) {
	ctx := t.Context()
	repo := NewRepository(nil)

	_, addErr := repo.AddWorkUnitOperation(ctx, testXID, testWID, nil)
	_, nilUpdateErr := repo.UpdateWorkUnitOperation(ctx, nil)
	_, nilOpErr := repo.UpdateWorkUnitOperation(ctx, &storage.OperationUpdate{UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"done"}}})
	for name, err := range map[string]error{
		"AddWorkUnitOperation(nil metadata)":     addErr,
		"UpdateWorkUnitOperation(nil update)":    nilUpdateErr,
		"UpdateWorkUnitOperation(nil operation)": nilOpErr,
	} {
		if !errors.Is(err, grpcerr.ErrInvalidArgument) || status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s = %v (code %v), want %v", name, err, status.Code(err), grpcerr.ErrInvalidArgument)
		}
	}
}

func TestAddWorkUnitOperation(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedWorkUnit(ctx, t, pool, testXID, testWID)

	target := validator.ConstructWorkUnitName(testXID, testWID)
	opMetadata := &essmsgpb.OperationMetadata{
		ActionType: essmsgpb.OperationMetadata_CREATE,
		Target:     target,
	}

	op, err := repo.AddWorkUnitOperation(ctx, testXID, testWID, opMetadata)
	if err != nil {
		t.Fatalf("AddWorkUnitOperation() unexpected error: %v", err)
	}
	if op.GetDone() {
		t.Errorf("op.GetDone() = true, want false")
	}
	wantPrefix := "experiments/123/workUnits/1/operations/"
	if !strings.HasPrefix(op.GetName(), wantPrefix) {
		t.Errorf("op.GetName() = %q, want prefix %q", op.GetName(), wantPrefix)
	}

	gotMetadata := &essmsgpb.OperationMetadata{}
	if err := op.GetMetadata().UnmarshalTo(gotMetadata); err != nil {
		t.Fatalf("op.GetMetadata().UnmarshalTo() failed: %v", err)
	}
	if gotMetadata.GetActionType() != opMetadata.GetActionType() {
		t.Errorf("ActionType = %v, want %v", gotMetadata.GetActionType(), opMetadata.GetActionType())
	}
	if gotMetadata.GetTarget() != target {
		t.Errorf("Target = %q, want %q", gotMetadata.GetTarget(), target)
	}
	if gotMetadata.GetCreateTime() == nil {
		t.Error("CreateTime = nil, want non-nil timestamp")
	}
	if opMetadata.GetCreateTime() != nil {
		t.Errorf("opMetadata was mutated in place; CreateTime = %v, want nil", opMetadata.GetCreateTime())
	}

	t.Run("missing parent work unit returns FailedPrecondition", func(t *testing.T) {
		_, err := repo.AddWorkUnitOperation(ctx, 999, 999, &essmsgpb.OperationMetadata{})
		if !errors.Is(err, grpcerr.ErrFailedPrecondition) || status.Code(err) != codes.FailedPrecondition {
			t.Errorf("AddWorkUnitOperation(missing parent) = %v (code %v), want %v", err, status.Code(err), grpcerr.ErrFailedPrecondition)
		}
	})
}

func TestFetchWorkUnitOperation(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedWorkUnit(ctx, t, pool, testXID, testWID)

	addedOp, oid := mustAddOperation(ctx, t, repo)
	gotOp, err := repo.FetchWorkUnitOperation(ctx, testXID, testWID, oid)
	if err != nil {
		t.Fatalf("FetchWorkUnitOperation() unexpected error: %v", err)
	}
	if diff := cmp.Diff(addedOp, gotOp, protocmp.Transform()); diff != "" {
		t.Errorf("FetchWorkUnitOperation() mismatch (-want +got):\n%s", diff)
	}

	t.Run("NotFound when missing", func(t *testing.T) {
		_, err := repo.FetchWorkUnitOperation(ctx, testXID, testWID, "non-existent-operation")
		if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
			t.Errorf("FetchWorkUnitOperation(missing) = %v (code %v), want %v", err, status.Code(err), grpcerr.ErrNotFound)
		}
	})
}

func TestUpdateWorkUnitOperation(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedWorkUnit(ctx, t, pool, testXID, testWID)

	responseAny, err := anypb.New(&essmsgpb.WorkUnit{Name: "experiments/123/workUnits/1"})
	if err != nil {
		t.Fatalf("anypb.New(WorkUnit) failed: %v", err)
	}
	updatedMetaAny, err := anypb.New(&essmsgpb.OperationMetadata{
		ActionType: essmsgpb.OperationMetadata_STOP,
		Target:     "experiments/123/workUnits/1",
	})
	if err != nil {
		t.Fatalf("anypb.New(OperationMetadata) failed: %v", err)
	}
	errStatus := &statuspb.Status{Code: int32(codes.Internal), Message: "execution failed"}

	t.Run("updates done and response", func(t *testing.T) {
		_, oid := mustAddOperation(ctx, t, repo)
		updatedOp, err := repo.UpdateWorkUnitOperation(ctx, &storage.OperationUpdate{
			XID: testXID,
			WID: testWID,
			OID: oid,
			Operation: &longrunningpb.Operation{
				Done:   true,
				Result: &longrunningpb.Operation_Response{Response: responseAny},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"done", "response"}},
		})
		if err != nil {
			t.Fatalf("UpdateWorkUnitOperation() failed: %v", err)
		}
		if !updatedOp.GetDone() || updatedOp.GetError() != nil {
			t.Errorf("updatedOp Done=%v Error=%v, want Done=true Error=nil", updatedOp.GetDone(), updatedOp.GetError())
		}
		if diff := cmp.Diff(responseAny, updatedOp.GetResponse(), protocmp.Transform()); diff != "" {
			t.Errorf("updatedOp.GetResponse() mismatch (-want +got):\n%s", diff)
		}

		gotOp, err := repo.FetchWorkUnitOperation(ctx, testXID, testWID, oid)
		if err != nil {
			t.Fatalf("FetchWorkUnitOperation() failed: %v", err)
		}
		if diff := cmp.Diff(updatedOp, gotOp, protocmp.Transform()); diff != "" {
			t.Errorf("FetchWorkUnitOperation() after update mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("updates done and error status", func(t *testing.T) {
		_, oid := mustAddOperation(ctx, t, repo)
		updatedOp, err := repo.UpdateWorkUnitOperation(ctx, &storage.OperationUpdate{
			XID: testXID,
			WID: testWID,
			OID: oid,
			Operation: &longrunningpb.Operation{
				Done:   true,
				Result: &longrunningpb.Operation_Error{Error: errStatus},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"done", "error"}},
		})
		if err != nil {
			t.Fatalf("UpdateWorkUnitOperation() failed: %v", err)
		}
		if !updatedOp.GetDone() || updatedOp.GetResponse() != nil {
			t.Errorf("updatedOp Done=%v Response=%v, want Done=true Response=nil", updatedOp.GetDone(), updatedOp.GetResponse())
		}
		if diff := cmp.Diff(errStatus, updatedOp.GetError(), protocmp.Transform()); diff != "" {
			t.Errorf("updatedOp.GetError() mismatch (-want +got):\n%s", diff)
		}

		gotOp, err := repo.FetchWorkUnitOperation(ctx, testXID, testWID, oid)
		if err != nil {
			t.Fatalf("FetchWorkUnitOperation() failed: %v", err)
		}
		if diff := cmp.Diff(updatedOp, gotOp, protocmp.Transform()); diff != "" {
			t.Errorf("FetchWorkUnitOperation() after error update mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("skips write on no-op update and preserves UpdateTime", func(t *testing.T) {
		op, oid := mustAddOperation(ctx, t, repo)
		initialUpdateTime := queryUpdateTime(ctx, t, pool, testXID, testWID, oid)

		// Updating done to false when it is already false is a no-op.
		noopOp, err := repo.UpdateWorkUnitOperation(ctx, &storage.OperationUpdate{
			XID:        testXID,
			WID:        testWID,
			OID:        oid,
			Operation:  &longrunningpb.Operation{Done: false},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"done"}},
		})
		if err != nil {
			t.Fatalf("UpdateWorkUnitOperation(no-op) failed: %v", err)
		}
		if diff := cmp.Diff(op, noopOp, protocmp.Transform()); diff != "" {
			t.Errorf("UpdateWorkUnitOperation(no-op) mismatch (-want +got):\n%s", diff)
		}
		if gotUpdateTime := queryUpdateTime(ctx, t, pool, testXID, testWID, oid); !gotUpdateTime.Equal(initialUpdateTime) {
			t.Errorf("UpdateTime after no-op = %v, want unchanged %v", gotUpdateTime, initialUpdateTime)
		}

		// Updating metadata to a different value performs the write and advances UpdateTime.
		if _, err := repo.UpdateWorkUnitOperation(ctx, &storage.OperationUpdate{
			XID:        testXID,
			WID:        testWID,
			OID:        oid,
			Operation:  &longrunningpb.Operation{Metadata: updatedMetaAny},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata"}},
		}); err != nil {
			t.Fatalf("UpdateWorkUnitOperation(metadata) failed: %v", err)
		}
		if gotUpdateTime := queryUpdateTime(ctx, t, pool, testXID, testWID, oid); !gotUpdateTime.After(initialUpdateTime) {
			t.Errorf("UpdateTime after real update = %v, want after %v", gotUpdateTime, initialUpdateTime)
		}
	})

	t.Run("NotFound and invalid update mask errors", func(t *testing.T) {
		_, err := repo.UpdateWorkUnitOperation(ctx, &storage.OperationUpdate{
			XID:        testXID,
			WID:        testWID,
			OID:        "missing-oid",
			Operation:  &longrunningpb.Operation{Done: true},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"done"}},
		})
		if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
			t.Errorf("UpdateWorkUnitOperation(missing) = %v (code %v), want %v", err, status.Code(err), grpcerr.ErrNotFound)
		}

		_, oid := mustAddOperation(ctx, t, repo)
		_, err = repo.UpdateWorkUnitOperation(ctx, &storage.OperationUpdate{
			XID:        testXID,
			WID:        testWID,
			OID:        oid,
			Operation:  &longrunningpb.Operation{Done: true},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"invalid_field"}},
		})
		if !errors.Is(err, grpcerr.ErrInvalidArgument) {
			t.Errorf("UpdateWorkUnitOperation(invalid mask) = %v, want %v", err, grpcerr.ErrInvalidArgument)
		}
	})

	t.Run("nil or empty update mask returns InvalidArgument", func(t *testing.T) {
		_, oid := mustAddOperation(ctx, t, repo)
		_, err := repo.UpdateWorkUnitOperation(ctx, &storage.OperationUpdate{
			XID:        testXID,
			WID:        testWID,
			OID:        oid,
			Operation:  &longrunningpb.Operation{Done: true},
			UpdateMask: nil,
		})
		if !errors.Is(err, grpcerr.ErrInvalidArgument) {
			t.Errorf("UpdateWorkUnitOperation(nil mask) = %v, want %v", err, grpcerr.ErrInvalidArgument)
		}
	})
}

// Concurrent updates of disjoint fields on the same operation must not overwrite each other:
// UpdateWorkUnitOperation locks the row with FOR NO KEY UPDATE inside pgdb.RunInTx before reading
// and merging, so all concurrent updates are preserved.
func TestUpdateWorkUnitOperation_ConcurrentUpdatesNoLostUpdate(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedWorkUnit(ctx, t, pool, testXID, testWID)

	op, oid := mustAddOperation(ctx, t, repo)
	wantMetadata, err := anypb.New(&essmsgpb.OperationMetadata{
		ActionType: essmsgpb.OperationMetadata_RESTART,
		Target:     "experiments/123/workUnits/1",
	})
	if err != nil {
		t.Fatalf("anypb.New(OperationMetadata) failed: %v", err)
	}
	wantResponse, err := anypb.New(&essmsgpb.WorkUnit{Name: "experiments/123/workUnits/1", Notes: "completed"})
	if err != nil {
		t.Fatalf("anypb.New(WorkUnit) failed: %v", err)
	}

	// Hold a FOR NO KEY UPDATE row lock until all worker goroutines have started and queued on the
	// row lock, guaranteeing that they contend simultaneously when the lock is released.
	holdTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin() failed: %v", err)
	}
	defer func() { _ = holdTx.Rollback(context.Background()) }()
	if _, err := holdTx.Exec(ctx, lockOperationSQL.String(), testXID, testWID, oid); err != nil {
		t.Fatalf("holdTx lock failed: %v", err)
	}

	updates := []*storage.OperationUpdate{
		{
			XID:        testXID,
			WID:        testWID,
			OID:        oid,
			Operation:  &longrunningpb.Operation{Done: true},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"done"}},
		},
		{
			XID:        testXID,
			WID:        testWID,
			OID:        oid,
			Operation:  &longrunningpb.Operation{Metadata: wantMetadata},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata"}},
		},
		{
			XID:        testXID,
			WID:        testWID,
			OID:        oid,
			Operation:  &longrunningpb.Operation{Result: &longrunningpb.Operation_Response{Response: wantResponse}},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"response"}},
		},
	}

	errs := make([]error, len(updates))
	var started sync.WaitGroup
	var done sync.WaitGroup
	for i, u := range updates {
		started.Add(1)
		done.Go(func() {
			started.Done()
			_, errs[i] = repo.UpdateWorkUnitOperation(ctx, u)
		})
	}
	started.Wait()
	time.Sleep(20 * time.Millisecond)
	if err := holdTx.Commit(ctx); err != nil {
		t.Fatalf("holdTx.Commit() failed: %v", err)
	}
	done.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("concurrent UpdateWorkUnitOperation() failed: %v", err)
	}

	gotOp, err := repo.FetchWorkUnitOperation(ctx, testXID, testWID, oid)
	if err != nil {
		t.Fatalf("FetchWorkUnitOperation() failed: %v", err)
	}
	wantOp := &longrunningpb.Operation{
		Name:     op.GetName(),
		Done:     true,
		Metadata: wantMetadata,
		Result:   &longrunningpb.Operation_Response{Response: wantResponse},
	}
	if diff := cmp.Diff(wantOp, gotOp, protocmp.Transform()); diff != "" {
		t.Errorf("final operation after concurrent updates mismatch (-want +got); lost update detected:\n%s", diff)
	}
}
