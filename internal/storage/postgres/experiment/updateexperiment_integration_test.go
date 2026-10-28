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
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

func TestUpdateExperiment(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	// 1. Create an Experiment.
	createdExp := createBlankExperiment(ctx, t, repo, 123)
	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", createdExp.GetName(), err)
	}

	// 2. Update the Experiment.
	update := &storage.ExperimentUpdate{
		XID: xid,
		Experiment: &pb.Experiment{
			Title: "new-title",
			Notes: "new-notes",
			Tags:  []string{"tag3"},
			Urls:  []*pb.Url{{Url: "http://new.com"}},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"title", "notes", "tags", "urls"}},
	}
	res, err := repo.UpdateExperiment(ctx, update)
	if err != nil {
		t.Fatalf("UpdateExperiment() unexpected error: %v", err)
	}
	originalExp, updatedExp := res.Original, res.Updated

	// 3. Verify the update.
	if diff := cmp.Diff(createdExp, originalExp, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateExperiment() original mismatch (-want +got):\n%s", diff)
	}

	wantExp := proto.Clone(createdExp).(*pb.Experiment)
	wantExp.Title = "new-title"
	wantExp.Notes = "new-notes"
	wantExp.Tags = []string{"tag3"}
	wantExp.Urls = []*pb.Url{{Url: "http://new.com"}}
	if diff := cmp.Diff(wantExp, updatedExp, protocmp.Transform(), protocmp.IgnoreFields(&pb.Experiment{}, "update_time")); diff != "" {
		t.Errorf("UpdateExperiment() updated mismatch (-want +got):\n%s", diff)
	}

	if !updatedExp.UpdateTime.AsTime().After(createdExp.UpdateTime.AsTime()) {
		t.Errorf("UpdateExperiment() UpdateTime = %v, want > %v", updatedExp.UpdateTime, createdExp.UpdateTime)
	}
	if updatedExp.GetCreator() != createdExp.GetCreator() {
		t.Errorf("UpdateExperiment() Creator = %q, want %q", updatedExp.GetCreator(), createdExp.GetCreator())
	}

	// 4. Verify with FetchExperiment.
	gotExp, err := repo.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperiment(%d) unexpected error: %v", xid, err)
	}
	if diff := cmp.Diff(updatedExp, gotExp, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperiment(%d) after update mismatch (-want +got):\n%s", xid, diff)
	}
}

func TestUpdateExperiment_NoChangesSkipsWrite(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	createdExp := createBlankExperiment(ctx, t, repo, 123)

	res, err := repo.UpdateExperiment(ctx, &storage.ExperimentUpdate{
		XID:        123,
		Experiment: &pb.Experiment{Title: createdExp.GetTitle()},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
	})
	if err != nil {
		t.Fatalf("UpdateExperiment() unexpected error: %v", err)
	}
	if diff := cmp.Diff(createdExp, res.Updated, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateExperiment(no-op) mismatch (-want +got):\n%s", diff)
	}
}

func TestUpdateExperiment_ImmutableField(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	createdExp := createBlankExperiment(ctx, t, repo, 123)
	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", createdExp.GetName(), err)
	}

	update := &storage.ExperimentUpdate{
		XID: xid,
		Experiment: &pb.Experiment{
			Creator: "new-creator",
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"creator"}},
	}
	_, err = repo.UpdateExperiment(ctx, update)
	if !errors.Is(err, grpcerr.ErrInvalidArgument) || status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateExperiment(immutable field) error = %v (code %v), want %v (code %v)", err, status.Code(err), grpcerr.ErrInvalidArgument, codes.InvalidArgument)
	}
}

func TestUpdateExperiment_NotFound(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	update := &storage.ExperimentUpdate{
		XID: 123456789,
		Experiment: &pb.Experiment{
			Title: "new-title",
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
	}
	_, err := repo.UpdateExperiment(ctx, update)
	if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
		t.Errorf("UpdateExperiment(not found) error = %v (code %v), want %v (code %v)", err, status.Code(err), grpcerr.ErrNotFound, codes.NotFound)
	}
}

