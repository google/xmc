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

package xidschema

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	postgresdialect "xmanager_cloud/internal/query_generator/dialect/postgres"
	"xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	"xmanager_cloud/internal/query_generator/querytools"
)

func TestXidsTableView(t *testing.T) {
	ctx := context.Background()
	qg, err := querytools.NewQueryGenerator(XidTables, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewQueryGenerator failed: %v", err)
	}

	tests := []struct {
		name           string
		filter         string
		orderBy        string
		afterCursor    map[string]any
		wantSubstrings []string
		wantArgs       []any
	}{
		{
			name:    "default select and order by xid asc",
			filter:  "",
			orderBy: "",
			wantSubstrings: []string{
				"SELECT x.Xid, x.ParentXid, x.Creator, x.OriginatingSystem, x.Endpoint, x.Acls, x.CreateTime, x.UpdateTime",
				"FROM Xids x",
				"ORDER BY x.Xid LIMIT 11",
			},
			wantArgs: nil,
		},
		{
			name:    "filter by creator and endpoint with cursor pagination",
			filter:  `creator = "alice" AND endpoint = "gke"`,
			orderBy: "xid desc",
			afterCursor: map[string]any{
				"xid": int64(250),
			},
			wantSubstrings: []string{
				"(x.Creator = $1)",
				"(x.Endpoint = $2)",
				"(x.Xid < $3)",
				"ORDER BY x.Xid DESC LIMIT 11",
			},
			wantArgs: []any{"alice", "gke", int64(250)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			normalizedFilter, err := normalizer.Normalize(tc.filter, qg.Tables()[0])
			if err != nil {
				t.Fatalf("Normalize(%q) failed: %v", tc.filter, err)
			}

			stmt, err := liststatement.ListStatement(ctx, postgresdialect.NewStatement(""), liststatement.ListParams{
				PageSize:              10,
				Filter:                normalizedFilter,
				OrderBy:               tc.orderBy,
				QueryGen:              qg,
				DefaultSelectFields:   XidsSelectFields,
				DefaultFullTextFields: XidsFullTextFields,
				ValidOrderByFields:    XidsOrderByFields,
				AfterCursor:           tc.afterCursor,
			})
			if err != nil {
				t.Fatalf("ListStatement failed: %v", err)
			}

			if strings.Contains(stmt.SQL, "bit_reversed") || strings.Contains(stmt.SQL, "BitReversed") {
				t.Errorf("unexpected bit_reversed reference in SQL: %s", stmt.SQL)
			}
			for _, sub := range tc.wantSubstrings {
				if !strings.Contains(stmt.SQL, sub) {
					t.Errorf("SQL missing substring %q\ngot: %s", sub, stmt.SQL)
				}
			}
			if diff := cmp.Diff(tc.wantArgs, stmt.Args); diff != "" {
				t.Errorf("Args mismatch (-want +got):\n%s\nSQL: %s", diff, stmt.SQL)
			}
		})
	}
}
