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

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"googlesql/safesql/go/safesql"
	"xmanager_cloud/query_generator/querytools"
)

func TestListStatement(t *testing.T) {
	testCases := []struct {
		name     string
		params   ListParams
		tables   []querytools.TableView
		wantStmt *spanner.Statement
		wantErr  bool
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
			stmt, err := ListStatement(ctx, tc.params)

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

func TestApplyPagination(t *testing.T) {
	extractCursor := func(id int) (map[string]any, error) {
		return map[string]any{"id": id}, nil
	}

	testCases := []struct {
		name        string
		results     []int
		pageSize    int32
		wantResults []int
		wantToken   string
	}{
		{
			name:        "no truncation",
			results:     []int{1, 2, 3},
			pageSize:    5,
			wantResults: []int{1, 2, 3},
			wantToken:   "",
		},
		{
			name:        "with truncation",
			results:     []int{1, 2, 3, 4, 5, 6},
			pageSize:    5,
			wantResults: []int{1, 2, 3, 4, 5},
			wantToken:   "eyJpZCI6NX0",
		},
		{
			name:        "zero page size",
			results:     []int{1, 2},
			pageSize:    0,
			wantResults: nil,
			wantToken:   "",
		},
		{
			name:        "negative page size",
			results:     []int{1, 2},
			pageSize:    -1,
			wantResults: nil,
			wantToken:   "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotResults, gotToken, err := ApplyPagination(tc.results, tc.pageSize, extractCursor)
			if tc.pageSize <= 0 {
				if err == nil {
					t.Errorf("ApplyPagination() with pageSize %d got nil error, want error", tc.pageSize)
				}
				return
			}
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
		wantAfterCursor map[string]any
		wantErr         bool
	}{
		{
			name:            "empty token, default cursor",
			pageToken:       "",
			wantAfterCursor: nil,
		},
		{
			name:            "cursor token preserves int64",
			pageToken:       "eyJhIjoib25lIiwiYiI6Mn0",
			wantAfterCursor: map[string]any{"a": "one", "b": int64(2)},
		},
		{
			name:      "empty JSON map cursor token is rejected",
			pageToken: "e30",
			wantErr:   true,
		},
		{
			name:      "invalid base64 token",
			pageToken: "invalid/base64",
			wantErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			params := &ListParams{}
			err := params.DecodePageToken(tc.pageToken)

			if tc.wantErr {
				if err == nil {
					t.Errorf("DecodePageToken(%q) got nil error, want error", tc.pageToken)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodePageToken(%q) returned an unexpected error: %v", tc.pageToken, err)
			}
			if diff := cmp.Diff(tc.wantAfterCursor, params.AfterCursor); diff != "" {
				t.Errorf("DecodePageToken(%q) afterCursor diff (-want +got):\n%s", tc.pageToken, diff)
			}
		})
	}
}