func TestUpdateExperiment_EmptyMask(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	createdExp := createBlankExperiment(ctx, t, repo, 123)
	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", createdExp.GetName(), err)
	}

	update := &storage.ExperimentUpdate{
		XID:        xid,
		Experiment: &pb.Experiment{},
		UpdateMask: &fmpb.FieldMask{Paths: []string{}},
	}
	_, err = repo.UpdateExperiment(ctx, update)
	if !errors.Is(err, grpcerr.ErrInvalidArgument) || status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateExperiment(empty mask) error = %v (code %v), want %v (code %v)", err, status.Code(err), grpcerr.ErrInvalidArgument, codes.InvalidArgument)
	}
}

func TestTxRepository_UpdateExperimentStatus(t *testing.T) {
	ctx := t.Context()
	repo, essPool := newTestRepository(t)

	createdExp := createBlankExperiment(ctx, t, repo, 321)
	completedAt := timestamppb.New(createdExp.CreateTime.AsTime().Add(2 * time.Minute))
	statusUpdatedAt := timestamppb.New(createdExp.CreateTime.AsTime().Add(time.Minute))

	wantExp := proto.Clone(createdExp).(*pb.Experiment)
	wantExp.UpdateTime = statusUpdatedAt
	wantExp.CompleteTime = completedAt
	wantExp.Status = &pb.ExperimentStatus{
		ActiveState:               pb.ExperimentStatus_INACTIVE,
		LaunchState:               pb.ExperimentStatus_COMPLETED,
		UpdateTime:                statusUpdatedAt,
		LastChildStatusUpdateTime: statusUpdatedAt,
		ChildStateCounts: []*pb.ExperimentStatus_StateCount{
			{State: pb.WorkUnitStatus_COMPLETED, Count: 2},
		},
	}

	err := runInTx(ctx, repo, func(ctx context.Context, tx *TxRepository) error {
		return tx.UpdateExperimentStatus(ctx, wantExp)
	})
	if err != nil {
		t.Fatalf("UpdateExperimentStatus() unexpected error: %v", err)
	}

	gotExp, err := repo.FetchExperiment(ctx, 321)
	if err != nil {
		t.Fatalf("FetchExperiment(321) unexpected error: %v", err)
	}
	if diff := cmp.Diff(wantExp, gotExp, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperiment(321) after status update mismatch (-want +got):\n%s", diff)
	}

	wantDerived := derivedStateColumns{
		ActiveState: pb.ExperimentStatus_INACTIVE,
		LaunchState: pb.ExperimentStatus_COMPLETED,
	}
	if diff := cmp.Diff(wantDerived, queryDerivedStateColumns(ctx, t, essPool, 321)); diff != "" {
		t.Errorf("stored derived state columns mismatch (-want +got):\n%s", diff)
	}
}

func TestTxRepository_UpdateExperimentStatus_NotFound(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	exp := blankExperimentProto(999999)
	exp.Status.UpdateTime = timestamppb.New(time.Now().Truncate(time.Microsecond))

	err := runInTx(ctx, repo, func(ctx context.Context, tx *TxRepository) error {
		return tx.UpdateExperimentStatus(ctx, exp)
	})
	if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
		t.Errorf("UpdateExperimentStatus(not found) error = %v (code %v), want %v (code %v)", err, status.Code(err), grpcerr.ErrNotFound, codes.NotFound)
	}
}

