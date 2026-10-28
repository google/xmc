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

// External test package exercising the repository against a real PostgreSQL XID database. The
// tests port the Spanner repository tests in internal/storage/spanner/xid and add the pagination
// and concurrency coverage that internal/storage/AGENTS.md requires.
package xid_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	xidpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/internal/storage/postgres/xid"
	"xmanager_cloud/internal/validator"
)

const (
	// clockTolerance bounds how far a repository-set timestamp may be from the test's clock.
	clockTolerance = time.Minute
	// lostUpdateWindow is how long each concurrent read-modify-write waits between its read and
	// its write. Without a row lock, concurrent transactions then read the same entry and
	// overwrite each other's updates; with the lock, they merely run one after another.
	lostUpdateWindow = 20 * time.Millisecond
)

// storedRow holds the raw column values of one Xids row, read with SQL that bypasses the
// repository.
type storedRow struct {
	ParentXid  int64
	Creator    string
	Endpoint   string
	CreateTime time.Time
	UpdateTime time.Time
}

// newTestRepository returns a repository bound to a fresh XID test database, together with the
// database's pool for transactions and raw SQL assertions.
func newTestRepository(t *testing.T) (*xid.Repository, *pgxpool.Pool) {
	t.Helper()
	xidPool, _ := postgrestest.Setup(t.Context(), t)
	return xid.NewRepository(xidPool), xidPool
}

// runInTx runs fn with a TxRepository bound to a transaction of its own, retrying it the way
// storage.TransactionRunner does.
func runInTx(ctx context.Context, pool *pgxpool.Pool, repo *xid.Repository, fn func(ctx context.Context, tx storage.XidTxRepository) error) error {
	return pgdb.RunInTx(ctx, pool, func(ctx context.Context, pgTx pgx.Tx) error {
		return fn(ctx, repo.WithTx(pgTx))
	})
}

// newEntry returns an entry to create, with one owner per given user.
func newEntry(creator, endpoint string, owners ...string) *xidpb.XidEntry {
	acls := &xidpb.XidAcls{}
	for _, owner := range owners {
		acls.Owners = append(acls.Owners, &xidpb.XidAcls_Role{Principal: &xidpb.XidAcls_Role_User{User: owner}})
	}
	return &xidpb.XidEntry{
		Creator: proto.String(creator),
		Origin:  &xidpb.OriginatingSystem{Endpoint: proto.String(endpoint)},
		Acls:    acls,
	}
}

// mustCreate creates entry or fails the test.
func mustCreate(ctx context.Context, t *testing.T, repo *xid.Repository, entry *xidpb.XidEntry) *xidpb.XidEntry {
	t.Helper()
	created, err := repo.CreateXidEntry(ctx, entry)
	if err != nil {
		t.Fatalf("CreateXidEntry(%v) unexpected error: %v", entry, err)
	}
	return created
}

// createEntries creates n entries with creators creator-0 .. creator-(n-1) in that order.
func createEntries(ctx context.Context, t *testing.T, repo *xid.Repository, n int) []*xidpb.XidEntry {
	t.Helper()
	entries := make([]*xidpb.XidEntry, n)
	for i := range entries {
		entries[i] = mustCreate(ctx, t, repo, newEntry(fmt.Sprintf("creator-%d", i), "test-endpoint"))
	}
	return entries
}

// xidOf parses the XID of entry's name or fails the test.
func xidOf(t *testing.T, entry *xidpb.XidEntry) int64 {
	t.Helper()
	id, err := validator.ValidateXIDName(entry.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", entry.GetName(), err)
	}
	return id
}

// sortByXID sorts entries by ascending XID in place.
func sortByXID(t *testing.T, entries []*xidpb.XidEntry) {
	t.Helper()
	sort.Slice(entries, func(i, j int) bool { return xidOf(t, entries[i]) < xidOf(t, entries[j]) })
}

// owners returns the user of every owner role of entry.
func owners(entry *xidpb.XidEntry) []string {
	var users []string
	for _, role := range entry.GetAcls().GetOwners() {
		users = append(users, role.GetUser())
	}
	return users
}

