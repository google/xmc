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
	"fmt"
	"math"
	"sort"
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
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/internal/validator"
)

func setupTestRepo(t *testing.T) (*pgxpool.Pool, *Repository) {
	t.Helper()
	_, essPool := postgrestest.Setup(t.Context(), t)
	return essPool, NewRepository(essPool)
}

func seedExperiment(t *testing.T, pool *pgxpool.Pool, xid int64, creator string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	const insertExpSQL = `INSERT INTO Experiments (
		Xid, Title, Creator, Priority, Urls, Tags, Notes, Resources, Status, ActiveState, LaunchState, CreateTime, UpdateTime
	) VALUES ($1, $2, $3, 0, '{}', '{}', '', '{}', $4, 0, 0, $5, $5)`
	if _, err := pool.Exec(t.Context(), insertExpSQL, xid, fmt.Sprintf("exp-%d", xid), creator, []byte{}, now); err != nil {
		t.Fatalf("seedExperiment(%d) failed: %v", xid, err)
	}
}

func makeSampleWorkUnit(xid, wid int64, baseTime time.Time) *essmsgpb.WorkUnit {
	baseTime = baseTime.UTC().Truncate(time.Microsecond)
	createTS := timestamppb.New(baseTime)
	updateTS := timestamppb.New(baseTime.Add(time.Minute))
	return &essmsgpb.WorkUnit{
		Name:         validator.ConstructWorkUnitName(xid, wid),
		WorkUnitType: essmsgpb.WorkUnit_STANDARD,
		Priority:     essmsgpb.Priority_NORMAL,
		Urls: []*essmsgpb.Url{
			{DisplayName: "logs", Url: "http://example.com/logs"},
		},
		Tags:  []string{"tag-a", "tag-b"},
		Notes: "initial notes",
		Executable: &essmsgpb.Executable{
			SchedulingConstraints: &essmsgpb.SchedulingConstraints{
				QueueName: "default-queue",
			},
		},
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: validator.ConstructWorkUnitExecutionName(xid, wid, 1),
			Metadata: &essmsgpb.WorkUnitExecutionMetadata{
				Type:         essmsgpb.WorkUnitExecutionMetadata_JOB_SET,
				ResourceName: "job-1",
				Namespace:    "default",
			},
			Status: &essmsgpb.WorkUnitStatus{
				State:      essmsgpb.WorkUnitStatus_RUNNING,
				UpdateTime: updateTS,
				SchedulingInformation: &essmsgpb.StructuredMessage{
					AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
						Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{
							QueueInfo: &essmsgpb.QueueInfo{PositionInQueue: 3},
						},
					},
				},
			},
			CreateTime: createTS,
			UpdateTime: updateTS,
			State:      essmsgpb.WorkUnitExecution_ACTIVE,
		},
		CreateTime: createTS,
		UpdateTime: updateTS,
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

func TestFetchWorkUnit_Integration(t *testing.T) {
	pool, repo := setupTestRepo(t)
	ctx := t.Context()
	xid := int64(100)
	wid := int64(1)
	seedExperiment(t, pool, xid, "alice")

	baseTime := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	want := makeSampleWorkUnit(xid, wid, baseTime)
	mustInsertWorkUnit(t, pool, repo, want)

	t.Run("non-transactional FetchWorkUnit happy path", func(t *testing.T) {
		got, err := repo.FetchWorkUnit(ctx, xid, wid)
		if err != nil {
			t.Fatalf("FetchWorkUnit() unexpected error: %v", err)
		}
		if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
			t.Errorf("FetchWorkUnit() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("transactional FetchWorkUnit happy path", func(t *testing.T) {
		var got *essmsgpb.WorkUnit
		err := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			var fetchErr error
			got, fetchErr = repo.WithTx(tx).FetchWorkUnit(ctx, xid, wid)
			return fetchErr
		})
		if err != nil {
			t.Fatalf("TxRepository.FetchWorkUnit() unexpected error: %v", err)
		}
		if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
			t.Errorf("TxRepository.FetchWorkUnit() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("non-existent XID returns NotFound", func(t *testing.T) {
		_, err := repo.FetchWorkUnit(ctx, 999, wid)
		if status.Code(err) != codes.NotFound {
			t.Errorf("FetchWorkUnit(non-existent XID) code = %v, want %v (err: %v)", status.Code(err), codes.NotFound, err)
		}
	})

	t.Run("non-existent WID returns NotFound", func(t *testing.T) {
		_, err := repo.FetchWorkUnit(ctx, xid, 999)
		if status.Code(err) != codes.NotFound {
			t.Errorf("FetchWorkUnit(non-existent WID) code = %v, want %v (err: %v)", status.Code(err), codes.NotFound, err)
		}
	})
}

