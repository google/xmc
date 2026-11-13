// Copyright 2026 Google LLC
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
package spannerdata

import (
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func mustNewRow(t *testing.T, cols []string, vals []any) *spanner.Row {
	t.Helper()
	row, err := spanner.NewRow(cols, vals)
	if err != nil {
		t.Fatalf("spanner.NewRow(%v, %v) failed: %v", cols, vals, err)
	}
	return row
}

func TestReadColumns(t *testing.T) {
	type decoded struct {
		ID   int64
		Name string
		Tags []string
		Msg  *statuspb.Status
	}

	sampleMsg := &statuspb.Status{Code: 3, Message: "invalid"}
	tests := []struct {
		name        string
		row         *spanner.Row
		cols        func(out *decoded) []colDest
		want        decoded
		wantErrPart string
	}{
		{
			name: "decodes multiple scalar and proto columns",
			row: mustNewRow(t,
				[]string{"Id", "Name", "Tags", "Msg"},
				[]any{int64(42), "exp-1", []string{"a", "b"}, sampleMsg},
			),
			cols: func(out *decoded) []colDest {
				return []colDest{
					{"Id", &out.ID},
					{"Name", &out.Name},
					{"Tags", &out.Tags},
					{"Msg", out.Msg},
				}
			},
			want: decoded{
				ID:   42,
				Name: "exp-1",
				Tags: []string{"a", "b"},
				Msg:  sampleMsg,
			},
		},
		{
			name: "no column destinations succeeds",
			row:  mustNewRow(t, []string{"Id"}, []any{int64(1)}),
			cols: func(*decoded) []colDest { return nil },
			want: decoded{Msg: &statuspb.Status{}},
		},
		{
			name: "stops at missing column and wraps column name",
			row:  mustNewRow(t, []string{"Id", "Name"}, []any{int64(7), "ignored"}),
			cols: func(out *decoded) []colDest {
				return []colDest{
					{"Id", &out.ID},
					{"MissingCol", &out.Name},
					{"Name", &out.Name},
				}
			},
			want:        decoded{ID: 7, Msg: &statuspb.Status{}},
			wantErrPart: "failed to read column MissingCol",
		},
		{
			name: "type mismatch returns wrapped column error",
			row:  mustNewRow(t, []string{"Id"}, []any{"not-an-int64"}),
			cols: func(out *decoded) []colDest {
				return []colDest{{"Id", &out.ID}}
			},
			want:        decoded{Msg: &statuspb.Status{}},
			wantErrPart: "failed to read column Id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decoded{Msg: &statuspb.Status{}}
			err := readColumns(tt.row, tt.cols(&got)...)
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("readColumns() error = %v, want substring %q", err, tt.wantErrPart)
				}
			} else if err != nil {
				t.Fatalf("readColumns() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("readColumns() decoded diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestReadNullTimestamp(t *testing.T) {
	now := time.Date(2026, time.March, 15, 10, 30, 0, 123456000, time.UTC)
	tests := []struct {
		name        string
		row         *spanner.Row
		col         string
		want        *timestamppb.Timestamp
		wantErrPart string
	}{
		{
			name: "valid timestamp",
			row:  mustNewRow(t, []string{"CreateTime"}, []any{spanner.NullTime{Time: now, Valid: true}}),
			col:  "CreateTime",
			want: timestamppb.New(now),
		},
		{
			name: "null timestamp returns nil",
			row:  mustNewRow(t, []string{"CreateTime"}, []any{spanner.NullTime{Valid: false}}),
			col:  "CreateTime",
			want: nil,
		},
		{
			name:        "missing column returns error",
			row:         mustNewRow(t, []string{"Other"}, []any{now}),
			col:         "CreateTime",
			wantErrPart: "failed to read column CreateTime",
		},
		{
			name:        "incompatible column type returns error",
			row:         mustNewRow(t, []string{"CreateTime"}, []any{int64(123)}),
			col:         "CreateTime",
			wantErrPart: "failed to read column CreateTime",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readNullTimestamp(tt.row, tt.col)
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("readNullTimestamp(%q) error = %v, want substring %q", tt.col, err, tt.wantErrPart)
				}
				return
			}
			if err != nil {
				t.Fatalf("readNullTimestamp(%q) unexpected error: %v", tt.col, err)
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("readNullTimestamp(%q) diff (-want +got):\n%s", tt.col, diff)
			}
		})
	}
}

func TestReadNullTimestamps(t *testing.T) {
	t1 := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	t2 := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)

	type tsTargets struct {
		First  *timestamppb.Timestamp
		Second *timestamppb.Timestamp
		Third  *timestamppb.Timestamp
	}

	tests := []struct {
		name        string
		row         *spanner.Row
		init        tsTargets
		cols        func(out *tsTargets) []tsDest
		want        tsTargets
		wantErrPart string
	}{
		{
			name: "reads mix of valid and null timestamps and clears stale destination",
			row: mustNewRow(t,
				[]string{"First", "Second", "Third"},
				[]any{t1, spanner.NullTime{Valid: false}, t2},
			),
			init: tsTargets{
				Second: timestamppb.New(t1),
			},
			cols: func(out *tsTargets) []tsDest {
				return []tsDest{
					{"First", &out.First},
					{"Second", &out.Second},
					{"Third", &out.Third},
				}
			},
			want: tsTargets{
				First:  timestamppb.New(t1),
				Second: nil,
				Third:  timestamppb.New(t2),
			},
		},
		{
			name: "no timestamp destinations succeeds",
			row:  mustNewRow(t, []string{"First"}, []any{t1}),
			cols: func(*tsTargets) []tsDest { return nil },
			want: tsTargets{},
		},
		{
			name: "stops on missing timestamp column",
			row:  mustNewRow(t, []string{"First", "Third"}, []any{t1, t2}),
			cols: func(out *tsTargets) []tsDest {
				return []tsDest{
					{"First", &out.First},
					{"MissingTime", &out.Second},
					{"Third", &out.Third},
				}
			},
			want: tsTargets{
				First: timestamppb.New(t1),
			},
			wantErrPart: "failed to read column MissingTime",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.init
			err := readNullTimestamps(tt.row, tt.cols(&got)...)
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("readNullTimestamps() error = %v, want substring %q", err, tt.wantErrPart)
				}
			} else if err != nil {
				t.Fatalf("readNullTimestamps() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("readNullTimestamps() diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestReadNullProto(t *testing.T) {
	validStatus := &statuspb.Status{Code: 3, Message: "invalid argument"}
	encodedStatus, marshalErr := proto.Marshal(&statuspb.Status{Code: 7, Message: "permission denied"})
	if marshalErr != nil {
		t.Fatalf("proto.Marshal failed: %v", marshalErr)
	}

	tests := []struct {
		name        string
		row         *spanner.Row
		col         string
		want        *statuspb.Status
		wantErrPart string
	}{
		{
			name: "valid proto message value",
			row: mustNewRow(t, []string{"Status"}, []any{
				spanner.NullProtoMessage{Valid: true, ProtoMessageVal: validStatus},
			}),
			col:  "Status",
			want: validStatus,
		},
		{
			name: "valid raw proto bytes",
			row:  mustNewRow(t, []string{"Status"}, []any{encodedStatus}),
			col:  "Status",
			want: &statuspb.Status{Code: 7, Message: "permission denied"},
		},
		{
			name: "null proto message via typed nil returns nil",
			row:  mustNewRow(t, []string{"Status"}, []any{(*statuspb.Status)(nil)}),
			col:  "Status",
			want: nil,
		},
		{
			name: "null proto message via NullProtoMessage typed nil returns nil",
			row: mustNewRow(t, []string{"Status"}, []any{
				spanner.NullProtoMessage{Valid: true, ProtoMessageVal: (*statuspb.Status)(nil)},
			}),
			col:  "Status",
			want: nil,
		},
		{
			name:        "missing column returns wrapped error",
			row:         mustNewRow(t, []string{"Other"}, []any{"x"}),
			col:         "Status",
			wantErrPart: "failed to read column Status",
		},
		{
			name:        "incompatible column type returns wrapped error",
			row:         mustNewRow(t, []string{"Status"}, []any{int64(99)}),
			col:         "Status",
			wantErrPart: "failed to read column Status",
		},
		{
			name:        "corrupt proto bytes returns wrapped error",
			row:         mustNewRow(t, []string{"Status"}, []any{[]byte{0xff, 0xff}}),
			col:         "Status",
			wantErrPart: "failed to read column Status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readNullProto(tt.row, tt.col, &statuspb.Status{})
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("readNullProto(%q) error = %v, want substring %q", tt.col, err, tt.wantErrPart)
				}
				if got != nil {
					t.Fatalf("readNullProto(%q) on error returned %v, want nil", tt.col, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("readNullProto(%q) unexpected error: %v", tt.col, err)
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("readNullProto(%q) diff (-want +got):\n%s", tt.col, diff)
			}
		})
	}

	t.Run("nil target returns error without panicking", func(t *testing.T) {
		row := mustNewRow(t, []string{"Status"}, []any{validStatus})
		got, err := readNullProto[proto.Message](row, "Status", nil)
		if err == nil || !strings.Contains(err.Error(), "failed to read column Status") {
			t.Fatalf("readNullProto(nil target) error = %v, want wrapped column read error", err)
		}
		if got != nil {
			t.Fatalf("readNullProto(nil target) returned %v, want nil", got)
		}
	})
}