// queryStoredRow reads the raw Xids row of id with SQL that bypasses the repository.
func queryStoredRow(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id int64) storedRow {
	t.Helper()
	const query = "SELECT ParentXid, Creator, Endpoint, CreateTime, UpdateTime FROM Xids WHERE Xid = $1"
	var got storedRow
	if err := pool.QueryRow(ctx, query, id).Scan(&got.ParentXid, &got.Creator, &got.Endpoint, &got.CreateTime, &got.UpdateTime); err != nil {
		t.Fatalf("QueryRow(%q, %d) unexpected error: %v", query, id, err)
	}
	return got
}

// listAllPages lists every entry page by page and returns the entries in order, together with
// the page sizes seen.
func listAllPages(ctx context.Context, t *testing.T, repo *xid.Repository, pageSize int32, filter, orderBy string) ([]*xidpb.XidEntry, []int) {
	t.Helper()
	var (
		all       []*xidpb.XidEntry
		counts    []int
		pageToken string
	)
	for page := 1; ; page++ {
		entries, nextPageToken, err := repo.ListXidEntries(ctx, pageSize, pageToken, filter, orderBy, 0)
		if err != nil {
			t.Fatalf("ListXidEntries(page %d, token %q) unexpected error: %v", page, pageToken, err)
		}
		all = append(all, entries...)
		counts = append(counts, len(entries))
		if nextPageToken == "" {
			return all, counts
		}
		if len(entries) != int(pageSize) {
			t.Fatalf("ListXidEntries(page %d) returned %d entries with a next page token, want a full page of %d", page, len(entries), pageSize)
		}
		pageToken = nextPageToken
	}
}

// appendOwner is a read-modify-write: it reads the entry of id, appends user to its owners and
// writes the entry back.
func appendOwner(ctx context.Context, tx storage.XidTxRepository, id int64, user string) error {
	entry, err := tx.FetchXidEntry(ctx, id)
	if err != nil {
		return err
	}
	time.Sleep(lostUpdateWindow)
	entry.Acls.Owners = append(entry.Acls.Owners, &xidpb.XidAcls_Role{Principal: &xidpb.XidAcls_Role_User{User: user}})
	entry.UpdateTime = timestamppb.Now()
	return tx.UpdateXidEntry(ctx, id, entry)
}

func TestCreateXidEntry(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)

	parentEntry := newEntry("test-creator", "parent-endpoint", "test-owner")
	createdParentEntry := mustCreate(ctx, t, repo, parentEntry)

	entry := newEntry("test-creator", "test-endpoint", "test-owner")
	entry.ParentXid = proto.String(createdParentEntry.GetName())
	createdEntry := mustCreate(ctx, t, repo, entry)

	if createdEntry != entry {
		t.Errorf("CreateXidEntry returned a different entry than it was given, want the input entry updated in place")
	}
	if createdEntry.GetName() == "" {
		t.Errorf("CreateXidEntry returned entry with empty name")
	}
	if createdEntry.GetName() == createdParentEntry.GetName() {
		t.Errorf("CreateXidEntry assigned the parent's name %q to the child", createdEntry.GetName())
	}
	if createdEntry.GetCreateTime() == nil {
		t.Errorf("CreateXidEntry returned entry with nil CreateTime")
	}
	if createdEntry.GetUpdateTime() == nil {
		t.Errorf("CreateXidEntry returned entry with nil UpdateTime")
	}
	if !createdEntry.GetCreateTime().AsTime().Equal(createdEntry.GetUpdateTime().AsTime()) {
		t.Errorf("CreateXidEntry returned CreateTime %v != UpdateTime %v", createdEntry.GetCreateTime().AsTime(), createdEntry.GetUpdateTime().AsTime())
	}
	if since := time.Since(createdEntry.GetCreateTime().AsTime()); since < 0 || since > clockTolerance {
		t.Errorf("CreateXidEntry returned CreateTime %v, want within %v of now", createdEntry.GetCreateTime().AsTime(), clockTolerance)
	}
	if createdEntry.GetCreator() != "test-creator" {
		t.Errorf("CreateXidEntry returned entry with wrong creator: got %q, want %q", createdEntry.GetCreator(), "test-creator")
	}
	if createdEntry.GetParentXid() != createdParentEntry.GetName() {
		t.Errorf("CreateXidEntry returned entry with wrong parent: got %q, want %q", createdEntry.GetParentXid(), createdParentEntry.GetName())
	}

	// The derived Endpoint column and the ParentXid default are written as the schema documents.
	wantParentRow := storedRow{ParentXid: 0, Creator: "test-creator", Endpoint: "parent-endpoint"}
	gotParentRow := queryStoredRow(ctx, t, pool, xidOf(t, createdParentEntry))
	if diff := cmp.Diff(wantParentRow, gotParentRow, ignoreStoredTimes()); diff != "" {
		t.Errorf("stored parent row mismatch (-want +got):\n%s", diff)
	}
	wantRow := storedRow{ParentXid: xidOf(t, createdParentEntry), Creator: "test-creator", Endpoint: "test-endpoint"}
	gotRow := queryStoredRow(ctx, t, pool, xidOf(t, createdEntry))
	if diff := cmp.Diff(wantRow, gotRow, ignoreStoredTimes()); diff != "" {
		t.Errorf("stored row mismatch (-want +got):\n%s", diff)
	}
	if !gotRow.CreateTime.Equal(createdEntry.GetCreateTime().AsTime()) || !gotRow.UpdateTime.Equal(createdEntry.GetUpdateTime().AsTime()) {
		t.Errorf("stored times = (%v, %v), want the returned times (%v, %v)", gotRow.CreateTime, gotRow.UpdateTime, createdEntry.GetCreateTime().AsTime(), createdEntry.GetUpdateTime().AsTime())
	}
}