func TestFetchUpdatingWorkUnits_Integration(t *testing.T) {
	pool, repo := setupTestRepo(t)
	ctx := t.Context()
	xid := int64(101)
	seedExperiment(t, pool, xid, "alice")

	baseTime := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	wu1 := makeSampleWorkUnit(xid, 1, baseTime)
	wu2 := makeSampleWorkUnit(xid, 2, baseTime.Add(time.Minute))
	mustInsertWorkUnit(t, pool, repo, wu1)
	mustInsertWorkUnit(t, pool, repo, wu2)

	var got map[int64]*essmsgpb.WorkUnit
	err := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		var fetchErr error
		got, fetchErr = repo.WithTx(tx).FetchUpdatingWorkUnits(ctx, xid, []int64{2, 999, 1})
		return fetchErr
	})
	if err != nil {
		t.Fatalf("FetchUpdatingWorkUnits() unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("FetchUpdatingWorkUnits() returned %d items, want 2", len(got))
	}

	// Spanner FetchUpdatingWorkUnits populates Name, Executable, and CurrentWorkUnitExecution with INACTIVE state.
	for _, src := range []*essmsgpb.WorkUnit{wu1, wu2} {
		_, wid, _ := validator.ValidateWorkUnitName(src.GetName())
		wantExec := proto.Clone(src.GetCurrentWorkUnitExecution()).(*essmsgpb.WorkUnitExecution)
		wantExec.State = essmsgpb.WorkUnitExecution_INACTIVE
		wantWU := &essmsgpb.WorkUnit{
			Name:                     src.GetName(),
			Executable:               src.GetExecutable(),
			CurrentWorkUnitExecution: wantExec,
		}
		if diff := cmp.Diff(wantWU, got[wid], protocmp.Transform()); diff != "" {
			t.Errorf("FetchUpdatingWorkUnits()[%d] mismatch (-want +got):\n%s", wid, diff)
		}
	}
}

func TestFetchCurrentWorkUnitExecution_Integration(t *testing.T) {
	pool, repo := setupTestRepo(t)
	ctx := t.Context()
	xid := int64(102)
	wid := int64(1)
	seedExperiment(t, pool, xid, "alice")

	baseTime := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	wu := makeSampleWorkUnit(xid, wid, baseTime)
	wu.CurrentWorkUnitExecution.CompleteTime = timestamppb.New(baseTime.Add(2 * time.Minute))
	mustInsertWorkUnit(t, pool, repo, wu)

	t.Run("happy path matches Spanner current execution projection", func(t *testing.T) {
		got, err := repo.FetchCurrentWorkUnitExecution(ctx, xid, wid)
		if err != nil {
			t.Fatalf("FetchCurrentWorkUnitExecution() unexpected error: %v", err)
		}
		want := proto.Clone(wu.GetCurrentWorkUnitExecution()).(*essmsgpb.WorkUnitExecution)
		want.State = essmsgpb.WorkUnitExecution_ACTIVE
		want.UpdateTime = want.GetStatus().GetUpdateTime()
		want.CompleteTime = nil
		if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
			t.Errorf("FetchCurrentWorkUnitExecution() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("non-existent work unit returns NotFound", func(t *testing.T) {
		_, err := repo.FetchCurrentWorkUnitExecution(ctx, xid, 999)
		if status.Code(err) != codes.NotFound {
			t.Errorf("FetchCurrentWorkUnitExecution() code = %v, want %v", status.Code(err), codes.NotFound)
		}
	})
}

func TestListWorkUnitExecutions_AndSaveUpdate_Integration(t *testing.T) {
	pool, repo := setupTestRepo(t)
	ctx := t.Context()
	xid := int64(103)
	wid := int64(1)
	seedExperiment(t, pool, xid, "alice")

	baseTime := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	wu := makeSampleWorkUnit(xid, wid, baseTime)
	mustInsertWorkUnit(t, pool, repo, wu)

	exec1 := proto.Clone(wu.GetCurrentWorkUnitExecution()).(*essmsgpb.WorkUnitExecution)
	exec1.CompleteTime = timestamppb.New(baseTime.Add(2 * time.Minute))
	exec1.State = essmsgpb.WorkUnitExecution_INACTIVE

	exec2 := proto.Clone(wu.GetCurrentWorkUnitExecution()).(*essmsgpb.WorkUnitExecution)
	exec2.Name = validator.ConstructWorkUnitExecutionName(xid, wid, 2)
	exec2.CreateTime = timestamppb.New(baseTime.Add(3 * time.Minute))
	exec2.UpdateTime = timestamppb.New(baseTime.Add(4 * time.Minute))
	exec2.Status.State = essmsgpb.WorkUnitStatus_COMPLETED
	exec2.Status.UpdateTime = exec2.UpdateTime
	exec2.State = essmsgpb.WorkUnitExecution_ACTIVE

	err := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		txRepo := repo.WithTx(tx)
		if err := txRepo.SaveWorkUnitExecution(ctx, exec1); err != nil {
			return err
		}
		return txRepo.UpdateCurrentWorkUnitExecution(ctx, xid, wid, 2, exec2)
	})
	if err != nil {
		t.Fatalf("archiving exec1 and updating to exec2 failed: %v", err)
	}

	got, err := repo.ListWorkUnitExecutions(ctx, xid, wid)
	if err != nil {
		t.Fatalf("ListWorkUnitExecutions() unexpected error: %v", err)
	}
	want := []*essmsgpb.WorkUnitExecution{exec2, exec1}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListWorkUnitExecutions() mismatch (-want +got):\n%s", diff)
	}

	t.Run("duplicate SaveWorkUnitExecution returns AlreadyExists", func(t *testing.T) {
		dupErr := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			return repo.WithTx(tx).SaveWorkUnitExecution(ctx, exec1)
		})
		if status.Code(dupErr) != codes.AlreadyExists {
			t.Errorf("SaveWorkUnitExecution(duplicate) code = %v, want %v (err: %v)", status.Code(dupErr), codes.AlreadyExists, dupErr)
		}
	})

	t.Run("UpdateCurrentWorkUnitExecution for missing work unit returns NotFound", func(t *testing.T) {
		nfErr := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			return repo.WithTx(tx).UpdateCurrentWorkUnitExecution(ctx, xid, 999, 1, exec2)
		})
		if status.Code(nfErr) != codes.NotFound {
			t.Errorf("UpdateCurrentWorkUnitExecution(missing) code = %v, want %v (err: %v)", status.Code(nfErr), codes.NotFound, nfErr)
		}
	})

	t.Run("ListWorkUnitExecutions for missing work unit returns NotFound", func(t *testing.T) {
		_, nfErr := repo.ListWorkUnitExecutions(ctx, xid, 999)
		if status.Code(nfErr) != codes.NotFound {
			t.Errorf("ListWorkUnitExecutions(missing) code = %v, want %v (err: %v)", status.Code(nfErr), codes.NotFound, nfErr)
		}
	})
}

