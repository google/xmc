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

package artifact

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
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

func blankArtifactProto(xid, aid int64) *essmsgpb.Artifact {
	return &essmsgpb.Artifact{
		Name: validator.ConstructArtifactName(aid), Parent: validator.ConstructExperimentName(xid),
		Url: &essmsgpb.Url{Url: fmt.Sprintf("http://test-%d.com", aid)}, Tags: []string{"tag1", "tag2"}, Notes: "test-notes",
	}
}

func mustCreateArtifact(ctx context.Context, t *testing.T, repo *Repository, art *essmsgpb.Artifact) *essmsgpb.Artifact {
	t.Helper()
	created, err := repo.CreateArtifact(ctx, art)
	if err != nil {
		t.Fatalf("CreateArtifact(%v) failed: %v", art, err)
	}
	return created
}

func TestNewRepository_BindsPool(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.pool != pool {
		t.Errorf("NewRepository(pool).pool = %v, want %p", repo.pool, pool)
	}
}

func TestRepository_ValidationAndNilPoolErrors(t *testing.T) {
	ctx := t.Context()
	nilRepo := NewRepository(nil)
	poolRepo := NewRepository(&pgxpool.Pool{})
	mask := &fmpb.FieldMask{Paths: []string{"notes"}}

	onlyErr := func(_ any, err error) error { return err }
	tests := []struct {
		name     string
		call     func() error
		wantErr  error
		wantCode codes.Code
	}{
		{"CreateArtifact nil pool", func() error { return onlyErr(nilRepo.CreateArtifact(ctx, blankArtifactProto(1, 1))) }, grpcerr.ErrFailedPrecondition, codes.FailedPrecondition},
		{"CreateArtifact nil artifact", func() error { return onlyErr(poolRepo.CreateArtifact(ctx, nil)) }, grpcerr.ErrInvalidArgument, codes.InvalidArgument},
		{"CreateArtifact invalid name", func() error {
			return onlyErr(poolRepo.CreateArtifact(ctx, &essmsgpb.Artifact{Name: "invalid", Parent: "experiments/1"}))
		}, grpcerr.ErrInvalidArgument, codes.InvalidArgument},
		{"CreateArtifact invalid parent", func() error {
			return onlyErr(poolRepo.CreateArtifact(ctx, &essmsgpb.Artifact{Name: "artifacts/1", Parent: "invalid/1"}))
		}, grpcerr.ErrInvalidArgument, codes.InvalidArgument},
		{"FetchArtifact nil pool", func() error { return onlyErr(nilRepo.FetchArtifact(ctx, 1)) }, grpcerr.ErrFailedPrecondition, codes.FailedPrecondition},
		{"ListArtifacts nil pool", func() error { _, _, err := nilRepo.ListArtifacts(ctx, "", 10, "", "", "", 0); return err }, grpcerr.ErrFailedPrecondition, codes.FailedPrecondition},
		{"ListArtifacts invalid parent", func() error { _, _, err := nilRepo.ListArtifacts(ctx, "invalid/1", 10, "", "", "", 0); return err }, grpcerr.ErrInvalidArgument, codes.InvalidArgument},
		{"UpdateArtifact nil update", func() error { return onlyErr(nilRepo.UpdateArtifact(ctx, nil)) }, grpcerr.ErrInvalidArgument, codes.InvalidArgument},
		{"UpdateArtifact nil artifact", func() error {
			return onlyErr(nilRepo.UpdateArtifact(ctx, &storage.ArtifactUpdate{AID: 1, UpdateMask: mask}))
		}, grpcerr.ErrInvalidArgument, codes.InvalidArgument},
		{"UpdateArtifact empty mask", func() error {
			return onlyErr(nilRepo.UpdateArtifact(ctx, &storage.ArtifactUpdate{AID: 1, Artifact: &essmsgpb.Artifact{}, UpdateMask: &fmpb.FieldMask{}}))
		}, grpcerr.ErrInvalidArgument, codes.InvalidArgument},
		{"UpdateArtifact nil pool", func() error {
			return onlyErr(nilRepo.UpdateArtifact(ctx, &storage.ArtifactUpdate{AID: 1, Artifact: &essmsgpb.Artifact{}, UpdateMask: mask}))
		}, grpcerr.ErrFailedPrecondition, codes.FailedPrecondition},
		{"DeleteArtifact nil pool", func() error { return nilRepo.DeleteArtifact(ctx, 1) }, grpcerr.ErrFailedPrecondition, codes.FailedPrecondition},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.wantErr) || status.Code(err) != tc.wantCode {
				t.Errorf("%s = %v (code %v), want %v (code %v)", tc.name, err, status.Code(err), tc.wantErr, tc.wantCode)
			}
		})
	}
}