// ignoreStoredTimes compares storedRow values without their timestamps.
func ignoreStoredTimes() cmp.Option {
	return cmp.FilterPath(func(p cmp.Path) bool {
		field, ok := p.Last().(cmp.StructField)
		return ok && (field.Name() == "CreateTime" || field.Name() == "UpdateTime")
	}, cmp.Ignore())
}

func TestCreateXidEntry_AssignsIncreasingIDs(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	entries := createEntries(ctx, t, repo, 3)
	for i := 1; i < len(entries); i++ {
		if prev, cur := xidOf(t, entries[i-1]), xidOf(t, entries[i]); cur <= prev {
			t.Errorf("entry %d got XID %d, want greater than the previous XID %d", i, cur, prev)
		}
	}
}

func TestCreateXidEntry_InvalidArguments(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	tests := []struct {
		name  string
		entry *xidpb.XidEntry
	}{
		{name: "nil_entry", entry: nil},
		{name: "invalid_parent_name", entry: &xidpb.XidEntry{Creator: proto.String("c"), Acls: &xidpb.XidAcls{}, ParentXid: proto.String("experiments/1")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.CreateXidEntry(ctx, tt.entry)
			if !errors.Is(err, grpcerr.ErrInvalidArgument) || status.Code(err) != codes.InvalidArgument {
				t.Errorf("CreateXidEntry() = %v, %v (code %v); want %v", got, err, status.Code(err), grpcerr.ErrInvalidArgument)
			}
		})
	}
}

func TestFetchXidEntry(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)

	parentEntry := newEntry("test-creator", "test-endpoint")
	createdParentEntry := mustCreate(ctx, t, repo, parentEntry)

	entry := newEntry("test-creator", "test-endpoint", "owner@example.com")
	entry.ParentXid = proto.String(createdParentEntry.GetName())
	createdEntry := mustCreate(ctx, t, repo, entry)
	id := xidOf(t, createdEntry)

	fetchers := []struct {
		name  string
		fetch func(ctx context.Context, id int64) (*xidpb.XidEntry, error)
	}{
		{name: "Repository", fetch: repo.FetchXidEntry},
		{name: "TxRepository", fetch: func(ctx context.Context, id int64) (*xidpb.XidEntry, error) {
			var got *xidpb.XidEntry
			err := runInTx(ctx, pool, repo, func(ctx context.Context, tx storage.XidTxRepository) error {
				var err error
				got, err = tx.FetchXidEntry(ctx, id)
				return err
			})
			return got, err
		}},
	}
	for _, f := range fetchers {
		t.Run(f.name, func(t *testing.T) {
			gotEntry, err := f.fetch(ctx, id)
			if err != nil {
				t.Fatalf("FetchXidEntry(%d) unexpected error: %v", id, err)
			}
			if diff := cmp.Diff(createdEntry, gotEntry, protocmp.Transform()); diff != "" {
				t.Errorf("FetchXidEntry(%d) mismatch (-want +got):\n%s", id, diff)
			}

			gotParent, err := f.fetch(ctx, xidOf(t, createdParentEntry))
			if err != nil {
				t.Fatalf("FetchXidEntry(parent) unexpected error: %v", err)
			}
			if diff := cmp.Diff(createdParentEntry, gotParent, protocmp.Transform()); diff != "" {
				t.Errorf("FetchXidEntry(parent) mismatch (-want +got):\n%s", diff)
			}
			if gotParent.ParentXid != nil {
				t.Errorf("FetchXidEntry(parent).ParentXid = %q, want unset for a stored ParentXid of 0", gotParent.GetParentXid())
			}
		})
	}
}

