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

package xid

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
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/internal/validator"
)

func newTestRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	xidPool, _ := postgrestest.Setup(t.Context(), t)
	return NewRepository(xidPool), xidPool
}

func mustCreateXidEntry(ctx context.Context, t *testing.T, repo *Repository, creator, endpoint, parent string) (*xidmsgpb.XidEntry, int64) {
	t.Helper()
	input := &xidmsgpb.XidEntry{
		Creator: proto.String(creator),
		Origin:  &xidmsgpb.OriginatingSystem{Endpoint: proto.String(endpoint)},
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "owner@example.com"}}},
		},
	}
	if parent != "" {
		input.ParentXid = proto.String(parent)
	}
	created, err := repo.CreateXidEntry(ctx, input)
	if err != nil {
		t.Fatalf("CreateXidEntry() failed: %v", err)
	}
	xid, err := validator.ValidateXIDName(created.GetName())
	if err != nil {
		t.Fatalf("ValidateXIDName(%q) failed: %v", created.GetName(), err)
	}
	return created, xid
}

func TestNewRepository_BindsPoolAndTx(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}

	var tx pgdb.DBTX = &pgxpool.Pool{}
	txRepo := repo.WithTx(tx)
	if txRepo.tx != tx {
		t.Errorf("WithTx(tx).tx = %v, want %v", txRepo.tx, tx)
	}
}

func TestRepository_ValidationAndNilDBErrors(t *testing.T) {
	ctx := t.Context()
	repo := NewRepository(nil)
	txRepo := repo.WithTx(nil)
	nonNilTxRepo := repo.WithTx(&pgxpool.Pool{})

	tests := []struct {
		name     string
		call     func() error
		wantErr  error
		wantCode codes.Code
	}{
		{"CreateXidEntry nil entry", func() error { _, err := repo.CreateXidEntry(ctx, nil); return err }, grpcerr.ErrInvalidArgument, codes.InvalidArgument},
		{"CreateXidEntry invalid parent XID", func() error {
			_, err := repo.CreateXidEntry(ctx, &xidmsgpb.XidEntry{ParentXid: proto.String("invalid-parent")})
			return err
		}, grpcerr.ErrInvalidArgument, codes.InvalidArgument},
		{"CreateXidEntry nil db", func() error { _, err := repo.CreateXidEntry(ctx, &xidmsgpb.XidEntry{}); return err }, grpcerr.ErrFailedPrecondition, codes.FailedPrecondition},
		{"FetchXidEntry nil db", func() error { _, err := repo.FetchXidEntry(ctx, 1); return err }, grpcerr.ErrFailedPrecondition, codes.FailedPrecondition},
		{"ListXidEntries nil db", func() error { _, _, err := repo.ListXidEntries(ctx, 10, "", "", "", 0); return err }, grpcerr.ErrFailedPrecondition, codes.FailedPrecondition},
		{"TxRepository FetchXidEntry nil tx", func() error { _, err := txRepo.FetchXidEntry(ctx, 1); return err }, grpcerr.ErrFailedPrecondition, codes.FailedPrecondition},
		{"TxRepository UpdateXidEntry nil tx", func() error { return txRepo.UpdateXidEntry(ctx, 1, &xidmsgpb.XidEntry{}) }, grpcerr.ErrFailedPrecondition, codes.FailedPrecondition},
		{"TxRepository UpdateXidEntry nil entry", func() error { return nonNilTxRepo.UpdateXidEntry(ctx, 1, nil) }, grpcerr.ErrInvalidArgument, codes.InvalidArgument},
		{"TxRepository UpdateXidEntry invalid entry name", func() error {
			return nonNilTxRepo.UpdateXidEntry(ctx, 1, &xidmsgpb.XidEntry{Name: proto.String("invalid")})
		}, grpcerr.ErrInvalidArgument, codes.InvalidArgument},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.wantErr) || status.Code(err) != tc.wantCode {
				t.Errorf("%s = %v (code %v), want %v (code %v)", tc.name, err, status.Code(err), tc.wantErr, tc.wantCode)
			}
		})
	}
}