func TestCreateAndFetchArtifact(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedWorkUnit(ctx, t, pool, testXID, testWID)

	expArt := &essmsgpb.Artifact{
		Name: "artifacts/456", Parent: "experiments/123",
		Url:      &essmsgpb.Url{Url: "http://artifact.com", Icon: "icon", DisplayName: "disp", Priority: 7},
		Priority: 7, Tags: []string{"tag3", "tag4"}, Notes: "artifact-notes",
		LifecyclePhase: essmsgpb.Artifact_OUTPUT, DataType: essmsgpb.Artifact_URL, MimeType: "text/html",
		AdditionalInfo: &essmsgpb.Artifact_AdditionalInfo{Title: "Info", Content: &essmsgpb.Artifact_AdditionalInfo_CodeBlock{CodeBlock: "echo hi"}},
		Payload:        &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_Text{Text: &essmsgpb.TextArtifact{Title: "T", Text: "B"}}},
	}
	createdExpArt := mustCreateArtifact(ctx, t, repo, expArt)
	if expArt.GetCreateTime() != nil || expArt.GetUpdateTime() != nil {
		t.Errorf("input expArt was mutated in place: %+v", expArt)
	}
	if createdExpArt.GetCreateTime() == nil || createdExpArt.GetUpdateTime() == nil {
		t.Fatalf("createdExpArt timestamps = (%v, %v), want non-nil", createdExpArt.GetCreateTime(), createdExpArt.GetUpdateTime())
	}
	gotExpArt, err := repo.FetchArtifact(ctx, 456)
	if err != nil {
		t.Fatalf("FetchArtifact(456) failed: %v", err)
	}
	if diff := cmp.Diff(createdExpArt, gotExpArt, protocmp.Transform()); diff != "" {
		t.Errorf("FetchArtifact(456) mismatch (-want +got):\n%s", diff)
	}

	t.Run("work unit parent and NULL optional columns round-trip", func(t *testing.T) {
		wuArt := &essmsgpb.Artifact{Name: "artifacts/457", Parent: "experiments/123/workUnits/1", Url: &essmsgpb.Url{Url: "http://wu-artifact.com"}}
		createdWUArt := mustCreateArtifact(ctx, t, repo, wuArt)
		gotWUArt, err := repo.FetchArtifact(ctx, 457)
		if err != nil {
			t.Fatalf("FetchArtifact(457) failed: %v", err)
		}
		if diff := cmp.Diff(createdWUArt, gotWUArt, protocmp.Transform()); diff != "" {
			t.Errorf("FetchArtifact(457) mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("NotFound and missing parent errors", func(t *testing.T) {
		if _, err := repo.FetchArtifact(ctx, 999999); !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
			t.Errorf("FetchArtifact(missing) = %v (code %v), want %v", err, status.Code(err), grpcerr.ErrNotFound)
		}
		if _, err := repo.CreateArtifact(ctx, blankArtifactProto(999, 888)); !errors.Is(err, grpcerr.ErrFailedPrecondition) || status.Code(err) != codes.FailedPrecondition {
			t.Errorf("CreateArtifact(missing parent) = %v (code %v), want %v", err, status.Code(err), grpcerr.ErrFailedPrecondition)
		}
	})
}