func TestFetchXidEntry_NotFound(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	const missing = 123456789

	_, err := repo.FetchXidEntry(ctx, missing)
	if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
		t.Errorf("FetchXidEntry(%d) error = %v (code %v), want %v", missing, err, status.Code(err), grpcerr.ErrNotFound)
	}

	err = runInTx(ctx, pool, repo, func(ctx context.Context, tx storage.XidTxRepository) error {
		_, err := tx.FetchXidEntry(ctx, missing)
		return err
	})
	if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
		t.Errorf("TxRepository.FetchXidEntry(%d) error = %v (code %v), want %v", missing, err, status.Code(err), grpcerr.ErrNotFound)
	}
}

func TestFetchXidEntry_ContextErrorIsPassedThrough(t *testing.T) {
	repo, _ := newTestRepository(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if got, err := repo.FetchXidEntry(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("FetchXidEntry(canceled ctx) = %v, %v; want error %v", got, err, context.Canceled)
	}
}

func TestTxRepository_FetchAndUpdateXidEntry(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)

	createdEntry := mustCreate(ctx, t, repo, newEntry("test-creator", "test-endpoint", "user1@example.com"))
	id := xidOf(t, createdEntry)

	var updatedEntry *xidpb.XidEntry
	err := runInTx(ctx, pool, repo, func(ctx context.Context, tx storage.XidTxRepository) error {
		fetched, err := tx.FetchXidEntry(ctx, id)
		if err != nil {
			return err
		}
		updatedEntry = proto.Clone(fetched).(*xidpb.XidEntry)
		updatedEntry.Acls = &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				{Principal: &xidpb.XidAcls_Role_User{User: "user2@example.com"}},
			},
		}
		updatedEntry.UpdateTime = timestamppb.New(time.Now().UTC().Truncate(time.Microsecond))
		return tx.UpdateXidEntry(ctx, id, updatedEntry)
	})
	if err != nil {
		t.Fatalf("RunInTx unexpected error: %v", err)
	}

	gotEntry, err := repo.FetchXidEntry(ctx, id)
	if err != nil {
		t.Fatalf("FetchXidEntry(%d) unexpected error: %v", id, err)
	}
	if diff := cmp.Diff(updatedEntry, gotEntry, protocmp.Transform()); diff != "" {
		t.Errorf("FetchXidEntry(%d) after tx update mismatch (-want +got):\n%s", id, diff)
	}
}

