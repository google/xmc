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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	xidpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

func TestNewRepository_BindsPoolAndTx(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}

	// Any DBTX implementation can stand in for the transaction handle.
	var tx pgdb.DBTX = &pgxpool.Pool{}
	txRepo := repo.WithTx(tx)
	if txRepo.tx != tx {
		t.Errorf("WithTx(tx).tx = %v, want %v", txRepo.tx, tx)
	}
}

// A repository without a pool or transaction fails with FailedPrecondition instead of panicking.
func TestRepository_NilPoolAndTx(t *testing.T) {
	ctx := t.Context()
	repo := NewRepository(nil)
	txRepo := repo.WithTx(nil)
	entry := &xidpb.XidEntry{Acls: &xidpb.XidAcls{}}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "CreateXidEntry", call: func() error { _, err := repo.CreateXidEntry(ctx, entry); return err }},
		{name: "FetchXidEntry", call: func() error { _, err := repo.FetchXidEntry(ctx, 1); return err }},
		{name: "ListXidEntries", call: func() error { _, _, err := repo.ListXidEntries(ctx, 10, "", "", "", 0); return err }},
		{name: "Tx_FetchXidEntry", call: func() error { _, err := txRepo.FetchXidEntry(ctx, 1); return err }},
		{name: "Tx_UpdateXidEntry", call: func() error { return txRepo.UpdateXidEntry(ctx, 1, entry) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if !errors.Is(err, grpcerr.ErrFailedPrecondition) || status.Code(err) != codes.FailedPrecondition {
				t.Errorf("%s error = %v (code %v), want %v", tt.name, err, status.Code(err), grpcerr.ErrFailedPrecondition)
			}
		})
	}
}