func TestDeleteArtifact(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedExperiment(ctx, t, pool, testXID)

	mustCreateArtifact(ctx, t, repo, blankArtifactProto(testXID, 456))
	if err := repo.DeleteArtifact(ctx, 456); err != nil {
		t.Fatalf("DeleteArtifact(456) failed: %v", err)
	}
	if _, err := repo.FetchArtifact(ctx, 456); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("FetchArtifact(456) after delete = %v, want %v", err, grpcerr.ErrNotFound)
	}
	if err := repo.DeleteArtifact(ctx, 456); !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
		t.Errorf("DeleteArtifact(missing) = %v (code %v), want %v", err, status.Code(err), grpcerr.ErrNotFound)
	}
}

func TestUpdateArtifact(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedExperiment(ctx, t, pool, testXID)

	createdArt := mustCreateArtifact(ctx, t, repo, blankArtifactProto(testXID, 456))
	newPayload := &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_Text{Text: &essmsgpb.TextArtifact{Title: "updated-title", Text: "updated-searchable-body"}}}
	res, err := repo.UpdateArtifact(ctx, &storage.ArtifactUpdate{
		AID:        456,
		Artifact:   &essmsgpb.Artifact{Notes: "new-notes", Tags: []string{"tag3"}, Url: &essmsgpb.Url{Url: "http://new.com", Priority: 5}, Payload: newPayload},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"notes", "tags", "url", "payload"}},
	})
	if err != nil {
		t.Fatalf("UpdateArtifact() failed: %v", err)
	}
	if diff := cmp.Diff(createdArt, res.Original, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateArtifact Original mismatch (-want +got):\n%s", diff)
	}
	if !res.Updated.GetUpdateTime().AsTime().After(createdArt.GetUpdateTime().AsTime()) {
		t.Errorf("UpdateTime = %v, want > %v", res.Updated.GetUpdateTime(), createdArt.GetUpdateTime())
	}
	wantUpdated := &essmsgpb.Artifact{
		Name: "artifacts/456", Parent: "experiments/123",
		Url: &essmsgpb.Url{Url: "http://new.com", Priority: 5}, Priority: 5,
		Tags: []string{"tag3"}, Notes: "new-notes", Payload: newPayload,
		CreateTime: createdArt.GetCreateTime(), UpdateTime: res.Updated.GetUpdateTime(),
	}
	if diff := cmp.Diff(wantUpdated, res.Updated, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateArtifact Updated mismatch (-want +got):\n%s", diff)
	}
	gotArt, err := repo.FetchArtifact(ctx, 456)
	if err != nil {
		t.Fatalf("FetchArtifact(456) failed: %v", err)
	}
	if diff := cmp.Diff(wantUpdated, gotArt, protocmp.Transform()); diff != "" {
		t.Errorf("FetchArtifact(456) after update mismatch (-want +got):\n%s", diff)
	}
	if listed, _, err := repo.ListArtifacts(ctx, "experiments/123", 10, "", `payload_type = "text" AND "updated-searchable-body"`, "", 0); err != nil || len(listed) != 1 {
		t.Errorf("ListArtifacts(updated payload_type & searchable_content) = (%d items, err %v), want (1, nil)", len(listed), err)
	}

	t.Run("no-op update skips write and preserves UpdateTime", func(t *testing.T) {
		noopRes, err := repo.UpdateArtifact(ctx, &storage.ArtifactUpdate{
			AID: 456, Artifact: &essmsgpb.Artifact{Notes: "new-notes"}, UpdateMask: &fmpb.FieldMask{Paths: []string{"notes"}},
		})
		if err != nil {
			t.Fatalf("UpdateArtifact(no-op) failed: %v", err)
		}
		if diff := cmp.Diff(res.Updated, noopRes.Updated, protocmp.Transform()); diff != "" {
			t.Errorf("UpdateArtifact(no-op) mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("immutable field and NotFound errors", func(t *testing.T) {
		_, err := repo.UpdateArtifact(ctx, &storage.ArtifactUpdate{
			AID: 456, Artifact: &essmsgpb.Artifact{Name: "artifacts/789"}, UpdateMask: &fmpb.FieldMask{Paths: []string{"name"}},
		})
		if !errors.Is(err, grpcerr.ErrInvalidArgument) {
			t.Errorf("UpdateArtifact(immutable field) = %v, want %v", err, grpcerr.ErrInvalidArgument)
		}
		_, err = repo.UpdateArtifact(ctx, &storage.ArtifactUpdate{
			AID: 999999, Artifact: &essmsgpb.Artifact{Notes: "x"}, UpdateMask: &fmpb.FieldMask{Paths: []string{"notes"}},
		})
		if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
			t.Errorf("UpdateArtifact(missing) = %v (code %v), want %v", err, status.Code(err), grpcerr.ErrNotFound)
		}
	})
}