// UpdateXidEntry writes only the ACLs and the update time: changes to any other field of the
// given entry are not persisted, as with the Spanner update mutation.
func TestTxRepository_UpdateXidEntry_WritesOnlyAclsAndUpdateTime(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)

	createdEntry := mustCreate(ctx, t, repo, newEntry("test-creator", "test-endpoint", "user1@example.com"))
	id := xidOf(t, createdEntry)
	updateTime := timestamppb.New(time.Now().UTC().Truncate(time.Microsecond))

	err := runInTx(ctx, pool, repo, func(ctx context.Context, tx storage.XidTxRepository) error {
		return tx.UpdateXidEntry(ctx, id, &xidpb.XidEntry{
			Name:       proto.String("xids/999"),
			Creator:    proto.String("other-creator"),
			Origin:     &xidpb.OriginatingSystem{Endpoint: proto.String("other-endpoint")},
			ParentXid:  proto.String("xids/1"),
			Acls:       &xidpb.XidAcls{Owners: []*xidpb.XidAcls_Role{{Principal: &xidpb.XidAcls_Role_User{User: "user2@example.com"}}}},
			CreateTime: updateTime,
			UpdateTime: updateTime,
		})
	})
	if err != nil {
		t.Fatalf("RunInTx unexpected error: %v", err)
	}

	want := proto.Clone(createdEntry).(*xidpb.XidEntry)
	want.Acls = &xidpb.XidAcls{Owners: []*xidpb.XidAcls_Role{{Principal: &xidpb.XidAcls_Role_User{User: "user2@example.com"}}}}
	want.UpdateTime = updateTime
	got, err := repo.FetchXidEntry(ctx, id)
	if err != nil {
		t.Fatalf("FetchXidEntry(%d) unexpected error: %v", id, err)
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("FetchXidEntry(%d) after update mismatch (-want +got):\n%s", id, diff)
	}
}

func TestTxRepository_UpdateXidEntry_RolledBack(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	errAbort := errors.New("abort")

	createdEntry := mustCreate(ctx, t, repo, newEntry("test-creator", "test-endpoint", "user1@example.com"))
	id := xidOf(t, createdEntry)

	err := runInTx(ctx, pool, repo, func(ctx context.Context, tx storage.XidTxRepository) error {
		if err := appendOwner(ctx, tx, id, "user2@example.com"); err != nil {
			return err
		}
		return errAbort
	})
	if !errors.Is(err, errAbort) {
		t.Fatalf("RunInTx error = %v, want %v", err, errAbort)
	}

	got, err := repo.FetchXidEntry(ctx, id)
	if err != nil {
		t.Fatalf("FetchXidEntry(%d) unexpected error: %v", id, err)
	}
	if diff := cmp.Diff(createdEntry, got, protocmp.Transform()); diff != "" {
		t.Errorf("FetchXidEntry(%d) after rolled back update mismatch (-want +got):\n%s", id, diff)
	}
}

func TestTxRepository_UpdateXidEntry_Errors(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	entry := newEntry("test-creator", "test-endpoint", "user1@example.com")
	entry.UpdateTime = timestamppb.Now()

	if err := repo.WithTx(nil).UpdateXidEntry(ctx, 1, entry); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("UpdateXidEntry with nil tx error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}

	tests := []struct {
		name     string
		id       int64
		entry    *xidpb.XidEntry
		wantErr  error
		wantCode codes.Code
	}{
		{name: "nil_entry", id: 1, entry: nil, wantErr: grpcerr.ErrInvalidArgument, wantCode: codes.InvalidArgument},
		{name: "missing_row", id: 123456789, entry: entry, wantErr: grpcerr.ErrNotFound, wantCode: codes.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runInTx(ctx, pool, repo, func(ctx context.Context, tx storage.XidTxRepository) error {
				return tx.UpdateXidEntry(ctx, tt.id, tt.entry)
			})
			if !errors.Is(err, tt.wantErr) || status.Code(err) != tt.wantCode {
				t.Errorf("UpdateXidEntry(%d) error = %v (code %v), want %v", tt.id, err, status.Code(err), tt.wantErr)
			}
		})
	}
}