func TestTxRepository_UpdateExperimentResources(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	createdExp := createBlankExperiment(ctx, t, repo, 654)
	updatedAt := timestamppb.New(createdExp.CreateTime.AsTime().Add(time.Minute))

	wantExp := proto.Clone(createdExp).(*pb.Experiment)
	wantExp.UpdateTime = updatedAt
	wantExp.Resources = []*pb.ResourceSet{
		{
			Resources: &pb.ResourceSet_CpuResources{
				CpuResources: &pb.CpuResources{
					CpuMillicores: 1000,
					MemoryBytes:   2048,
				},
			},
		},
	}

	err := runInTx(ctx, repo, func(ctx context.Context, tx *TxRepository) error {
		return tx.UpdateExperimentResources(ctx, wantExp)
	})
	if err != nil {
		t.Fatalf("UpdateExperimentResources() unexpected error: %v", err)
	}

	gotExp, err := repo.FetchExperiment(ctx, 654)
	if err != nil {
		t.Fatalf("FetchExperiment(654) unexpected error: %v", err)
	}
	if diff := cmp.Diff(wantExp, gotExp, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperiment(654) after resources update mismatch (-want +got):\n%s", diff)
	}
}

func TestTxRepository_UpdateExperimentResources_NotFound(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	exp := blankExperimentProto(999999)
	exp.UpdateTime = timestamppb.New(time.Now().Truncate(time.Microsecond))

	err := runInTx(ctx, repo, func(ctx context.Context, tx *TxRepository) error {
		return tx.UpdateExperimentResources(ctx, exp)
	})
	if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
		t.Errorf("UpdateExperimentResources(not found) error = %v (code %v), want %v (code %v)", err, status.Code(err), grpcerr.ErrNotFound, codes.NotFound)
	}
}

func TestTxRepository_RollbackRestoresExperiment(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	createdExp := createBlankExperiment(ctx, t, repo, 777)
	errAbort := errors.New("abort transaction")

	err := runInTx(ctx, repo, func(ctx context.Context, tx *TxRepository) error {
		exp, err := tx.FetchExperiment(ctx, 777)
		if err != nil {
			return err
		}
		exp.Status.ActiveState = pb.ExperimentStatus_INACTIVE
		exp.Status.UpdateTime = timestamppb.New(time.Now().Truncate(time.Microsecond))
		if err := tx.UpdateExperimentStatus(ctx, exp); err != nil {
			return err
		}
		return errAbort
	})
	if !errors.Is(err, errAbort) {
		t.Fatalf("runInTx() error = %v, want %v", err, errAbort)
	}

	gotExp, err := repo.FetchExperiment(ctx, 777)
	if err != nil {
		t.Fatalf("FetchExperiment(777) unexpected error: %v", err)
	}
	if diff := cmp.Diff(createdExp, gotExp, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperiment(777) after rollback mismatch (-want +got):\n%s", diff)
	}
}

// UpdateExperiment writes back every user-mutable column after merging the masked fields into the
// row it read. Without FOR NO KEY UPDATE on that read, concurrent updates of disjoint fields of
// the same experiment read the same initial row and overwrite each other's columns.
func TestUpdateExperiment_ConcurrentUpdatesDoNotLoseUpdates(t *testing.T) {
	const rounds = 5
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	const xid = int64(888)
	createBlankExperiment(ctx, t, repo, xid)

	for round := 1; round <= rounds; round++ {
		wantTitle := fmt.Sprintf("title-round-%d", round)
		wantNotes := fmt.Sprintf("notes-round-%d", round)
		wantPriority := pb.Priority_HIGH
		wantTags := []string{fmt.Sprintf("tag-a-%d", round), fmt.Sprintf("tag-b-%d", round)}
		wantUrls := []*pb.Url{{Url: fmt.Sprintf("https://example.com/%d", round)}}

		updates := []*storage.ExperimentUpdate{
			{
				XID:        xid,
				Experiment: &pb.Experiment{Title: wantTitle},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
			},
			{
				XID:        xid,
				Experiment: &pb.Experiment{Notes: wantNotes},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"notes"}},
			},
			{
				XID:        xid,
				Experiment: &pb.Experiment{Priority: wantPriority},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"priority"}},
			},
			{
				XID:        xid,
				Experiment: &pb.Experiment{Tags: wantTags},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"tags"}},
			},
			{
				XID:        xid,
				Experiment: &pb.Experiment{Urls: wantUrls},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"urls"}},
			},
		}

		errs := make([]error, len(updates))
		var wg sync.WaitGroup
		for i, u := range updates {
			wg.Go(func() {
				_, errs[i] = repo.UpdateExperiment(ctx, u)
			})
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatalf("round %d UpdateExperiment() unexpected error: %v", round, err)
		}

		got, err := repo.FetchExperiment(ctx, xid)
		if err != nil {
			t.Fatalf("round %d FetchExperiment(%d) unexpected error: %v", round, xid, err)
		}
		if got.GetTitle() != wantTitle {
			t.Errorf("round %d Title = %q, want %q (lost update)", round, got.GetTitle(), wantTitle)
		}
		if got.GetNotes() != wantNotes {
			t.Errorf("round %d Notes = %q, want %q (lost update)", round, got.GetNotes(), wantNotes)
		}
		if got.GetPriority() != wantPriority {
			t.Errorf("round %d Priority = %v, want %v (lost update)", round, got.GetPriority(), wantPriority)
		}
		if diff := cmp.Diff(wantTags, got.GetTags()); diff != "" {
			t.Errorf("round %d Tags mismatch (-want +got; lost update):\n%s", round, diff)
		}
		if diff := cmp.Diff(wantUrls, got.GetUrls(), protocmp.Transform()); diff != "" {
			t.Errorf("round %d Urls mismatch (-want +got; lost update):\n%s", round, diff)
		}
	}
}