// Nil entries are rejected before any database access.
func TestRepository_NilEntry(t *testing.T) {
	ctx := t.Context()
	repo := NewRepository(nil)
	tests := []struct {
		name string
		call func() error
	}{
		{name: "CreateXidEntry", call: func() error { _, err := repo.CreateXidEntry(ctx, nil); return err }},
		{name: "Tx_UpdateXidEntry", call: func() error { return repo.WithTx(&pgxpool.Pool{}).UpdateXidEntry(ctx, 1, nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if !errors.Is(err, grpcerr.ErrInvalidArgument) || status.Code(err) != codes.InvalidArgument {
				t.Errorf("%s error = %v (code %v), want %v", tt.name, err, status.Code(err), grpcerr.ErrInvalidArgument)
			}
		})
	}
}

// The transactional read must lock the row (AGENTS.md section 5) and must not use FOR UPDATE.
func TestLockXidSQL_LocksRowForNoKeyUpdate(t *testing.T) {
	if !strings.HasSuffix(lockXidSQL.String(), "FOR NO KEY UPDATE") {
		t.Errorf("lockXidSQL = %q, want suffix %q", lockXidSQL.String(), "FOR NO KEY UPDATE")
	}
	if strings.Contains(selectXidSQL.String(), "FOR ") {
		t.Errorf("selectXidSQL = %q, want no row lock", selectXidSQL.String())
	}
}

func TestXidRow_ScanDestinations(t *testing.T) {
	var row xidRow
	tests := []struct {
		name     string
		columns  []string
		wantNil  []int
		wantErr  bool
		wantSize int
	}{
		{
			name:     "fetched_columns_in_order",
			columns:  []string{"xid", "parentxid", "creator", "originatingsystem", "acls", "createtime", "updatetime"},
			wantSize: 7,
		},
		{
			name:     "list_columns_with_endpoint_and_duplicate_sort_column",
			columns:  []string{"xid", "parentxid", "creator", "originatingsystem", "endpoint", "acls", "createtime", "updatetime", "xid"},
			wantNil:  []int{4, 8},
			wantSize: 9,
		},
		{
			name:    "missing_column",
			columns: []string{"xid", "parentxid", "creator", "originatingsystem", "acls", "createtime"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := make([]pgconn.FieldDescription, len(tt.columns))
			for i, name := range tt.columns {
				fields[i] = pgconn.FieldDescription{Name: name}
			}
			dest, err := row.scanDestinations(fields)
			if (err != nil) != tt.wantErr {
				t.Fatalf("scanDestinations(%v) error = %v, wantErr %t", tt.columns, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(dest) != tt.wantSize {
				t.Fatalf("scanDestinations(%v) returned %d destinations, want %d", tt.columns, len(dest), tt.wantSize)
			}
			var gotNil []int
			for i, d := range dest {
				if d == nil {
					gotNil = append(gotNil, i)
				}
			}
			if diff := cmp.Diff(tt.wantNil, gotNil); diff != "" {
				t.Errorf("nil destinations mismatch (-want +got):\n%s", diff)
			}
			if dest[0] != &row.xid {
				t.Errorf("dest[0] = %p, want &row.xid (%p)", dest[0], &row.xid)
			}
		})
	}
}

func TestXidRow_ToProto(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 6000, time.UTC)
	origin := &xidpb.OriginatingSystem{Endpoint: proto.String("gke")}
	acls := &xidpb.XidAcls{Owners: []*xidpb.XidAcls_Role{{Principal: &xidpb.XidAcls_Role_User{User: "alice"}}}}
	originBytes, err := proto.Marshal(origin)
	if err != nil {
		t.Fatalf("proto.Marshal(origin) failed: %v", err)
	}
	aclsBytes, err := proto.Marshal(acls)
	if err != nil {
		t.Fatalf("proto.Marshal(acls) failed: %v", err)
	}

	tests := []struct {
		name string
		row  xidRow
		want *xidpb.XidEntry
	}{
		{
			name: "with_parent",
			row:  xidRow{xid: 42, parentXID: 7, creator: "alice", origin: originBytes, acls: aclsBytes, createTime: now, updateTime: now.Add(time.Second)},
			want: &xidpb.XidEntry{
				Name:       proto.String("xids/42"),
				ParentXid:  proto.String("xids/7"),
				Creator:    proto.String("alice"),
				Origin:     origin,
				Acls:       acls,
				CreateTime: timestamppb.New(now),
				UpdateTime: timestamppb.New(now.Add(time.Second)),
			},
		},
		{
			name: "without_parent_and_empty_protos",
			row:  xidRow{xid: 1, creator: "", origin: []byte{}, acls: []byte{}, createTime: now, updateTime: now},
			want: &xidpb.XidEntry{
				Name:       proto.String("xids/1"),
				Creator:    proto.String(""),
				Origin:     &xidpb.OriginatingSystem{},
				Acls:       &xidpb.XidAcls{},
				CreateTime: timestamppb.New(now),
				UpdateTime: timestamppb.New(now),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.row.toProto()
			if err != nil {
				t.Fatalf("toProto() failed: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("toProto() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestXidRow_ToProto_InvalidBytes(t *testing.T) {
	row := xidRow{xid: 1, acls: []byte{0xff, 0xff, 0xff, 0xff}}
	if got, err := row.toProto(); err == nil {
		t.Errorf("toProto() = %v, want error for undecodable Acls", got)
	}
}

func TestMarshalProto_NeverReturnsNil(t *testing.T) {
	tests := []struct {
		name string
		msg  proto.Message
	}{
		{name: "nil_message", msg: (*xidpb.XidAcls)(nil)},
		{name: "empty_message", msg: &xidpb.XidAcls{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := marshalProto(tt.msg)
			if err != nil {
				t.Fatalf("marshalProto() failed: %v", err)
			}
			if got == nil || len(got) != 0 {
				t.Errorf("marshalProto() = %v (nil: %t), want a non-nil empty slice", got, got == nil)
			}
		})
	}
}

func TestParentXID(t *testing.T) {
	tests := []struct {
		name    string
		entry   *xidpb.XidEntry
		want    int64
		wantErr error
	}{
		{name: "no_parent", entry: &xidpb.XidEntry{}, want: 0},
		{name: "parent", entry: &xidpb.XidEntry{ParentXid: proto.String("xids/12")}, want: 12},
		{name: "invalid_parent", entry: &xidpb.XidEntry{ParentXid: proto.String("experiments/12")}, wantErr: grpcerr.ErrInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parentXID(tt.entry)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("parentXID() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parentXID() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestExtractXidEntryCursor(t *testing.T) {
	now := time.Now()
	nowProto := timestamppb.New(now)

	tests := []struct {
		name    string
		entry   *xidpb.XidEntry
		want    map[string]any
		wantErr bool
	}{
		{
			name: "valid_xid_entry",
			entry: &xidpb.XidEntry{
				Name:       proto.String("xids/123"),
				CreateTime: nowProto,
				UpdateTime: nowProto,
			},
			// AsTime() returns UTC, the same instant as now.
			want: map[string]any{
				"xid":     int64(123),
				"created": now.UTC(),
				"updated": now.UTC(),
			},
		},
		{
			name:  "valid_xid_entry_no_timestamps",
			entry: &xidpb.XidEntry{Name: proto.String("xids/456")},
			want:  map[string]any{"xid": int64(456)},
		},
		{
			name:    "invalid_xid_name",
			entry:   &xidpb.XidEntry{Name: proto.String("invalid/789")},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractXidEntryCursor(tt.entry)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExtractXidEntryCursor(%v) error = %v, wantErr %t", tt.entry, err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ExtractXidEntryCursor(%v) mismatch (-want +got):\n%s", tt.entry, diff)
			}
		})
	}
}