func TestCreateAndFetchXidEntry(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	inputParent := &xidmsgpb.XidEntry{
		Creator: proto.String("creator@example.com"),
		Origin:  &xidmsgpb.OriginatingSystem{Endpoint: proto.String("https://endpoint.example.com")},
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "owner@example.com"}}},
		},
	}
	createdParent, err := repo.CreateXidEntry(ctx, inputParent)
	if err != nil {
		t.Fatalf("CreateXidEntry(parent) failed: %v", err)
	}
	if inputParent.GetName() != "" || inputParent.GetCreateTime() != nil {
		t.Errorf("inputParent was mutated in place: %+v", inputParent)
	}

	createdChild, childID := mustCreateXidEntry(ctx, t, repo, "child-creator", "https://endpoint.example.com", createdParent.GetName())
	if createdChild.GetParentXid() != createdParent.GetName() {
		t.Errorf("createdChild.GetParentXid() = %q, want %q", createdChild.GetParentXid(), createdParent.GetName())
	}

	gotChild, err := repo.FetchXidEntry(ctx, childID)
	if err != nil {
		t.Fatalf("FetchXidEntry(%d) failed: %v", childID, err)
	}
	if diff := cmp.Diff(createdChild, gotChild, protocmp.Transform()); diff != "" {
		t.Errorf("FetchXidEntry(%d) mismatch (-want +got):\n%s", childID, diff)
	}

	t.Run("NotFound when missing", func(t *testing.T) {
		_, err := repo.FetchXidEntry(ctx, 99999999)
		if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
			t.Errorf("FetchXidEntry(missing) = %v (code %v), want %v", err, status.Code(err), grpcerr.ErrNotFound)
		}
	})
}

func TestTxRepository_FetchAndUpdateXidEntry(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	_, xidID := mustCreateXidEntry(ctx, t, repo, "creator@example.com", "https://endpoint.example.com", "")

	var updatedEntry *xidmsgpb.XidEntry
	err := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		txRepo := repo.WithTx(tx)
		fetched, err := txRepo.FetchXidEntry(ctx, xidID)
		if err != nil {
			return err
		}
		updatedEntry = proto.Clone(fetched).(*xidmsgpb.XidEntry)
		updatedEntry.Acls = &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "updated-owner@example.com"}}},
		}
		updatedEntry.UpdateTime = timestamppb.New(time.Now().UTC().Truncate(time.Microsecond))
		return txRepo.UpdateXidEntry(ctx, xidID, updatedEntry)
	})
	if err != nil {
		t.Fatalf("RunInTx(FetchAndUpdate) failed: %v", err)
	}

	gotEntry, err := repo.FetchXidEntry(ctx, xidID)
	if err != nil {
		t.Fatalf("FetchXidEntry(%d) failed: %v", xidID, err)
	}
	if diff := cmp.Diff(updatedEntry, gotEntry, protocmp.Transform()); diff != "" {
		t.Errorf("FetchXidEntry(%d) after update mismatch (-want +got):\n%s", xidID, diff)
	}

	t.Run("NotFound on missing XID in transaction", func(t *testing.T) {
		err := pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			_, err := repo.WithTx(tx).FetchXidEntry(ctx, 99999999)
			return err
		})
		if !errors.Is(err, grpcerr.ErrNotFound) {
			t.Errorf("TxRepository.FetchXidEntry(missing) = %v, want %v", err, grpcerr.ErrNotFound)
		}

		err = pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			return repo.WithTx(tx).UpdateXidEntry(ctx, 99999999, &xidmsgpb.XidEntry{})
		})
		if !errors.Is(err, grpcerr.ErrNotFound) {
			t.Errorf("TxRepository.UpdateXidEntry(missing) = %v, want %v", err, grpcerr.ErrNotFound)
		}
	})
}