// Concurrent read-modify-write transactions must not lose updates: TxRepository.FetchXidEntry
// locks the row, so each transaction reads the entry that the previous one committed.
func TestTxRepository_FetchXidEntryLocksRowAgainstLostUpdates(t *testing.T) {
	const workers = 10
	ctx := t.Context()
	repo, pool := newTestRepository(t)

	createdEntry := mustCreate(ctx, t, repo, newEntry("test-creator", "test-endpoint"))
	id := xidOf(t, createdEntry)
	wantOwners := make([]string, workers)
	for i := range wantOwners {
		wantOwners[i] = fmt.Sprintf("user-%02d@example.com", i)
	}

	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i, owner := range wantOwners {
		wg.Go(func() {
			errs[i] = runInTx(ctx, pool, repo, func(ctx context.Context, tx storage.XidTxRepository) error {
				return appendOwner(ctx, tx, id, owner)
			})
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("RunInTx unexpected error: %v", err)
	}

	got, err := repo.FetchXidEntry(ctx, id)
	if err != nil {
		t.Fatalf("FetchXidEntry(%d) unexpected error: %v", id, err)
	}
	gotOwners := owners(got)
	slices.Sort(gotOwners)
	if diff := cmp.Diff(wantOwners, gotOwners); diff != "" {
		t.Errorf("owners after concurrent updates mismatch (-want +got); missing owners are lost updates:\n%s", diff)
	}
}

func TestListXidEntries(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	entries := createEntries(ctx, t, repo, 5)

	gotEntries, nextPageToken, err := repo.ListXidEntries(ctx, 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListXidEntries unexpected error: %v", err)
	}
	if nextPageToken != "" {
		t.Errorf("ListXidEntries returned next page token %q for a single page, want empty", nextPageToken)
	}
	if len(gotEntries) != 5 {
		t.Errorf("ListXidEntries returned %d entries, want 5", len(gotEntries))
	}

	sortByXID(t, entries)
	sortByXID(t, gotEntries)
	if diff := cmp.Diff(entries, gotEntries, protocmp.Transform()); diff != "" {
		t.Errorf("ListXidEntries() mismatch (-want +got):\n%s", diff)
	}
}

func TestListXidEntries_Empty(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	gotEntries, nextPageToken, err := repo.ListXidEntries(ctx, 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListXidEntries unexpected error: %v", err)
	}
	if len(gotEntries) != 0 || nextPageToken != "" {
		t.Errorf("ListXidEntries on an empty table = %v, %q; want no entries and an empty token", gotEntries, nextPageToken)
	}
}

func TestListXidEntries_Pagination(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)
	createEntries(ctx, t, repo, 5)

	tests := []struct {
		pageSize   int32
		wantCounts []int
	}{
		{3, []int{3, 2}},
		{5, []int{5}},
		{1, []int{1, 1, 1, 1, 1}},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("pageSize=%d", tc.pageSize), func(t *testing.T) {
			var gotCounts []int
			var pageToken string
			for i := 0; i < len(tc.wantCounts); i++ {
				xidEntries, nextPageToken, err := repo.ListXidEntries(ctx, tc.pageSize, pageToken, "", "xid", 0)
				if err != nil {
					t.Fatalf("ListXidEntries unexpected error: %v", err)
				}
				gotCounts = append(gotCounts, len(xidEntries))
				pageToken = nextPageToken
				if i < len(tc.wantCounts)-1 && nextPageToken == "" {
					t.Errorf("ListXidEntries returned empty nextPageToken on page %d, want non-empty", i+1)
				}
			}
			if pageToken != "" {
				t.Errorf("ListXidEntries returned non-empty nextPageToken on last page, want empty")
			}
			if diff := cmp.Diff(tc.wantCounts, gotCounts); diff != "" {
				t.Errorf("ListXidEntries returned unexpected page counts (-want +got):\n%s", diff)
			}
		})
	}
}