func TestFetchNextWorkUnitID_AndInsertDerivedColumns_Integration(t *testing.T) {
	pool, repo := setupTestRepo(t)
	ctx := t.Context()
	xid1 := int64(201)
	xid2 := int64(202)
	seedExperiment(t, pool, xid1, "alice")
	seedExperiment(t, pool, xid2, "bob")

	fetchNext := func(targetXID int64) (int64, error) {
		var nextID int64
		err := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			var fetchErr error
			nextID, fetchErr = repo.WithTx(tx).FetchNextWorkUnitID(ctx, targetXID)
			return fetchErr
		})
		return nextID, err
	}

	t.Run("non-existent experiment returns NotFound", func(t *testing.T) {
		if _, err := fetchNext(999); status.Code(err) != codes.NotFound {
			t.Errorf("FetchNextWorkUnitID(missing experiment) code = %v, want %v", status.Code(err), codes.NotFound)
		}
		wu := makeSampleWorkUnit(999, 1, time.Now())
		err := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			return repo.WithTx(tx).InsertWorkUnit(ctx, wu)
		})
		if status.Code(err) != codes.NotFound {
			t.Errorf("InsertWorkUnit(missing experiment) code = %v, want %v", status.Code(err), codes.NotFound)
		}
	})

	t.Run("allocates sequential IDs per experiment and rejects duplicates", func(t *testing.T) {
		id, err := fetchNext(xid1)
		if err != nil || id != 1 {
			t.Fatalf("FetchNextWorkUnitID(empty) = %d, %v; want 1, nil", id, err)
		}

		wu1 := makeSampleWorkUnit(xid1, 1, time.Now())
		// Clear queue position to verify default PositionInQueue = MaxInt64.
		wu1.CurrentWorkUnitExecution.Status.SchedulingInformation = nil
		wu1.CurrentWorkUnitExecution.Status.SystemMessage = &essmsgpb.StructuredMessage{
			UserFacingText: "sys warn",
			Severity:       essmsgpb.StructuredMessage_WARNING,
		}
		wu1.CurrentWorkUnitExecution.Status.UserMessage = &essmsgpb.StructuredMessage{
			UserFacingText: "usr err",
			Severity:       essmsgpb.StructuredMessage_ERROR,
		}
		mustInsertWorkUnit(t, pool, repo, wu1)

		// Verify derived columns in database.
		var queue string
		var state, severity, position int64
		const checkSQL = `SELECT QueueName, ExecutionState, ExecutionStatusMessageSeverity, PositionInQueue FROM WorkUnits WHERE Xid = $1 AND WorkUnitId = $2`
		if err := pool.QueryRow(ctx, checkSQL, xid1, int64(1)).Scan(&queue, &state, &severity, &position); err != nil {
			t.Fatalf("querying derived columns failed: %v", err)
		}
		if queue != "default-queue" || state != int64(essmsgpb.WorkUnitStatus_RUNNING) || severity != int64(essmsgpb.StructuredMessage_ERROR) || position != math.MaxInt64 {
			t.Errorf("derived columns = (%q, %d, %d, %d), want (\"default-queue\", %d, %d, %d)",
				queue, state, severity, position,
				essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.StructuredMessage_ERROR, int64(math.MaxInt64))
		}

		if id, err = fetchNext(xid1); err != nil || id != 2 {
			t.Fatalf("FetchNextWorkUnitID(after 1) = %d, %v; want 2, nil", id, err)
		}
		mustInsertWorkUnit(t, pool, repo, makeSampleWorkUnit(xid1, 10, time.Now()))
		if id, err = fetchNext(xid1); err != nil || id != 11 {
			t.Fatalf("FetchNextWorkUnitID(after 10) = %d, %v; want 11, nil", id, err)
		}
		if id, err = fetchNext(xid2); err != nil || id != 1 {
			t.Fatalf("FetchNextWorkUnitID(other experiment) = %d, %v; want 1, nil", id, err)
		}

		dupErr := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			return repo.WithTx(tx).InsertWorkUnit(ctx, wu1)
		})
		if status.Code(dupErr) != codes.AlreadyExists {
			t.Errorf("InsertWorkUnit(duplicate) code = %v, want %v", status.Code(dupErr), codes.AlreadyExists)
		}
	})
}