// Concurrent read-modify-write transactions on the same XidEntry must not lose updates:
// TxRepository.FetchXidEntry locks the row with FOR NO KEY UPDATE, serializing concurrent writers.
func TestTxRepository_ConcurrentUpdatesNoLostUpdate(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	_, xidID := mustCreateXidEntry(ctx, t, repo, "creator@example.com", "https://endpoint.example.com", "")

	holdTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin() failed: %v", err)
	}
	defer func() { _ = holdTx.Rollback(context.Background()) }()
	if _, err := holdTx.Exec(ctx, lockXidSQL.String(), xidID); err != nil {
		t.Fatalf("holdTx lock failed: %v", err)
	}

	newOwners := []string{"user1@example.com", "user2@example.com", "user3@example.com"}
	errs := make([]error, len(newOwners))
	var started sync.WaitGroup
	var done sync.WaitGroup
	for i, user := range newOwners {
		started.Add(1)
		done.Go(func() {
			started.Done()
			errs[i] = pgdb.RunInTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
				txRepo := repo.WithTx(tx)
				entry, err := txRepo.FetchXidEntry(ctx, xidID)
				if err != nil {
					return err
				}
				entry.Acls.Owners = append(entry.Acls.Owners, &xidmsgpb.XidAcls_Role{
					Principal: &xidmsgpb.XidAcls_Role_User{User: user},
				})
				entry.UpdateTime = timestamppb.New(time.Now().UTC().Truncate(time.Microsecond))
				return txRepo.UpdateXidEntry(ctx, xidID, entry)
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

	got, err := repo.FetchXidEntry(ctx, xidID)
	if err != nil {
		t.Fatalf("FetchXidEntry() failed: %v", err)
	}
	var gotUsers []string
	for _, r := range got.GetAcls().GetOwners() {
		gotUsers = append(gotUsers, r.GetUser())
	}
	slices.Sort(gotUsers)
	wantUsers := []string{"owner@example.com", "user1@example.com", "user2@example.com", "user3@example.com"}
	if diff := cmp.Diff(wantUsers, gotUsers); diff != "" {
		t.Errorf("owners after concurrent updates mismatch (-want +got); lost update detected:\n%s", diff)
	}
}

func TestListXidEntries_FiltersAndOrdering(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	e1, xid1 := mustCreateXidEntry(ctx, t, repo, "C1", "E1", "")
	time.Sleep(2 * time.Millisecond)
	e2, _ := mustCreateXidEntry(ctx, t, repo, "C2", "E1", "")
	time.Sleep(2 * time.Millisecond)
	e3, _ := mustCreateXidEntry(ctx, t, repo, "C1", "E2", "")

	filterTests := []struct {
		name   string
		filter string
		want   []*xidmsgpb.XidEntry
	}{
		{name: "all", filter: "", want: []*xidmsgpb.XidEntry{e1, e2, e3}},
		{name: "creator=C1", filter: `creator="C1"`, want: []*xidmsgpb.XidEntry{e1, e3}},
		{name: "creator=C2", filter: `creator="C2"`, want: []*xidmsgpb.XidEntry{e2}},
		{name: "endpoint=E1", filter: `endpoint="E1"`, want: []*xidmsgpb.XidEntry{e1, e2}},
		{name: "endpoint=E2", filter: `endpoint="E2"`, want: []*xidmsgpb.XidEntry{e3}},
		{name: "xid", filter: fmt.Sprintf("xid=%d", xid1), want: []*xidmsgpb.XidEntry{e1}},
		{name: "xid_or_creator_normalized", filter: fmt.Sprintf("xid=\"%d\" or creator=C2", xid1), want: []*xidmsgpb.XidEntry{e1, e2}},
	}
	for _, tc := range filterTests {
		t.Run("filter_"+tc.name, func(t *testing.T) {
			got, _, err := repo.ListXidEntries(ctx, 10, "", tc.filter, "", 0)
			if err != nil {
				t.Fatalf("ListXidEntries(filter=%q) failed: %v", tc.filter, err)
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ListXidEntries(filter=%q) mismatch (-want +got):\n%s", tc.filter, diff)
			}
		})
	}

	asc := []*xidmsgpb.XidEntry{e1, e2, e3}
	desc := []*xidmsgpb.XidEntry{e3, e2, e1}
	orderTests := []struct {
		orderBy string
		want    []*xidmsgpb.XidEntry
	}{
		{orderBy: "xid", want: asc},
		{orderBy: "xid desc", want: desc},
		{orderBy: "created", want: asc},
		{orderBy: "created desc", want: desc},
		{orderBy: "updated", want: asc},
		{orderBy: "updated desc", want: desc},
	}
	for _, tc := range orderTests {
		t.Run("order_"+tc.orderBy, func(t *testing.T) {
			got, _, err := repo.ListXidEntries(ctx, 10, "", "", tc.orderBy, 0)
			if err != nil {
				t.Fatalf("ListXidEntries(orderBy=%q) failed: %v", tc.orderBy, err)
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ListXidEntries(orderBy=%q) mismatch (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

func TestListXidEntries_MultiPagePagination(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	specs := []struct{ creator, endpoint string }{
		{"C", "E1"}, {"B", "E2"}, {"A", "E1"}, {"C", "E2"}, {"B", "E1"},
	}
	entries := make([]*xidmsgpb.XidEntry, len(specs))
	for i, s := range specs {
		entries[i], _ = mustCreateXidEntry(ctx, t, repo, s.creator, s.endpoint, "")
		time.Sleep(2 * time.Millisecond)
	}

	t.Run("default order multi-page traversal", func(t *testing.T) {
		var collected []*xidmsgpb.XidEntry
		var pageToken string
		wantPageSizes := []int{2, 2, 1}
		for i, wantSize := range wantPageSizes {
			page, nextToken, err := repo.ListXidEntries(ctx, 2, pageToken, "", "", 0)
			if err != nil {
				t.Fatalf("ListXidEntries page %d failed: %v", i+1, err)
			}
			if len(page) != wantSize {
				t.Errorf("page %d len = %d, want %d", i+1, len(page), wantSize)
			}
			if (i < len(wantPageSizes)-1) != (nextToken != "") {
				t.Errorf("page %d nextToken = %q, want non-empty=%v", i+1, nextToken, i < len(wantPageSizes)-1)
			}
			collected = append(collected, page...)
			pageToken = nextToken
		}
		if diff := cmp.Diff(entries, collected, protocmp.Transform()); diff != "" {
			t.Errorf("paginated entries mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("filtered and ordered multi-page traversal", func(t *testing.T) {
		page1, token1, err := repo.ListXidEntries(ctx, 2, "", `endpoint="E1"`, "created desc", 0)
		if err != nil || token1 == "" {
			t.Fatalf("ListXidEntries page 1 err=%v token=%q, want nil error and non-empty token", err, token1)
		}
		if diff := cmp.Diff([]*xidmsgpb.XidEntry{entries[4], entries[2]}, page1, protocmp.Transform()); diff != "" {
			t.Errorf("page 1 mismatch (-want +got):\n%s", diff)
		}
		page2, token2, err := repo.ListXidEntries(ctx, 2, token1, `endpoint="E1"`, "created desc", 0)
		if err != nil || token2 != "" {
			t.Fatalf("ListXidEntries page 2 err=%v token=%q, want nil error and empty token", err, token2)
		}
		if diff := cmp.Diff([]*xidmsgpb.XidEntry{entries[0]}, page2, protocmp.Transform()); diff != "" {
			t.Errorf("page 2 mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestExtractXidEntryCursor(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	nowProto := timestamppb.New(now)

	tests := []struct {
		name    string
		entry   *xidmsgpb.XidEntry
		want    map[string]any
		wantErr bool
	}{
		{"with timestamps", &xidmsgpb.XidEntry{Name: proto.String("xids/123"), CreateTime: nowProto, UpdateTime: nowProto}, map[string]any{"xid": int64(123), "created": now, "updated": now}, false},
		{"without timestamps", &xidmsgpb.XidEntry{Name: proto.String("xids/456")}, map[string]any{"xid": int64(456)}, false},
		{"invalid name", &xidmsgpb.XidEntry{Name: proto.String("invalid/789")}, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractXidEntryCursor(tc.entry)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ExtractXidEntryCursor() err = %v, wantErr %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ExtractXidEntryCursor() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
