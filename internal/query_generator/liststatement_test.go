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
package liststatement

import (
	"context"
	"strings"
	"testing"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/cmp/cmp"
	"golang/cmp/cmpopts/cmpopts"
	"googlesql/safesql/go/safesql"
	"xmanager_cloud/query_generator/querytools"
)

func TestListStatement(t *testing.T) {
	testCases := []struct {
		name       string
		params     ListParams
		tables     []querytools.TableView
		wantStmt   *spanner.Statement
		wantOffset int64
		wantErr    bool
	}{
		{
			name: "no filter",
			params: ListParams{
				PageSize: 10,
				DefaultSelectFields: []querytools.TableField{
					querytools.AlbumTitleTableField,
				},
				ValidOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.AlbumTitleTableField},
					{TableField: querytools.ReleaseDateTableField},
				},
				IndexOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.SingerIDTableField, SortOrder: querytools.Asc},
					{TableField: querytools.AlbumIDTableField, SortOrder: querytools.Asc},
				},
			},
			tables: querytools.AlbumsTables,
			wantStmt: &spanner.Statement{
				SQL: "SELECT a.AlbumTitle, a.SingerId, a.AlbumId FROM Albums a ORDER BY a.SingerId, a.AlbumId LIMIT 11",
			},
			wantOffset: 0,
		},
		{
			name: "with filter",
			params: ListParams{
				PageSize: 10,
				Filter:   `AlbumTitle = "foo"`,
				DefaultSelectFields: []querytools.TableField{
					querytools.AlbumIDTableField,
				},
				ValidOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.AlbumTitleTableField},
					{TableField: querytools.ReleaseDateTableField},
				},
				IndexOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.AlbumTitleTableField, SortOrder: querytools.Asc},
					{TableField: querytools.ReleaseDateTableField, SortOrder: querytools.Asc},
				},
			},
			tables: querytools.AlbumsTables,
			wantStmt: &spanner.Statement{
				SQL:    "SELECT a.AlbumId, a.ReleaseDate FROM Albums@{FORCE_INDEX=AlbumsByTitleReleaseDate} a WHERE (a.AlbumTitle = @fb0) ORDER BY a.AlbumTitle, a.ReleaseDate LIMIT 11",
				Params: map[string]any{"fb0": "foo"},
			},
			wantOffset: 0,
		},
		{
			name: "with order by",
			params: ListParams{
				PageSize: 10,
				OrderBy:  "AlbumTitle",
				DefaultSelectFields: []querytools.TableField{
					querytools.AlbumIDTableField,
				},
				ValidOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.AlbumTitleTableField},
					{TableField: querytools.ReleaseDateTableField},
				},
				IndexOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.SingerIDTableField, SortOrder: querytools.Asc},
					{TableField: querytools.AlbumIDTableField, SortOrder: querytools.Asc},
				},
			},
			tables: querytools.AlbumsTables,
			wantStmt: &spanner.Statement{
				SQL: "SELECT a.AlbumId, a.AlbumTitle, a.SingerId FROM Albums a ORDER BY a.AlbumTitle, a.SingerId, a.AlbumId LIMIT 11",
			},
			wantOffset: 0,
		},
		{
			name: "with order by desc",
			params: ListParams{
				PageSize: 10,
				OrderBy:  "AlbumTitle desc",
				DefaultSelectFields: []querytools.TableField{
					querytools.AlbumIDTableField,
				},
				ValidOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.AlbumTitleTableField},
					{TableField: querytools.ReleaseDateTableField},
				},
				IndexOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.SingerIDTableField, SortOrder: querytools.Asc},
					{TableField: querytools.AlbumIDTableField, SortOrder: querytools.Asc},
				},
			},
			tables: querytools.AlbumsTables,
			wantStmt: &spanner.Statement{
				SQL: "SELECT a.AlbumId, a.AlbumTitle, a.SingerId FROM Albums a ORDER BY a.AlbumTitle DESC, a.SingerId, a.AlbumId LIMIT 11",
			},
			wantOffset: 0,
		},
		{
			name: "with page offset",
			params: ListParams{
				PageSize:   10,
				PageOffset: 20,
				DefaultSelectFields: []querytools.TableField{
					querytools.AlbumTitleTableField,
				},
				ValidOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.AlbumTitleTableField},
					{TableField: querytools.ReleaseDateTableField},
				},
				IndexOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.SingerIDTableField, SortOrder: querytools.Asc},
					{TableField: querytools.AlbumIDTableField, SortOrder: querytools.Asc},
				},
			},
			tables: querytools.AlbumsTables,
			wantStmt: &spanner.Statement{
				SQL: "SELECT a.AlbumTitle, a.SingerId, a.AlbumId FROM Albums a ORDER BY a.SingerId, a.AlbumId LIMIT 11 OFFSET 20",
			},
			wantOffset: 20,
		},
		{
			name: "full text search",
			params: ListParams{
				PageSize: 10,
				Filter:   `"foo"`,
				DefaultSelectFields: []querytools.TableField{
					querytools.AlbumIDTableField,
				},
				DefaultFullTextFields: []safesql.TrustedSQLString{
					safesql.New("a.AlbumTitle"),
				},
				ValidOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.AlbumTitleTableField},
					{TableField: querytools.ReleaseDateTableField},
				},
				IndexOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.SingerIDTableField, SortOrder: querytools.Asc},
					{TableField: querytools.AlbumIDTableField, SortOrder: querytools.Asc},
				},
			},
			tables: querytools.AlbumsTables,
			wantStmt: &spanner.Statement{
				SQL:    "SELECT a.AlbumId, a.SingerId FROM Albums a WHERE (SEARCH(a.AlbumTitle, @fb0)) ORDER BY a.SingerId, a.AlbumId LIMIT 11",
				Params: map[string]any{"fb0": "foo"},
			},
			wantOffset: 0,
		},
		{
			name: "with after cursor",
			params: ListParams{
				PageSize: 10,
				AfterCursor: map[string]any{
					string(querytools.SingerIDTableField): "singer1",
					string(querytools.AlbumIDTableField):  "album1",
				},
				DefaultSelectFields: []querytools.TableField{
					querytools.AlbumTitleTableField,
				},
				ValidOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.AlbumTitleTableField},
					{TableField: querytools.ReleaseDateTableField},
				},
				IndexOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.SingerIDTableField, SortOrder: querytools.Asc},
					{TableField: querytools.AlbumIDTableField, SortOrder: querytools.Asc},
				},
			},
			tables: querytools.AlbumsTables,
			wantStmt: &spanner.Statement{
				SQL: "SELECT a.AlbumTitle, a.SingerId, a.AlbumId FROM Albums a WHERE ((a.SingerId = @val20 AND a.AlbumId > @val21) OR (a.SingerId > @val10)) ORDER BY a.SingerId, a.AlbumId LIMIT 11",
				Params: map[string]any{
					"val10": "singer1",
					"val20": "singer1",
					"val21": "album1",
				},
			},
			wantOffset: 0,
		},
		{
			name: "invalid filter",
			params: ListParams{
				PageSize: 10,
				Filter:   `invalid = field`,
			},
			tables:  querytools.AlbumsTables,
			wantErr: true,
		},
		{
			name: "both page offset and after cursor error",
			params: ListParams{
				PageSize:   10,
				PageOffset: 20,
				AfterCursor: map[string]any{
					string(querytools.SingerIDTableField): "singer1",
				},
			},
			tables:  querytools.AlbumsTables,
			wantErr: true,
		},
		{
			name: "invalid order by",
			params: ListParams{
				PageSize: 10,
				OrderBy:  `invalidfield`,
				ValidOrderByFields: []querytools.UserOrderedIndex{
					{TableField: querytools.AlbumTitleTableField},
				},
			},
			tables:  querytools.AlbumsTables,
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			qg, err := querytools.NewQueryGenerator(tc.tables, nil, nil, nil)
			if err != nil {
				t.Fatalf("Failed to create query generator: %v", err)
			}
			tc.params.QueryGen = qg
			stmt, offset, err := ListStatement(ctx, tc.params)

			if tc.wantErr {
				if err == nil {
					t.Errorf("ListStatement() got nil error, want non-nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("ListStatement() returned an unexpected error: %v", err)
			}

			if diff := cmp.Diff(tc.wantStmt, stmt, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ListStatement() returned diff (-want +got):\n%s", diff)
			}

			if offset != tc.wantOffset {
				t.Errorf("ListStatement() returned offset %d, want %d", offset, tc.wantOffset)
			}
		})
	}
}