// Without an orderBy, pages follow the declared default ordering of the Xids table view, XID
// ascending, consistently across page boundaries: no entry is skipped or repeated.
func TestListXidEntries_DefaultOrderingAcrossPages(t *testing.T) {
	const (
		total    = 7
		pageSize = 3
	)
	ctx := t.Context()
	repo, _ := newTestRepository(t)
	entries := createEntries(ctx, t, repo, total)
	sortByXID(t, entries)

	got, counts := listAllPages(ctx, t, repo, pageSize, "", "")
	if diff := cmp.Diff([]int{3, 3, 1}, counts); diff != "" {
		t.Errorf("page sizes mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(entries, got, protocmp.Transform()); diff != "" {
		t.Errorf("entries across pages mismatch (-want +got):\n%s", diff)
	}
}

// Explicit orderings, including descending ones, stay consistent across page boundaries.
func TestListXidEntries_OrderByAcrossPages(t *testing.T) {
	const (
		total    = 7
		pageSize = 2
	)
	ctx := t.Context()
	repo, _ := newTestRepository(t)
	entries := createEntries(ctx, t, repo, total)
	sortByXID(t, entries)
	reversed := slices.Clone(entries)
	slices.Reverse(reversed)

	tests := []struct {
		orderBy string
		want    []*xidpb.XidEntry
	}{
		{orderBy: "xid", want: entries},
		{orderBy: "xid desc", want: reversed},
		{orderBy: "created", want: entries},
		{orderBy: "created desc", want: reversed},
		{orderBy: "updated desc", want: reversed},
		{orderBy: "updated desc, xid", want: reversed},
	}
	for _, tt := range tests {
		t.Run(tt.orderBy, func(t *testing.T) {
			got, counts := listAllPages(ctx, t, repo, pageSize, "", tt.orderBy)
			if diff := cmp.Diff([]int{2, 2, 2, 1}, counts); diff != "" {
				t.Errorf("page sizes mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("entries across pages mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestListXidEntries_FilterFields(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	entry1 := mustCreate(ctx, t, repo, newEntry("C1", "E1"))
	entry2 := mustCreate(ctx, t, repo, newEntry("C2", "E1"))
	entry3 := mustCreate(ctx, t, repo, newEntry("C1", "E2"))
	xid1 := xidOf(t, entry1)

	testCases := []struct {
		name   string
		filter string
		want   []*xidpb.XidEntry
	}{
		{name: `creator="C1"`, filter: `creator="C1"`, want: []*xidpb.XidEntry{entry1, entry3}},
		{name: `creator="C2"`, filter: `creator="C2"`, want: []*xidpb.XidEntry{entry2}},
		{name: `endpoint="E1"`, filter: `endpoint="E1"`, want: []*xidpb.XidEntry{entry1, entry2}},
		{name: `endpoint="E2"`, filter: `endpoint="E2"`, want: []*xidpb.XidEntry{entry3}},
		{name: "xid", filter: fmt.Sprintf("xid=%d", xid1), want: []*xidpb.XidEntry{entry1}},
		{name: "xid_or_creator", filter: fmt.Sprintf("xid=%d OR creator=\"C2\"", xid1), want: []*xidpb.XidEntry{entry1, entry2}},
		{name: "xid_or_creator_normalized", filter: fmt.Sprintf("xid=\"%d\" or creator=C2", xid1), want: []*xidpb.XidEntry{entry1, entry2}},
		{name: "xid_and_creator", filter: fmt.Sprintf("xid=%d AND creator=\"C2\"", xid1), want: nil},
		{name: "parent_xid", filter: "parent_xid=0", want: []*xidpb.XidEntry{entry1, entry2, entry3}},
		{name: "no_match", filter: `creator="nobody"`, want: nil},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := repo.ListXidEntries(ctx, 10, "", tc.filter, "", 0)
			if err != nil {
				t.Fatalf("ListXidEntries with filter=%q unexpected error: %v", tc.filter, err)
			}
			sortByXID(t, got)
			sortByXID(t, tc.want)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ListXidEntries with filter=%q mismatch (-want +got):\n%s", tc.filter, diff)
			}
		})
	}
}

func TestListXidEntries_OrderByFields(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)
	entries := createEntries(ctx, t, repo, 3)

	testCases := []struct {
		name    string
		orderBy string
		want    []*xidpb.XidEntry
	}{
		{name: "xid_asc", orderBy: "xid", want: []*xidpb.XidEntry{entries[0], entries[1], entries[2]}},
		{name: "xid_desc", orderBy: "xid desc", want: []*xidpb.XidEntry{entries[2], entries[1], entries[0]}},
		{name: "created_asc", orderBy: "created", want: []*xidpb.XidEntry{entries[0], entries[1], entries[2]}},
		{name: "created_desc", orderBy: "created desc", want: []*xidpb.XidEntry{entries[2], entries[1], entries[0]}},
		{name: "updated_asc", orderBy: "updated", want: []*xidpb.XidEntry{entries[0], entries[1], entries[2]}},
		{name: "updated_desc", orderBy: "updated desc", want: []*xidpb.XidEntry{entries[2], entries[1], entries[0]}},
		{name: "default", orderBy: "", want: []*xidpb.XidEntry{entries[0], entries[1], entries[2]}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := repo.ListXidEntries(ctx, 10, "", "", tc.orderBy, 0)
			if err != nil {
				t.Fatalf("ListXidEntries with orderBy=%q unexpected error: %v", tc.orderBy, err)
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ListXidEntries with orderBy=%q mismatch (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

func TestListXidEntries_Complex(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	testData := []struct {
		creator  string
		endpoint string
	}{
		{"C", "E1"},
		{"B", "E2"},
		{"A", "E1"},
		{"C", "E2"},
		{"B", "E1"},
	}
	entries := make([]*xidpb.XidEntry, len(testData))
	for i, d := range testData {
		entries[i] = mustCreate(ctx, t, repo, newEntry(d.creator, d.endpoint))
	}

	// Page 1: filter by endpoint=E1, order by created desc, page size 2.
	filter := `endpoint="E1"`
	orderBy := "created desc"
	pageSize := int32(2)

	gotPage1, page1Token, err := repo.ListXidEntries(ctx, pageSize, "", filter, orderBy, 0)
	if err != nil {
		t.Fatalf("ListXidEntries page 1 unexpected error: %v", err)
	}
	if page1Token == "" {
		t.Errorf("ListXidEntries page 1 got empty next page token, want non-empty")
	}
	wantPage1 := []*xidpb.XidEntry{entries[4], entries[2]} // B, A
	if diff := cmp.Diff(wantPage1, gotPage1, protocmp.Transform()); diff != "" {
		t.Errorf("ListXidEntries page 1 mismatch (-want +got):\n%s", diff)
	}

	// Page 2.
	gotPage2, page2Token, err := repo.ListXidEntries(ctx, pageSize, page1Token, filter, orderBy, 0)
	if err != nil {
		t.Fatalf("ListXidEntries page 2 unexpected error: %v", err)
	}
	if page2Token != "" {
		t.Errorf("ListXidEntries page 2 got next page token %q, want empty", page2Token)
	}
	wantPage2 := []*xidpb.XidEntry{entries[0]} // C
	if diff := cmp.Diff(wantPage2, gotPage2, protocmp.Transform()); diff != "" {
		t.Errorf("ListXidEntries page 2 mismatch (-want +got):\n%s", diff)
	}
}

func TestListXidEntries_InvalidArguments(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)
	createEntries(ctx, t, repo, 1)

	tests := []struct {
		name      string
		pageToken string
		filter    string
		orderBy   string
	}{
		{name: "malformed_filter", filter: `creator =`},
		{name: "unknown_filter_field", filter: `nickname="x"`},
		{name: "unknown_order_by_field", orderBy: "name"},
		{name: "malformed_order_by", orderBy: "xid sideways"},
		{name: "undecodable_page_token", pageToken: "not base64!"},
		{name: "page_token_with_unknown_field", pageToken: base64.RawURLEncoding.EncodeToString([]byte(`{"bit_reversed_xid":1}`))},
		{name: "page_token_missing_sort_field", pageToken: base64.RawURLEncoding.EncodeToString([]byte(`{"created":"2026-01-01T00:00:00Z"}`)), orderBy: "xid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, token, err := repo.ListXidEntries(ctx, 10, tt.pageToken, tt.filter, tt.orderBy, 0)
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("ListXidEntries() = %v, %q, %v (code %v); want code %v", got, token, err, status.Code(err), codes.InvalidArgument)
			}
		})
	}

	if got, token, err := repo.ListXidEntries(ctx, 0, "", "", "", 0); err == nil {
		t.Errorf("ListXidEntries(pageSize 0) = %v, %q, nil; want error", got, token)
	}
}

// Staleness has no PostgreSQL equivalent: reads are always strong, so a just-created entry is
// listed even with a large staleness.
func TestListXidEntries_IgnoresStaleness(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)
	created := mustCreate(ctx, t, repo, newEntry("creator", "endpoint"))

	got, _, err := repo.ListXidEntries(ctx, 10, "", "", "", 15*time.Second)
	if err != nil {
		t.Fatalf("ListXidEntries unexpected error: %v", err)
	}
	if diff := cmp.Diff([]*xidpb.XidEntry{created}, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListXidEntries with staleness mismatch (-want +got):\n%s", diff)
	}
}