func TestUpdateArtifact_ConcurrentUpdatesNoLostUpdate(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedExperiment(ctx, t, pool, testXID)

	const aid int64 = 456
	mustCreateArtifact(ctx, t, repo, blankArtifactProto(testXID, aid))

	holdTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin() failed: %v", err)
	}
	defer func() { _ = holdTx.Rollback(context.Background()) }()
	if _, err := holdTx.Exec(ctx, lockArtifactSQL.String(), aid); err != nil {
		t.Fatalf("holdTx lock failed: %v", err)
	}

	updates := []*storage.ArtifactUpdate{
		{AID: aid, Artifact: &essmsgpb.Artifact{Notes: "concurrent-notes"}, UpdateMask: &fmpb.FieldMask{Paths: []string{"notes"}}},
		{AID: aid, Artifact: &essmsgpb.Artifact{Tags: []string{"concurrent-tag"}}, UpdateMask: &fmpb.FieldMask{Paths: []string{"tags"}}},
		{AID: aid, Artifact: &essmsgpb.Artifact{MimeType: "application/json"}, UpdateMask: &fmpb.FieldMask{Paths: []string{"mime_type"}}},
	}

	errs := make([]error, len(updates))
	var started, done sync.WaitGroup
	for i, u := range updates {
		started.Add(1)
		done.Go(func() {
			started.Done()
			_, errs[i] = repo.UpdateArtifact(ctx, u)
		})
	}
	started.Wait()
	time.Sleep(20 * time.Millisecond)
	if err := holdTx.Commit(ctx); err != nil {
		t.Fatalf("holdTx.Commit() failed: %v", err)
	}
	done.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("concurrent UpdateArtifact() failed: %v", err)
	}

	got, err := repo.FetchArtifact(ctx, aid)
	if err != nil {
		t.Fatalf("FetchArtifact() failed: %v", err)
	}
	if got.GetNotes() != "concurrent-notes" || got.GetMimeType() != "application/json" || !cmp.Equal(got.GetTags(), []string{"concurrent-tag"}) {
		t.Errorf("got after concurrent updates = (notes=%q, mime=%q, tags=%v), lost update detected", got.GetNotes(), got.GetMimeType(), got.GetTags())
	}
}

