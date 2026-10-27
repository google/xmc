// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pgdb

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
)

func newTestListParams(t *testing.T) liststatement.ListParams {
	t.Helper()
	idField := querytools.TableField("id")
	nameField := querytools.TableField("name")
	tables := []querytools.TableView{{
		Name:       "Items",
		NameClause: safesql.New("Items i"),
		Cost:       10,
		Fields: map[querytools.TableField]querytools.Member{
			idField:   {Name: idField, QueryLiteral: safesql.New("i.Id"), Type: filtervisitor.Integer, Selectable: true, Indexed: true},
			nameField: {Name: nameField, QueryLiteral: safesql.New("i.Name"), Type: filtervisitor.String, Selectable: true},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{{TableField: idField, SortOrder: querytools.Asc}},
	}}
	qgen, err := querytools.NewQueryGenerator(tables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		t.Fatalf("NewQueryGenerator() failed: %v", err)
	}
	return liststatement.ListParams{
		PageSize:            10,
		QueryGen:            qgen,
		DefaultSelectFields: []querytools.TableField{idField, nameField},
		ValidOrderByFields:  []querytools.UserOrderedIndex{{TableField: idField}, {TableField: nameField}},
	}
}

func TestList_ValidationAndQueryErrors(t *testing.T) {
	type itemRow struct {
		ID int64 `db:"id"`
	}
	ctx := t.Context()
	identity := func(r itemRow) (int64, error) { return r.ID, nil }
	cursor := func(id int64) (map[string]any, error) { return map[string]any{"id": id}, nil }

	tests := []struct {
		name      string
		pageToken string
		filter    string
		orderBy   string
		wantCode  codes.Code
	}{
		{name: "invalid filter syntax", filter: "id = = 1", wantCode: codes.InvalidArgument},
		{name: "unclosed quote in filter", filter: `name = "unclosed`, wantCode: codes.InvalidArgument},
		{name: "invalid page token", pageToken: "not-valid-base64!!!", wantCode: codes.InvalidArgument},
		{name: "invalid orderBy field", orderBy: "unknown_field", wantCode: codes.InvalidArgument},
		{name: "nil DBTX", wantCode: codes.FailedPrecondition},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			params := newTestListParams(t)
			params.Filter = tc.filter
			params.OrderBy = tc.orderBy
			_, _, err := List(ctx, nil, "item", tc.pageToken, params, identity, cursor)
			if got := status.Code(err); got != tc.wantCode {
				t.Errorf("status.Code(err) = %v, want %v (err: %v)", got, tc.wantCode, err)
			}
		})
	}
}

func TestMapRowsToProtos(t *testing.T) {
	got, err := mapRowsToProtos([]int{1, 2, 3}, func(v int) (string, error) {
		return fmt.Sprintf("item-%d", v), nil
	})
	if err != nil {
		t.Fatalf("mapRowsToProtos() unexpected error: %v", err)
	}
	if diff := cmp.Diff([]string{"item-1", "item-2", "item-3"}, got); diff != "" {
		t.Errorf("mapRowsToProtos() mismatch (-want +got):\n%s", diff)
	}

	_, err = mapRowsToProtos([]int{1, 2}, func(v int) (string, error) {
		if v == 2 {
			return "", grpcerr.ErrInternal
		}
		return "ok", nil
	})
	if !errors.Is(err, grpcerr.ErrInternal) {
		t.Errorf("mapRowsToProtos(error) = %v, want %v", err, grpcerr.ErrInternal)
	}
}