// Concurrent transactional read-modify-writes through TxRepository.FetchExperiment,
// UpdateExperimentStatus and UpdateExperimentResources must not lose updates: FetchExperiment
// locks the row with FOR NO KEY UPDATE, so each transaction reads the state that the previous one
// committed.
func TestTxRepository_FetchExperimentLocksRowAgainstLostUpdates(t *testing.T) {
	const workers = 10
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	const xid = int64(889)
	createBlankExperiment(ctx, t, repo, xid)

	wantStorageBytes := make([]int64, workers)
	for i := range wantStorageBytes {
		wantStorageBytes[i] = int64(i + 1)
	}

	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i, storageBytes := range wantStorageBytes {
		wg.Go(func() {
			errs[i] = runInTx(ctx, repo, func(ctx context.Context, tx *TxRepository) error {
				exp, err := tx.FetchExperiment(ctx, xid)
				if err != nil {
					return err
				}
				time.Sleep(lostUpdateWindow)

				now := timestamppb.New(time.Now().Truncate(time.Microsecond))
				exp.UpdateTime = now
				exp.Status.UpdateTime = now

				var totalRunning int64
				for _, c := range exp.Status.ChildStateCounts {
					if c.State == pb.WorkUnitStatus_RUNNING {
						totalRunning = c.Count
					}
				}
				exp.Status.ChildStateCounts = []*pb.ExperimentStatus_StateCount{
					{State: pb.WorkUnitStatus_RUNNING, Count: totalRunning + 1},
				}
				exp.Resources = append(exp.Resources, &pb.ResourceSet{
					EphemeralStorageBytes: storageBytes,
				})

				if err := tx.UpdateExperimentStatus(ctx, exp); err != nil {
					return err
				}
				return tx.UpdateExperimentResources(ctx, exp)
			})
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("runInTx() unexpected error: %v", err)
	}

	got, err := repo.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperiment(%d) unexpected error: %v", xid, err)
	}
	var gotRunning int64
	for _, c := range got.GetStatus().GetChildStateCounts() {
		if c.GetState() == pb.WorkUnitStatus_RUNNING {
			gotRunning = c.GetCount()
		}
	}
	if gotRunning != workers {
		t.Errorf("RUNNING child state count = %d, want %d (lost status updates)", gotRunning, workers)
	}

	var gotStorageBytes []int64
	for _, res := range got.GetResources() {
		gotStorageBytes = append(gotStorageBytes, res.GetEphemeralStorageBytes())
	}
	slices.Sort(gotStorageBytes)
	if diff := cmp.Diff(wantStorageBytes, gotStorageBytes); diff != "" {
		t.Errorf("appended resource storage bytes mismatch (-want +got; missing entries are lost updates):\n%s", diff)
	}
}