func TestListArtifacts_FiltersAndOrdering(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedWorkUnit(ctx, t, pool, testXID, testWID)

	art1 := blankArtifactProto(testXID, 3)
	art1.Url = &essmsgpb.Url{Url: "http://1", DisplayName: "name-1", Priority: 9}
	art1.Tags, art1.Notes, art1.MimeType = []string{"tag1"}, "notes-1", "mime-1"
	art1.Payload = &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_ManagedXprof{ManagedXprof: &essmsgpb.ManagedXprofArtifact{DisplayName: "xprof-display", RunGroup: "xprof-rg"}}}
	mustCreateArtifact(ctx, t, repo, art1)
	time.Sleep(2 * time.Millisecond)

	art2 := blankArtifactProto(testXID, 1)
	art2.Parent = validator.ConstructWorkUnitName(testXID, testWID)
	art2.Url = &essmsgpb.Url{Url: "http://2", DisplayName: "name-2", Priority: 8}
	art2.Tags, art2.Notes, art2.MimeType = []string{"tag2"}, "notes-2", "mime-2"
	art2.Payload = &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_CodeSource{CodeSource: &essmsgpb.CodeSourceArtifact{Title: "code-title", CodeBlock: "print('hi')"}}}
	mustCreateArtifact(ctx, t, repo, art2)
	time.Sleep(2 * time.Millisecond)

	art3 := blankArtifactProto(testXID, 2)
	art3.Url = &essmsgpb.Url{Url: "http://3", DisplayName: "name-1", Priority: 7}
	art3.Tags, art3.Notes, art3.MimeType, art3.LifecyclePhase = []string{"tag1", "tag3"}, "notes-3", "mime-3", essmsgpb.Artifact_OUTPUT
	mustCreateArtifact(ctx, t, repo, art3)

	filterTests := []struct {
		name, parent, filter string
		want                 []int64
	}{
		{"all", "", "", []int64{1, 2, 3}},
		{"experiment parent", "experiments/123", "", []int64{1, 2, 3}},
		{"work unit parent", "experiments/123/workUnits/1", "", []int64{1}},
		{"work unit parent with filter", "experiments/123/workUnits/1", `mime_type="mime-2"`, []int64{1}},
		{"artifact_id", "", "artifact_id=1", []int64{1}},
		{"display_name", "", `display_name="name-1"`, []int64{2, 3}},
		{"tags", "", `tags:"tag1"`, []int64{2, 3}},
		{"notes", "", `notes="notes-3"`, []int64{2}},
		{"mime_type", "", `mime_type="mime-2"`, []int64{1}},
		{"payload_type managed_xprof", "", `payload_type = "managed_xprof"`, []int64{3}},
		{"payload_type code_source", "", `payload_type = "code_source"`, []int64{1}},
		{"full text searchable_content", "", "xprof", []int64{3}},
		{"full text tags", "", "tag3", []int64{2}},
		{"lifecycle_phase enum name", "", `lifecycle_phase = "OUTPUT"`, []int64{2}},
	}
	for _, tc := range filterTests {
		t.Run("filter_"+tc.name, func(t *testing.T) {
			got, _, err := repo.ListArtifacts(ctx, tc.parent, 10, "", tc.filter, "artifact_id", 0)
			if err != nil {
				t.Fatalf("ListArtifacts() failed: %v", err)
			}
			if diff := cmp.Diff(tc.want, extractAIDs(got)); diff != "" {
				t.Errorf("ListArtifacts() AIDs mismatch (-want +got):\n%s", diff)
			}
		})
	}

	for _, tc := range []struct {
		orderBy string
		want    []int64
	}{
		{"artifact_id", []int64{1, 2, 3}},
		{"artifact_id desc", []int64{3, 2, 1}},
		{"priority desc", []int64{3, 1, 2}},
		{"created", []int64{3, 1, 2}},
		{"created desc", []int64{2, 1, 3}},
		{"updated", []int64{3, 1, 2}},
		{"updated desc", []int64{2, 1, 3}},
	} {
		t.Run("order_"+tc.orderBy, func(t *testing.T) {
			got, _, err := repo.ListArtifacts(ctx, "", 10, "", "", tc.orderBy, 0)
			if err != nil {
				t.Fatalf("ListArtifacts(orderBy=%q) failed: %v", tc.orderBy, err)
			}
			if diff := cmp.Diff(tc.want, extractAIDs(got)); diff != "" {
				t.Errorf("ListArtifacts(orderBy=%q) AIDs mismatch (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

func TestListArtifacts_MultiPagePagination(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedWorkUnit(ctx, t, pool, 1, 1)
	postgrestest.SeedWorkUnit(ctx, t, pool, 1, 2)

	specs := []struct {
		parent, displayName, mimeType string
	}{
		{"experiments/1", "D", "M1"},
		{"experiments/1", "C", "M2"},
		{"experiments/1/workUnits/1", "B", "M1"},
		{"experiments/1/workUnits/1", "A", "M2"},
		{"experiments/1/workUnits/2", "E", "M1"},
	}
	created := make([]*essmsgpb.Artifact, len(specs))
	for i, s := range specs {
		art := blankArtifactProto(1, int64(i+1))
		art.Parent, art.Url.DisplayName, art.MimeType = s.parent, s.displayName, s.mimeType
		created[i] = mustCreateArtifact(ctx, t, repo, art)
	}

	t.Run("default order multi-page traversal", func(t *testing.T) {
		var collected []*essmsgpb.Artifact
		var pageToken string
		wantSizes := []int{2, 2, 1}
		for i, wantSize := range wantSizes {
			page, nextToken, err := repo.ListArtifacts(ctx, "", 2, pageToken, "", "", 0)
			if err != nil {
				t.Fatalf("ListArtifacts page %d failed: %v", i+1, err)
			}
			if len(page) != wantSize || (i < len(wantSizes)-1) != (nextToken != "") {
				t.Errorf("page %d len=%d nextToken=%q, want len=%d", i+1, len(page), nextToken, wantSize)
			}
			collected = append(collected, page...)
			pageToken = nextToken
		}
		if diff := cmp.Diff(created, collected, protocmp.Transform()); diff != "" {
			t.Errorf("paginated artifacts mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("filtered and ordered multi-page traversal", func(t *testing.T) {
		page1, token1, err := repo.ListArtifacts(ctx, "", 2, "", `mime_type="M1"`, "display_name desc", 0)
		if err != nil || token1 == "" {
			t.Fatalf("page 1 err=%v token=%q, want nil error and non-empty token", err, token1)
		}
		if diff := cmp.Diff([]*essmsgpb.Artifact{created[4], created[0]}, page1, protocmp.Transform()); diff != "" {
			t.Errorf("page 1 mismatch (-want +got):\n%s", diff)
		}
		page2, token2, err := repo.ListArtifacts(ctx, "", 2, token1, `mime_type="M1"`, "display_name desc", 0)
		if err != nil || token2 != "" {
			t.Fatalf("page 2 err=%v token=%q, want nil error and empty token", err, token2)
		}
		if diff := cmp.Diff([]*essmsgpb.Artifact{created[2]}, page2, protocmp.Transform()); diff != "" {
			t.Errorf("page 2 mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestExtractArtifactCursor(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	nowProto := timestamppb.New(now)

	tests := []struct {
		name     string
		artifact *essmsgpb.Artifact
		want     map[string]any
		wantErr  bool
	}{
		{
			name:     "work unit parent with url and priority",
			artifact: &essmsgpb.Artifact{Name: "artifacts/456", Parent: "experiments/123/workUnits/1", CreateTime: nowProto, UpdateTime: nowProto, Url: &essmsgpb.Url{DisplayName: "test-display", Priority: 10}, Priority: 5},
			want:     map[string]any{"xid": int64(123), "work_unit_id": int64(1), "artifact_id": int64(456), "created": now, "updated": now, "display_name": "test-display", "priority": int64(5)},
		},
		{
			name:     "experiment parent without timestamps",
			artifact: &essmsgpb.Artifact{Name: "artifacts/456", Parent: "experiments/123"},
			want:     map[string]any{"xid": int64(123), "work_unit_id": nil, "artifact_id": int64(456), "display_name": "", "priority": int64(0)},
		},
		{name: "invalid artifact name", artifact: &essmsgpb.Artifact{Name: "invalid/456", Parent: "experiments/123"}, wantErr: true},
		{name: "invalid artifact parent", artifact: &essmsgpb.Artifact{Name: "artifacts/456", Parent: "invalid/123"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractArtifactCursor(tc.artifact)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ExtractArtifactCursor() err = %v, wantErr %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ExtractArtifactCursor() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func extractAIDs(artifacts []*essmsgpb.Artifact) []int64 {
	var aids []int64
	for _, art := range artifacts {
		aid, _ := validator.ValidateArtifactName(art.GetName())
		aids = append(aids, aid)
	}
	return aids
}