func TestGenerateOrderBy(t *testing.T) {
	validFields := []querytools.UserOrderedIndex{
		{TableField: "field1"},
		{TableField: "field2"},
	}

	testCases := []struct {
		name      string
		orderBy   string
		want      []querytools.UserOrderedIndex
		wantInErr string
	}{
		{
			name:    "single field asc",
			orderBy: "field1",
			want: []querytools.UserOrderedIndex{
				{TableField: "field1", SortOrder: querytools.Asc},
			},
		},
		{
			name:    "single field asc explicit",
			orderBy: "field1 asc",
			want: []querytools.UserOrderedIndex{
				{TableField: "field1", SortOrder: querytools.Asc},
			},
		},
		{
			name:    "single field desc",
			orderBy: "field1 desc",
			want: []querytools.UserOrderedIndex{
				{TableField: "field1", SortOrder: querytools.Desc},
			},
		},
		{
			name:    "multiple fields",
			orderBy: "field1, field2 desc",
			want: []querytools.UserOrderedIndex{
				{TableField: "field1", SortOrder: querytools.Asc},
				{TableField: "field2", SortOrder: querytools.Desc},
			},
		},
		{
			name:      "invalid field",
			orderBy:   "field3",
			wantInErr: "not a valid field",
		},
		{
			name:      "invalid direction",
			orderBy:   "field1 descending",
			wantInErr: "invalid direction",
		},
		{
			name:      "too many spaces",
			orderBy:   "field1 desc asc",
			wantInErr: "more than one space",
		},
		{
			name:      "empty segment",
			orderBy:   "field1,,field2",
			wantInErr: "empty or whitespace-only segments",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			got, err := generateOrderBy(ctx, tc.orderBy, validFields)
			if tc.wantInErr != "" {
				if err == nil {
					t.Errorf("generateOrderBy(%q) got nil error, want error containing %q", tc.orderBy, tc.wantInErr)
				} else if !strings.Contains(err.Error(), tc.wantInErr) {
					t.Errorf("generateOrderBy(%q) got error %q, want error containing %q", tc.orderBy, err.Error(), tc.wantInErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("generateOrderBy(%q) returned an unexpected error: %v", tc.orderBy, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("generateOrderBy(%q) returned diff (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

func TestDecodeOffsetPageToken(t *testing.T) {
	testCases := []struct {
		name      string
		pageToken string
		want      int64
		wantErr   bool
	}{
		{
			name:      "empty token",
			pageToken: "",
			want:      0,
		},
		{
			name:      "valid token",
			pageToken: "100",
			want:      100,
		},
		{
			name:      "invalid token",
			pageToken: "abc",
			wantErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeOffsetPageToken(tc.pageToken)
			if tc.wantErr {
				if err == nil {
					t.Errorf("DecodeOffsetPageToken(%q) got nil error, want error", tc.pageToken)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeOffsetPageToken(%q) returned an unexpected error: %v", tc.pageToken, err)
			}
			if got != tc.want {
				t.Errorf("DecodeOffsetPageToken(%q) = %d, want %d", tc.pageToken, got, tc.want)
			}
		})
	}
}

func TestEncodeOffsetPageToken(t *testing.T) {
	testCases := []struct {
		name       string
		pageOffset int64
		want       string
	}{
		{
			name:       "zero offset",
			pageOffset: 0,
			want:       "0",
		},
		{
			name:       "positive offset",
			pageOffset: 123,
			want:       "123",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := EncodeOffsetPageToken(tc.pageOffset)
			if got != tc.want {
				t.Errorf("EncodeOffsetPageToken(%d) = %q, want %q", tc.pageOffset, got, tc.want)
			}
		})
	}
}

func TestApplyOffsetPagination(t *testing.T) {
	testCases := []struct {
		name          string
		results       []int
		pageSize      int32
		currentOffset int64
		wantResults   []int
		wantToken     string
	}{
		{
			name:          "no truncation",
			results:       []int{1, 2, 3},
			pageSize:      5,
			currentOffset: 10,
			wantResults:   []int{1, 2, 3},
			wantToken:     "",
		},
		{
			name:          "with truncation",
			results:       []int{1, 2, 3, 4, 5, 6},
			pageSize:      5,
			currentOffset: 10,
			wantResults:   []int{1, 2, 3, 4, 5},
			wantToken:     "15",
		},
		{
			name:          "empty results",
			results:       []int{},
			pageSize:      5,
			currentOffset: 10,
			wantResults:   []int{},
			wantToken:     "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotResults, gotToken := ApplyOffsetPagination(tc.results, tc.pageSize, tc.currentOffset)
			if diff := cmp.Diff(tc.wantResults, gotResults); diff != "" {
				t.Errorf("ApplyOffsetPagination() results diff (-want +got):\n%s", diff)
			}
			if gotToken != tc.wantToken {
				t.Errorf("ApplyOffsetPagination() token got %q, want %q", gotToken, tc.wantToken)
			}
		})
	}
}

func TestIsCursorPageToken(t *testing.T) {
	testCases := []struct {
		name           string
		pageToken      string
		defaultOnEmpty bool
		want           bool
	}{
		{
			name:           "empty token, default false",
			pageToken:      "",
			defaultOnEmpty: false,
			want:           false,
		},
		{
			name:           "empty token, default true",
			pageToken:      "",
			defaultOnEmpty: true,
			want:           true,
		},
		{
			name:           "offset token, default false",
			pageToken:      "100",
			defaultOnEmpty: false,
			want:           false,
		},
		{
			name:           "offset token, default true",
			pageToken:      "100",
			defaultOnEmpty: true,
			want:           false,
		},
		{
			name:           "cursor token, default false",
			pageToken:      "eyJhIjoib25lIiwiYiI6Mn0",
			defaultOnEmpty: false,
			want:           true,
		},
		{
			name:           "random string token, default true",
			pageToken:      "random",
			defaultOnEmpty: true,
			want:           true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsCursorPageToken(tc.pageToken, tc.defaultOnEmpty)
			if got != tc.want {
				t.Errorf("IsCursorPageToken(%q, %v) = %v, want %v", tc.pageToken, tc.defaultOnEmpty, got, tc.want)
			}
		})
	}
}

func TestDecodeAfterCursorPageToken(t *testing.T) {
	testCases := []struct {
		name      string
		pageToken string
		want      map[string]any
		wantErr   bool
	}{
		{
			name:      "empty token",
			pageToken: "",
			want:      nil,
		},
		{
			name:      "valid token",
			pageToken: "eyJhIjoib25lIiwiYiI6Mn0",            // base64url for {"a":"one","b":2}
			want:      map[string]any{"a": "one", "b": 2.0}, // JSON numbers are float64
		},
		{
			name:      "invalid base64",
			pageToken: "invalid/base64",
			wantErr:   true,
		},
		{
			name:      "invalid json",
			pageToken: "YWJj", // base64 for "abc"
			wantErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeAfterCursorPageToken(tc.pageToken)
			if tc.wantErr {
				if err == nil {
					t.Errorf("DecodeAfterCursorPageToken(%q) got nil error, want error", tc.pageToken)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeAfterCursorPageToken(%q) returned an unexpected error: %v", tc.pageToken, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("DecodeAfterCursorPageToken(%q) diff (-want +got):\n%s", tc.pageToken, diff)
			}
		})
	}
}

func TestEncodeAfterCursorPageToken(t *testing.T) {
	testCases := []struct {
		name        string
		afterCursor map[string]any
		want        string
	}{
		{
			name:        "empty map",
			afterCursor: nil,
			want:        "",
		},
		{
			name:        "valid map",
			afterCursor: map[string]any{"a": "one", "b": 2},
			want:        "eyJhIjoib25lIiwiYiI6Mn0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EncodeAfterCursorPageToken(tc.afterCursor)
			if err != nil {
				t.Fatalf("EncodeAfterCursorPageToken(%v) returned an unexpected error: %v", tc.afterCursor, err)
			}
			if got != tc.want {
				t.Errorf("EncodeAfterCursorPageToken(%v) = %q, want %q", tc.afterCursor, got, tc.want)
			}
		})
	}
}

func TestApplyAfterCursorPagination(t *testing.T) {
	testCases := []struct {
		name           string
		results        []int
		pageSize       int32
		lastItemCursor map[string]any
		wantResults    []int
		wantToken      string
	}{
		{
			name:           "no truncation",
			results:        []int{1, 2, 3},
			pageSize:       5,
			lastItemCursor: map[string]any{"id": 3},
			wantResults:    []int{1, 2, 3},
			wantToken:      "",
		},
		{
			name:           "with truncation",
			results:        []int{1, 2, 3, 4, 5, 6},
			pageSize:       5,
			lastItemCursor: map[string]any{"id": 5},
			wantResults:    []int{1, 2, 3, 4, 5},
			wantToken:      "eyJpZCI6NX0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotResults, gotToken, err := ApplyAfterCursorPagination(tc.results, tc.pageSize, tc.lastItemCursor)
			if err != nil {
				t.Fatalf("ApplyAfterCursorPagination() returned an unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.wantResults, gotResults); diff != "" {
				t.Errorf("ApplyAfterCursorPagination() results diff (-want +got):\n%s", diff)
			}
			if gotToken != tc.wantToken {
				t.Errorf("ApplyAfterCursorPagination() token got %q, want %q", gotToken, tc.wantToken)
			}
		})
	}
}

func TestApplyPagination(t *testing.T) {
	extractCursor := func(id int) (map[string]any, error) {
		return map[string]any{"id": id}, nil
	}

	testCases := []struct {
		name               string
		results            []int
		pageSize           int32
		pageOffset         int64
		isCursorPagination bool
		wantResults        []int
		wantToken          string
	}{
		{
			name:               "offset pagination, no truncation",
			results:            []int{1, 2, 3},
			pageSize:           5,
			pageOffset:         10,
			isCursorPagination: false,
			wantResults:        []int{1, 2, 3},
			wantToken:          "",
		},
		{
			name:               "offset pagination, with truncation",
			results:            []int{1, 2, 3, 4, 5, 6},
			pageSize:           5,
			pageOffset:         10,
			isCursorPagination: false,
			wantResults:        []int{1, 2, 3, 4, 5},
			wantToken:          "15",
		},
		{
			name:               "cursor pagination, no truncation",
			results:            []int{1, 2, 3},
			pageSize:           5,
			isCursorPagination: true,
			wantResults:        []int{1, 2, 3},
			wantToken:          "",
		},
		{
			name:               "cursor pagination, with truncation",
			results:            []int{1, 2, 3, 4, 5, 6},
			pageSize:           5,
			isCursorPagination: true,
			wantResults:        []int{1, 2, 3, 4, 5},
			wantToken:          "eyJpZCI6NX0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotResults, gotToken, err := ApplyPagination(tc.results, tc.pageSize, tc.pageOffset, tc.isCursorPagination, extractCursor)
			if err != nil {
				t.Fatalf("ApplyPagination() returned an unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.wantResults, gotResults); diff != "" {
				t.Errorf("ApplyPagination() results diff (-want +got):\n%s", diff)
			}
			if gotToken != tc.wantToken {
				t.Errorf("ApplyPagination() token got %q, want %q", gotToken, tc.wantToken)
			}
		})
	}
}

func TestListParams_DecodePageToken(t *testing.T) {
	testCases := []struct {
		name            string
		pageToken       string
		defaultToCursor bool
		wantIsCursor    bool
		wantOffset      int64
		wantAfterCursor map[string]any
		wantErr         bool
	}{
		{
			name:            "empty token, default offset",
			pageToken:       "",
			defaultToCursor: false,
			wantIsCursor:    false,
			wantOffset:      0,
		},
		{
			name:            "empty token, default cursor",
			pageToken:       "",
			defaultToCursor: true,
			wantIsCursor:    true,
			wantAfterCursor: nil,
		},
		{
			name:            "offset token",
			pageToken:       "100",
			defaultToCursor: true, // Should still be offset
			wantIsCursor:    false,
			wantOffset:      100,
		},
		{
			name:            "cursor token",
			pageToken:       "eyJhIjoib25lIiwiYiI6Mn0",
			defaultToCursor: false, // Should still be cursor
			wantIsCursor:    true,
			wantAfterCursor: map[string]any{"a": "one", "b": 2.0},
		},
		{
			name:            "invalid offset token",
			pageToken:       "abc",
			defaultToCursor: false, // IsCursorPageToken will return true for "abc"
			wantIsCursor:    true,
			wantErr:         true, // DecodeAfterCursorPageToken will fail
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			params := &ListParams{}
			gotIsCursor, err := params.DecodePageToken(tc.pageToken, tc.defaultToCursor)

			if tc.wantErr {
				if err == nil {
					t.Errorf("DecodePageToken(%q) got nil error, want error", tc.pageToken)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodePageToken(%q) returned an unexpected error: %v", tc.pageToken, err)
			}
			if gotIsCursor != tc.wantIsCursor {
				t.Errorf("DecodePageToken(%q) returned isCursor %v, want %v", tc.pageToken, gotIsCursor, tc.wantIsCursor)
			}
			if params.PageOffset != tc.wantOffset {
				t.Errorf("DecodePageToken(%q) offset = %d, want %d", tc.pageToken, params.PageOffset, tc.wantOffset)
			}
			if diff := cmp.Diff(tc.wantAfterCursor, params.AfterCursor); diff != "" {
				t.Errorf("DecodePageToken(%q) afterCursor diff (-want +got):\n%s", tc.pageToken, diff)
			}
		})
	}
}