func TestUpdateWorkUnit_Integration(t *testing.T) {
	pool, repo := setupTestRepo(t)
	ctx := t.Context()
	xid := int64(301)
	wid := int64(1)
	seedExperiment(t, pool, xid, "alice")

	baseTime := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	initial := makeSampleWorkUnit(xid, wid, baseTime)
	mustInsertWorkUnit(t, pool, repo, initial)

	t.Run("updates masked fields and advances UpdateTime", func(t *testing.T) {
		update := &storage.WorkUnitUpdate{
			XID: xid,
			WID: wid,
			WorkUnit: &essmsgpb.WorkUnit{
				Priority: essmsgpb.Priority_HIGH,
				Notes:    "updated notes",
				Tags:     []string{"new-tag"},
				Urls:     []*essmsgpb.Url{{DisplayName: "new", Url: "http://example.com/new"}},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority", "notes", "tags", "urls"}},
		}
		res, err := repo.UpdateWorkUnit(ctx, update)
		if err != nil {
			t.Fatalf("UpdateWorkUnit() unexpected error: %v", err)
		}
		if diff := cmp.Diff(initial, res.Original, protocmp.Transform()); diff != "" {
			t.Errorf("UpdateWorkUnit().Original mismatch (-want +got):\n%s", diff)
		}
		if !res.Updated.GetUpdateTime().AsTime().After(initial.GetUpdateTime().AsTime()) {
			t.Errorf("UpdateWorkUnit().Updated.UpdateTime = %v, want after %v", res.Updated.GetUpdateTime().AsTime(), initial.GetUpdateTime().AsTime())
		}

		stored, err := repo.FetchWorkUnit(ctx, xid, wid)
		if err != nil {
			t.Fatalf("FetchWorkUnit() unexpected error: %v", err)
		}
		if diff := cmp.Diff(res.Updated, stored, protocmp.Transform()); diff != "" {
			t.Errorf("stored work unit mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("no-op update skips write and preserves UpdateTime", func(t *testing.T) {
		before, err := repo.FetchWorkUnit(ctx, xid, wid)
		if err != nil {
			t.Fatalf("FetchWorkUnit() failed: %v", err)
		}
		res, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{
			XID:        xid,
			WID:        wid,
			WorkUnit:   &essmsgpb.WorkUnit{Notes: before.GetNotes()},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
		})
		if err != nil {
			t.Fatalf("UpdateWorkUnit(no-op) failed: %v", err)
		}
		if diff := cmp.Diff(before, res.Updated, protocmp.Transform()); diff != "" {
			t.Errorf("UpdateWorkUnit(no-op) changed work unit (-want +got):\n%s", diff)
		}
	})

	t.Run("immutable field returns InvalidArgument", func(t *testing.T) {
		_, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{
			XID:        xid,
			WID:        wid,
			WorkUnit:   &essmsgpb.WorkUnit{Name: "experiments/301/workUnits/999"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("UpdateWorkUnit(immutable) code = %v, want %v", status.Code(err), codes.InvalidArgument)
		}
	})

	t.Run("empty mask returns InvalidArgument", func(t *testing.T) {
		_, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{
			XID:        xid,
			WID:        wid,
			WorkUnit:   &essmsgpb.WorkUnit{Notes: "x"},
			UpdateMask: &fieldmaskpb.FieldMask{},
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("UpdateWorkUnit(empty mask) code = %v, want %v", status.Code(err), codes.InvalidArgument)
		}
	})

	t.Run("non-existent work unit returns NotFound", func(t *testing.T) {
		_, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{
			XID:        xid,
			WID:        999,
			WorkUnit:   &essmsgpb.WorkUnit{Notes: "x"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
		})
		if status.Code(err) != codes.NotFound {
			t.Errorf("UpdateWorkUnit(missing) code = %v, want %v", status.Code(err), codes.NotFound)
		}
	})
}

func TestListWorkUnitMessagesBySeverity_Integration(t *testing.T) {
	pool, repo := setupTestRepo(t)
	ctx := t.Context()
	xid1 := int64(401)
	xid2 := int64(402)
	seedExperiment(t, pool, xid1, "alice")
	seedExperiment(t, pool, xid2, "bob")

	msg := func(text string, sev essmsgpb.StructuredMessage_Severity) *essmsgpb.StructuredMessage {
		return &essmsgpb.StructuredMessage{UserFacingText: text, Severity: sev}
	}
	insertWithMsgs := func(xid, wid int64, sysMsg, usrMsg *essmsgpb.StructuredMessage) {
		wu := makeSampleWorkUnit(xid, wid, time.Now())
		wu.CurrentWorkUnitExecution.Status.SystemMessage = sysMsg
		wu.CurrentWorkUnitExecution.Status.UserMessage = usrMsg
		mustInsertWorkUnit(t, pool, repo, wu)
	}

	sysInfo := msg("sys-info", essmsgpb.StructuredMessage_INFO)
	usrError := msg("usr-error", essmsgpb.StructuredMessage_ERROR)
	sysWarn := msg("sys-warn", essmsgpb.StructuredMessage_WARNING)
	usrWarn := msg("usr-warn", essmsgpb.StructuredMessage_WARNING)
	sysFatal := msg("sys-fatal", essmsgpb.StructuredMessage_FATAL)
	otherErr := msg("other-exp-err", essmsgpb.StructuredMessage_ERROR)

	insertWithMsgs(xid1, 1, sysInfo, usrError) // row severity = ERROR
	insertWithMsgs(xid1, 2, sysWarn, usrWarn)  // row severity = WARNING
	insertWithMsgs(xid1, 3, sysFatal, nil)     // row severity = FATAL
	insertWithMsgs(xid1, 4, sysInfo, nil)      // row severity = INFO
	insertWithMsgs(xid2, 1, otherErr, nil)     // different experiment

	querySev := func(targetXID int64, sev essmsgpb.StructuredMessage_Severity) []*essmsgpb.StructuredMessage {
		var out []*essmsgpb.StructuredMessage
		err := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			var listErr error
			out, listErr = repo.WithTx(tx).ListWorkUnitMessagesBySeverity(ctx, targetXID, sev)
			return listErr
		})
		if err != nil {
			t.Fatalf("ListWorkUnitMessagesBySeverity(%d, %v) failed: %v", targetXID, sev, err)
		}
		return out
	}

	sortMsgs := protocmp.SortRepeated(func(a, b *essmsgpb.StructuredMessage) bool {
		return a.GetUserFacingText() < b.GetUserFacingText()
	})
	if diff := cmp.Diff([]*essmsgpb.StructuredMessage{usrError}, querySev(xid1, essmsgpb.StructuredMessage_ERROR), protocmp.Transform(), sortMsgs); diff != "" {
		t.Errorf("ERROR messages mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]*essmsgpb.StructuredMessage{sysWarn, usrWarn}, querySev(xid1, essmsgpb.StructuredMessage_WARNING), protocmp.Transform(), sortMsgs); diff != "" {
		t.Errorf("WARNING messages mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]*essmsgpb.StructuredMessage{sysFatal}, querySev(xid1, essmsgpb.StructuredMessage_FATAL), protocmp.Transform(), sortMsgs); diff != "" {
		t.Errorf("FATAL messages mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]*essmsgpb.StructuredMessage{sysInfo}, querySev(xid1, essmsgpb.StructuredMessage_INFO), protocmp.Transform(), sortMsgs); diff != "" {
		t.Errorf("INFO messages mismatch (-want +got):\n%s", diff)
	}
}

func seedListWorkUnitsFixture(t *testing.T, pool *pgxpool.Pool, repo *Repository, xid int64) []*essmsgpb.WorkUnit {
	t.Helper()
	seedExperiment(t, pool, xid, "alice")
	now := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	states := []essmsgpb.WorkUnitStatus_WorkUnitState{
		essmsgpb.WorkUnitStatus_RUNNING,
		essmsgpb.WorkUnitStatus_COMPLETED,
		essmsgpb.WorkUnitStatus_RUNNING,
		essmsgpb.WorkUnitStatus_FAILED,
		essmsgpb.WorkUnitStatus_SCHEDULING,
	}
	completeTimes := []*timestamppb.Timestamp{
		nil,
		timestamppb.New(now.Add(2 * time.Hour)),
		nil,
		timestamppb.New(now.Add(2 * time.Hour)), // tie with WID 2
		timestamppb.New(now.Add(3 * time.Hour)),
	}

	var workUnits []*essmsgpb.WorkUnit
	for i := 0; i < 5; i++ {
		wid := int64(i + 1)
		wu := makeSampleWorkUnit(xid, wid, now.Add(time.Duration(i)*time.Minute))
		wu.CurrentWorkUnitExecution.Status.State = states[i]
		wu.CurrentWorkUnitExecution.CompleteTime = completeTimes[i]
		mustInsertWorkUnit(t, pool, repo, wu)
		workUnits = append(workUnits, wu)
	}
	return workUnits
}

func collectAllListedWorkUnits(t *testing.T, repo *Repository, parent string, pageSize int32, filter, orderBy string) []*essmsgpb.WorkUnit {
	t.Helper()
	var got []*essmsgpb.WorkUnit
	var pageToken string
	for page := 0; page < 20; page++ {
		res, nextToken, err := repo.ListWorkUnits(t.Context(), parent, pageSize, pageToken, filter, orderBy, 0)
		if err != nil {
			t.Fatalf("ListWorkUnits(page=%d) failed: %v", page, err)
		}
		got = append(got, res...)
		pageToken = nextToken
		if pageToken == "" {
			return got
		}
	}
	t.Fatalf("ListWorkUnits did not terminate after 20 pages")
	return nil
}

func TestListWorkUnits_Integration(t *testing.T) {
	pool, repo := setupTestRepo(t)
	ctx := t.Context()
	xid := int64(500)
	parent := validator.ConstructExperimentName(xid)
	workUnits := seedListWorkUnitsFixture(t, pool, repo, xid)

	t.Run("ListAll default ordering work_unit_id ASC", func(t *testing.T) {
		got, nextToken, err := repo.ListWorkUnits(ctx, parent, 10, "", "", "", 0)
		if err != nil {
			t.Fatalf("ListWorkUnits() failed: %v", err)
		}
		if nextToken != "" {
			t.Errorf("nextPageToken = %q, want empty", nextToken)
		}
		if diff := cmp.Diff(workUnits, got, protocmp.Transform()); diff != "" {
			t.Errorf("ListWorkUnits() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("Pagination across multiple page sizes", func(t *testing.T) {
		for _, pageSize := range []int32{1, 2, 3, 5} {
			got := collectAllListedWorkUnits(t, repo, parent, pageSize, "", "")
			if diff := cmp.Diff(workUnits, got, protocmp.Transform()); diff != "" {
				t.Errorf("Pagination(pageSize=%d) mismatch (-want +got):\n%s", pageSize, diff)
			}
		}
	})

	t.Run("FilterByState quoted and unquoted", func(t *testing.T) {
		want := []*essmsgpb.WorkUnit{workUnits[0], workUnits[2]}
		for _, filter := range []string{`state = "RUNNING"`, `state = RUNNING`} {
			got, _, err := repo.ListWorkUnits(ctx, parent, 10, "", filter, "", 0)
			if err != nil {
				t.Fatalf("ListWorkUnits(filter=%q) failed: %v", filter, err)
			}
			if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ListWorkUnits(filter=%q) mismatch (-want +got):\n%s", filter, diff)
			}
		}
	})

	t.Run("OrderByField and multi-page PaginationByOrderField", func(t *testing.T) {
		cases := []struct {
			orderBy string
			want    []*essmsgpb.WorkUnit
		}{
			{
				orderBy: "created desc",
				want:    []*essmsgpb.WorkUnit{workUnits[4], workUnits[3], workUnits[2], workUnits[1], workUnits[0]},
			},
			{
				orderBy: "work_unit_id desc",
				want:    []*essmsgpb.WorkUnit{workUnits[4], workUnits[3], workUnits[2], workUnits[1], workUnits[0]},
			},
			{
				orderBy: "state",
				// SCHEDULING (2: WID 5), RUNNING (4: WID 1, WID 3), COMPLETED (10: WID 2), FAILED (11: WID 4)
				want: []*essmsgpb.WorkUnit{workUnits[4], workUnits[0], workUnits[2], workUnits[1], workUnits[3]},
			},
			{
				orderBy: "state desc",
				// FAILED (11: WID 4), COMPLETED (10: WID 2), RUNNING (4: WID 1, WID 3), SCHEDULING (2: WID 5)
				want: []*essmsgpb.WorkUnit{workUnits[3], workUnits[1], workUnits[0], workUnits[2], workUnits[4]},
			},
			{
				orderBy: "execution_completed",
				// NULLS FIRST (WID 1, WID 3), then 2h (WID 2, WID 4), then 3h (WID 5)
				want: []*essmsgpb.WorkUnit{workUnits[0], workUnits[2], workUnits[1], workUnits[3], workUnits[4]},
			},
			{
				orderBy: "execution_completed desc",
				// 3h (WID 5), then 2h (WID 2, WID 4), then NULLS LAST (WID 1, WID 3)
				want: []*essmsgpb.WorkUnit{workUnits[4], workUnits[1], workUnits[3], workUnits[0], workUnits[2]},
			},
		}
		for _, tc := range cases {
			got := collectAllListedWorkUnits(t, repo, parent, 2, "", tc.orderBy)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("PaginationByOrderField(%q) mismatch (-want +got):\n%s", tc.orderBy, diff)
			}
		}
	})

	t.Run("FilterOnSortField", func(t *testing.T) {
		got := collectAllListedWorkUnits(t, repo, parent, 1, `(state = RUNNING OR state = COMPLETED)`, "state")
		want := []*essmsgpb.WorkUnit{workUnits[0], workUnits[2], workUnits[1]}
		if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
			t.Errorf("FilterOnSortField mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("ParentFiltering isolates experiments", func(t *testing.T) {
		otherXID := int64(501)
		seedExperiment(t, pool, otherXID, "bob")
		otherWU := makeSampleWorkUnit(otherXID, 1, time.Now())
		mustInsertWorkUnit(t, pool, repo, otherWU)

		got, _, err := repo.ListWorkUnits(ctx, validator.ConstructExperimentName(otherXID), 10, "", "", "", 0)
		if err != nil {
			t.Fatalf("ListWorkUnits(otherXID) failed: %v", err)
		}
		if diff := cmp.Diff([]*essmsgpb.WorkUnit{otherWU}, got, protocmp.Transform()); diff != "" {
			t.Errorf("ParentFiltering mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("InvalidArguments return InvalidArgument", func(t *testing.T) {
		invalidCases := []struct {
			name      string
			parent    string
			filter    string
			orderBy   string
			pageToken string
		}{
			{name: "invalid parent", parent: "invalid_parent"},
			{name: "invalid filter syntax", parent: parent, filter: "state === RUNNING"},
			{name: "unknown filter field", parent: parent, filter: "unknown_field = 123"},
			{name: "unknown orderBy field", parent: parent, orderBy: "unknown_field asc"},
			{name: "corrupted pageToken", parent: parent, pageToken: "corrupted_token"},
		}
		for _, tc := range invalidCases {
			_, _, err := repo.ListWorkUnits(ctx, tc.parent, 10, tc.pageToken, tc.filter, tc.orderBy, 0)
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("%s: code = %v, want %v (err: %v)", tc.name, status.Code(err), codes.InvalidArgument, err)
			}
		}
	})
}

func TestFetchActiveWorkUnitsByQueues_Integration(t *testing.T) {
	pool, repo := setupTestRepo(t)
	ctx := t.Context()
	seedExperiment(t, pool, 601, "alice")
	seedExperiment(t, pool, 602, "bob")

	baseTime := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	makeQueuedWU := func(xid, wid int64, queue string, state essmsgpb.WorkUnitStatus_WorkUnitState, pos int32, offset time.Duration) *essmsgpb.WorkUnit {
		wu := makeSampleWorkUnit(xid, wid, baseTime.Add(offset))
		wu.Executable.SchedulingConstraints.QueueName = queue
		wu.CurrentWorkUnitExecution.Status.State = state
		if pos <= 0 {
			wu.CurrentWorkUnitExecution.Status.SchedulingInformation = nil
		} else {
			wu.CurrentWorkUnitExecution.Status.SchedulingInformation = &essmsgpb.StructuredMessage{
				AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
					Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{
						QueueInfo: &essmsgpb.QueueInfo{PositionInQueue: pos},
					},
				},
			}
		}
		mustInsertWorkUnit(t, pool, repo, wu)
		return wu
	}

	// Expected active sort order across ["q1", "q2"]:
	// 1. RUNNING (SortPhase=2), pos=1, q1, alice
	wuRun1 := makeQueuedWU(601, 1, "q1", essmsgpb.WorkUnitStatus_RUNNING, 1, 2*time.Minute)
	// 2. RUNNING (SortPhase=2), pos=unknown(MaxInt64), q2, bob
	wuRun2 := makeQueuedWU(602, 1, "q2", essmsgpb.WorkUnitStatus_RUNNING, 0, 1*time.Minute)
	// 3. SCHEDULING (SortPhase=1), pos=1, q1, alice
	wuSched1 := makeQueuedWU(601, 2, "q1", essmsgpb.WorkUnitStatus_SCHEDULING, 1, 3*time.Minute)
	// 4. SCHEDULING (SortPhase=1), pos=2, createTime=+1m, q2, bob
	wuSched2 := makeQueuedWU(602, 2, "q2", essmsgpb.WorkUnitStatus_SCHEDULING, 2, 1*time.Minute)
	// 5. SCHEDULING (SortPhase=1), pos=2, createTime=+4m, q1, alice
	wuSched3 := makeQueuedWU(601, 3, "q1", essmsgpb.WorkUnitStatus_SCHEDULING, 2, 4*time.Minute)

	// Inactive or other-queue work units that must be excluded:
	_ = makeQueuedWU(601, 4, "q1", essmsgpb.WorkUnitStatus_COMPLETED, 1, 0)
	_ = makeQueuedWU(601, 5, "q1", essmsgpb.WorkUnitStatus_FAILED, 1, 0)
	_ = makeQueuedWU(602, 3, "q3", essmsgpb.WorkUnitStatus_RUNNING, 1, 0)

	want := []storage.WorkUnitWithAuthor{
		{WorkUnit: wuRun1, Author: "alice"},
		{WorkUnit: wuRun2, Author: "bob"},
		{WorkUnit: wuSched1, Author: "alice"},
		{WorkUnit: wuSched2, Author: "bob"},
		{WorkUnit: wuSched3, Author: "alice"},
	}

	var collected []storage.WorkUnitWithAuthor
	var token string
	for page := 0; page < 10; page++ {
		res, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"q1", "q2"}, 2, token)
		if err != nil {
			t.Fatalf("FetchActiveWorkUnitsByQueues(page=%d) failed: %v", page, err)
		}
		if res.TotalQueueSize != 5 {
			t.Errorf("page %d TotalQueueSize = %d, want 5", page, res.TotalQueueSize)
		}
		collected = append(collected, res.WorkUnits...)
		token = res.NextPageToken
		if token == "" {
			break
		}
	}

	if diff := cmp.Diff(want, collected, protocmp.Transform()); diff != "" {
		t.Errorf("FetchActiveWorkUnitsByQueues() paginated results mismatch (-want +got):\n%s", diff)
	}
}

func TestConcurrency_FetchNextWorkUnitIDAndInsertWorkUnit(t *testing.T) {
	pool, repo := setupTestRepo(t)
	ctx := t.Context()
	xid := int64(701)
	seedExperiment(t, pool, xid, "alice")

	const workers = 10
	var wg sync.WaitGroup
	allocatedIDs := make([]int64, workers)
	errs := make([]error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var assignedID int64
			errs[idx] = pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
				txRepo := repo.WithTx(tx)
				nextID, err := txRepo.FetchNextWorkUnitID(ctx, xid)
				if err != nil {
					return err
				}
				assignedID = nextID
				wu := makeSampleWorkUnit(xid, nextID, time.Now())
				return txRepo.InsertWorkUnit(ctx, wu)
			})
			allocatedIDs[idx] = assignedID
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d failed: %v", i, err)
		}
	}

	sort.Slice(allocatedIDs, func(i, j int) bool { return allocatedIDs[i] < allocatedIDs[j] })
	wantIDs := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if diff := cmp.Diff(wantIDs, allocatedIDs); diff != "" {
		t.Errorf("allocated work unit IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestConcurrency_UpdateWorkUnit_AndTransactionalReadModifyWrite(t *testing.T) {
	pool, repo := setupTestRepo(t)
	ctx := t.Context()
	xid := int64(702)
	wid := int64(1)
	seedExperiment(t, pool, xid, "alice")

	baseTime := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	initial := makeSampleWorkUnit(xid, wid, baseTime)
	mustInsertWorkUnit(t, pool, repo, initial)

	t.Run("concurrent UpdateWorkUnit calls do not lose updates", func(t *testing.T) {
		updates := []*storage.WorkUnitUpdate{
			{
				XID:        xid,
				WID:        wid,
				WorkUnit:   &essmsgpb.WorkUnit{Priority: essmsgpb.Priority_HIGH},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority"}},
			},
			{
				XID:        xid,
				WID:        wid,
				WorkUnit:   &essmsgpb.WorkUnit{Notes: "concurrent-notes"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			},
			{
				XID:        xid,
				WID:        wid,
				WorkUnit:   &essmsgpb.WorkUnit{Tags: []string{"concurrent-tag-1", "concurrent-tag-2"}},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
			},
			{
				XID: xid,
				WID: wid,
				WorkUnit: &essmsgpb.WorkUnit{
					Urls: []*essmsgpb.Url{{DisplayName: "concurrent-url", Url: "http://example.com/concurrent"}},
				},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"urls"}},
			},
		}

		var wg sync.WaitGroup
		errs := make([]error, len(updates))
		for i, u := range updates {
			wg.Add(1)
			go func(idx int, upd *storage.WorkUnitUpdate) {
				defer wg.Done()
				_, errs[idx] = repo.UpdateWorkUnit(ctx, upd)
			}(i, u)
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("concurrent UpdateWorkUnit[%d] failed: %v", i, err)
			}
		}

		got, err := repo.FetchWorkUnit(ctx, xid, wid)
		if err != nil {
			t.Fatalf("FetchWorkUnit() failed: %v", err)
		}
		if got.GetPriority() != essmsgpb.Priority_HIGH {
			t.Errorf("Priority = %v, want %v", got.GetPriority(), essmsgpb.Priority_HIGH)
		}
		if got.GetNotes() != "concurrent-notes" {
			t.Errorf("Notes = %q, want %q", got.GetNotes(), "concurrent-notes")
		}
		if diff := cmp.Diff([]string{"concurrent-tag-1", "concurrent-tag-2"}, got.GetTags()); diff != "" {
			t.Errorf("Tags mismatch (-want +got):\n%s", diff)
		}
		wantURLs := []*essmsgpb.Url{{DisplayName: "concurrent-url", Url: "http://example.com/concurrent"}}
		if diff := cmp.Diff(wantURLs, got.GetUrls(), protocmp.Transform()); diff != "" {
			t.Errorf("Urls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("concurrent transactional execution updates do not lose updates", func(t *testing.T) {
		const workers = 10
		var wg sync.WaitGroup
		errs := make([]error, workers)

		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				errs[idx] = pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
					txRepo := repo.WithTx(tx)
					currentWU, err := txRepo.FetchWorkUnit(ctx, xid, wid)
					if err != nil {
						return err
					}
					currentExec := currentWU.GetCurrentWorkUnitExecution()
					_, _, currentEID, err := validator.ValidateWorkUnitExecutionName(currentExec.GetName())
					if err != nil {
						return err
					}
					if err := txRepo.SaveWorkUnitExecution(ctx, currentExec); err != nil {
						return err
					}
					nextEID := currentEID + 1
					nextExec := proto.Clone(currentExec).(*essmsgpb.WorkUnitExecution)
					nextExec.Name = validator.ConstructWorkUnitExecutionName(xid, wid, nextEID)
					nextExec.UpdateTime = timestamppb.New(baseTime.Add(time.Duration(nextEID) * time.Minute))
					nextExec.Status.UpdateTime = nextExec.UpdateTime
					return txRepo.UpdateCurrentWorkUnitExecution(ctx, xid, wid, nextEID, nextExec)
				})
			}(i)
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("worker %d failed: %v", i, err)
			}
		}

		execs, err := repo.ListWorkUnitExecutions(ctx, xid, wid)
		if err != nil {
			t.Fatalf("ListWorkUnitExecutions() failed: %v", err)
		}
		if len(execs) != workers+1 {
			t.Fatalf("len(ListWorkUnitExecutions()) = %d, want %d", len(execs), workers+1)
		}
		wantCurrentName := validator.ConstructWorkUnitExecutionName(xid, wid, int64(workers+1))
		if execs[0].GetName() != wantCurrentName {
			t.Errorf("current execution name = %q, want %q", execs[0].GetName(), wantCurrentName)
		}
	})
}
