// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package xid_test

import (
	"context"
	"fmt"
	"github.com/google/xmc/internal/storage/spanner"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	xidpb "github.com/google/xmc/api/protos/xid"
	"github.com/google/xmc/internal/storage/spanner/emulator"
	"github.com/google/xmc/internal/storage/spanner/utils"
	"github.com/google/xmc/internal/storage/spanner/xid"
	"github.com/google/xmc/internal/validator"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

func TestListXidEntries(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(xidClient)

	// Create some entries
	var entries []*xidpb.XidEntry
	for i := 0; i < 5; i++ {
		entry := &xidpb.XidEntry{
			Creator: proto.String(fmt.Sprintf("creator-%d", i)),
			Origin: &xidpb.OriginatingSystem{
				Endpoint: proto.String("test-endpoint"),
			},
			Acls: &xidpb.XidAcls{},
		}
		createdEntry, err := storage.CreateXidEntry(ctx, entry)
		if err != nil {
			t.Fatalf("CreateXidEntry failed: %v", err)
		}
		entries = append(entries, createdEntry)
	}

	// List all entries
	gotEntries, _, err := storage.ListXidEntries(ctx, 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListXidEntries failed: %v", err)
	}
	if len(gotEntries) != 5 {
		t.Errorf("ListXidEntries returned %d entries, want 5", len(gotEntries))
	}

	// Sort entries by XID for comparison
	sort.Slice(entries, func(i, j int) bool {
		iXID, _ := validator.ValidateXIDName(entries[i].GetName())
		jXID, _ := validator.ValidateXIDName(entries[j].GetName())
		return iXID < jXID
	})
	sort.Slice(gotEntries, func(i, j int) bool {
		iXID, _ := validator.ValidateXIDName(gotEntries[i].GetName())
		jXID, _ := validator.ValidateXIDName(gotEntries[j].GetName())
		return iXID < jXID
	})
	if diff := cmp.Diff(entries, gotEntries, protocmp.Transform()); diff != "" {
		t.Errorf("ListXidEntries() returned diff (-want +got):\n%s", diff)
	}
}

func TestListXidEntries_Pagination(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(xidClient)

	// Create some entries
	for i := 0; i < 5; i++ {
		entry := &xidpb.XidEntry{
			Creator: proto.String(fmt.Sprintf("creator-%d", i)),
			Origin: &xidpb.OriginatingSystem{
				Endpoint: proto.String("test-endpoint"),
			},
			Acls: &xidpb.XidAcls{},
		}
		_, err := storage.CreateXidEntry(ctx, entry)
		if err != nil {
			t.Fatalf("CreateXidEntry failed: %v", err)
		}
	}

	tests := []struct {
		pageSize   int
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
				xidEntries, nextPageToken, err := storage.ListXidEntries(ctx, int32(tc.pageSize), pageToken, "", "xid", 0)
				if err != nil {
					t.Fatalf("ListXidEntries failed: %v", err)
				}
				gotCounts = append(gotCounts, len(xidEntries))
				pageToken = nextPageToken
				if diff := cmp.Diff(tc.wantCounts[i], len(xidEntries)); diff != "" {
					t.Errorf("ListXidEntries returned unexpected page count (-want +got):\n%s", diff)
				}
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

func TestListXidEntries_FilterFields(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(xidClient)

	// Create some entries
	entry1, err := storage.CreateXidEntry(ctx, &xidpb.XidEntry{
		Creator: proto.String("C1"),
		Origin:  &xidpb.OriginatingSystem{Endpoint: proto.String("E1")},
		Acls:    &xidpb.XidAcls{},
	})
	if err != nil {
		t.Fatalf("CreateXidEntry failed: %v", err)
	}
	entry2, err := storage.CreateXidEntry(ctx, &xidpb.XidEntry{
		Creator: proto.String("C2"),
		Origin:  &xidpb.OriginatingSystem{Endpoint: proto.String("E1")},
		Acls:    &xidpb.XidAcls{},
	})
	if err != nil {
		t.Fatalf("CreateXidEntry failed: %v", err)
	}
	entry3, err := storage.CreateXidEntry(ctx, &xidpb.XidEntry{
		Creator: proto.String("C1"),
		Origin:  &xidpb.OriginatingSystem{Endpoint: proto.String("E2")},
		Acls:    &xidpb.XidAcls{},
	})
	if err != nil {
		t.Fatalf("CreateXidEntry failed: %v", err)
	}
	xid1, _ := validator.ValidateXIDName(entry1.GetName())

	testCases := []struct {
		name   string
		filter string
		want   []*xidpb.XidEntry
	}{
		{
			name:   `creator="C1"`,
			filter: `creator="C1"`,
			want:   []*xidpb.XidEntry{entry1, entry3},
		},
		{
			name:   `creator="C2"`,
			filter: `creator="C2"`,
			want:   []*xidpb.XidEntry{entry2},
		},
		{
			name:   `endpoint="E1"`,
			filter: `endpoint="E1"`,
			want:   []*xidpb.XidEntry{entry1, entry2},
		},
		{
			name:   `endpoint="E2"`,
			filter: `endpoint="E2"`,
			want:   []*xidpb.XidEntry{entry3},
		},
		{
			name:   "xid",
			filter: fmt.Sprintf("xid=%d", xid1),
			want:   []*xidpb.XidEntry{entry1},
		},
		{
			name:   "xid_or_creator",
			filter: fmt.Sprintf("xid=%d OR creator=\"C2\"", xid1),
			want:   []*xidpb.XidEntry{entry1, entry2},
		},
		{
			name:   "xid_or_creator_normalized",
			filter: fmt.Sprintf("xid=\"%d\" or creator=C2", xid1),
			want:   []*xidpb.XidEntry{entry1, entry2},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := storage.ListXidEntries(ctx, 10, "", tc.filter, "", 0)
			if err != nil {
				t.Fatalf("ListXidEntries with filter=%q failed: %v", tc.filter, err)
			}
			sort.Slice(got, func(i, j int) bool {
				iXID, _ := validator.ValidateXIDName(got[i].GetName())
				jXID, _ := validator.ValidateXIDName(got[j].GetName())
				return iXID < jXID
			})
			sort.Slice(tc.want, func(i, j int) bool {
				iXID, _ := validator.ValidateXIDName(tc.want[i].GetName())
				jXID, _ := validator.ValidateXIDName(tc.want[j].GetName())
				return iXID < jXID
			})
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ListXidEntries with filter=%q returned diff (-want +got):\n%s", tc.filter, diff)
			}
		})
	}
}

func TestListXidEntries_OrderByFields(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(xidClient)

	var entries []*xidpb.XidEntry
	for i := 0; i < 3; i++ {
		entry := &xidpb.XidEntry{
			Creator: proto.String(fmt.Sprintf("creator-%d", i)),
			Origin:  &xidpb.OriginatingSystem{Endpoint: proto.String("test-endpoint")},
			Acls:    &xidpb.XidAcls{},
		}
		created, err := storage.CreateXidEntry(ctx, entry)
		if err != nil {
			t.Fatalf("CreateXidEntry failed: %v", err)
		}
		entries = append(entries, created)
	}

	testCases := []struct {
		name    string
		orderBy string
		want    []*xidpb.XidEntry
	}{
		{
			name:    "xid_asc",
			orderBy: "xid",
			want:    []*xidpb.XidEntry{entries[0], entries[1], entries[2]},
		},
		{
			name:    "xid_desc",
			orderBy: "xid desc",
			want:    []*xidpb.XidEntry{entries[2], entries[1], entries[0]},
		},
		{
			name:    "created_asc",
			orderBy: "created",
			want:    []*xidpb.XidEntry{entries[0], entries[1], entries[2]},
		},
		{
			name:    "created_desc",
			orderBy: "created desc",
			want:    []*xidpb.XidEntry{entries[2], entries[1], entries[0]},
		},
		{
			name:    "updated_asc",
			orderBy: "updated",
			want:    []*xidpb.XidEntry{entries[0], entries[1], entries[2]},
		},
		{
			name:    "updated_desc",
			orderBy: "updated desc",
			want:    []*xidpb.XidEntry{entries[2], entries[1], entries[0]},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := storage.ListXidEntries(ctx, 10, "", "", tc.orderBy, 0)
			if err != nil {
				t.Fatalf("ListXidEntries with orderBy=%q failed: %v", tc.orderBy, err)
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ListXidEntries with orderBy=%q returned diff (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

func TestListXidEntries_Complex(t *testing.T) {
	ctx := context.Background()
	xidClient, _ := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(xidClient)

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
		entry := &xidpb.XidEntry{
			Creator: proto.String(d.creator),
			Origin: &xidpb.OriginatingSystem{
				Endpoint: proto.String(d.endpoint),
			},
			Acls: &xidpb.XidAcls{},
		}
		createdEntry, err := storage.CreateXidEntry(ctx, entry)
		if err != nil {
			t.Fatalf("CreateXidEntry failed: %v", err)
		}
		entries[i] = createdEntry
	}

	// List page 1: filter by endpoint=E1, order by created desc, page size 2
	filter := `endpoint="E1"`
	orderBy := "created desc"
	pageSize := int32(2)

	gotPage1, page1Token, err := storage.ListXidEntries(ctx, pageSize, "", filter, orderBy, 0)
	if err != nil {
		t.Fatalf("ListXidEntries page 1 failed: %v", err)
	}
	if page1Token == "" {
		t.Errorf("ListXidEntries page 1 got empty next page token, want non-empty")
	}

	wantPage1 := []*xidpb.XidEntry{entries[4], entries[2]} // B, A
	if diff := cmp.Diff(wantPage1, gotPage1, protocmp.Transform()); diff != "" {
		t.Errorf("ListXidEntries page 1 returned diff (-want +got):\n%s", diff)
	}

	// List page 2
	gotPage2, page2Token, err := storage.ListXidEntries(ctx, pageSize, page1Token, filter, orderBy, 0)
	if err != nil {
		t.Fatalf("ListXidEntries page 2 failed: %v", err)
	}
	if page2Token != "" {
		t.Errorf("ListXidEntries page 2 got next page token %q, want empty", page2Token)
	}
	wantPage2 := []*xidpb.XidEntry{entries[0]} // C
	if diff := cmp.Diff(wantPage2, gotPage2, protocmp.Transform()); diff != "" {
		t.Errorf("ListXidEntries page 2 returned diff (-want +got):\n%s", diff)
	}
}

func TestExtractXidEntryCursor(t *testing.T) {
	now := time.Now()
	nowProto := timestamppb.New(now)

	testCases := []struct {
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
			want: map[string]any{
				"xid":              int64(123),
				"bit_reversed_xid": spannerutils.ReverseBits(123),
				"created":          now,
				"updated":          now,
			},
			wantErr: false,
		},
		{
			name: "valid_xid_entry_no_timestamps",
			entry: &xidpb.XidEntry{
				Name: proto.String("xids/456"),
			},
			want: map[string]any{
				"xid":              int64(456),
				"bit_reversed_xid": spannerutils.ReverseBits(456),
			},
			wantErr: false,
		},
		{
			name: "invalid_xid_name",
			entry: &xidpb.XidEntry{
				Name: proto.String("invalid/789"),
			},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := xid.ExtractXidEntryCursor(tc.entry)
			if (err != nil) != tc.wantErr {
				t.Errorf("extractXidEntryCursor(%v) returned error %v, wantErr=%t", tc.entry, err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("extractXidEntryCursor(%v) returned diff (-want +got):\n%s", tc.entry, diff)
			}
		})
	}
}
