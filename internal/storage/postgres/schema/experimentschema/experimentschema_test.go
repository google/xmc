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

package experimentschema

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	postgresdialect "xmanager_cloud/internal/query_generator/dialect/postgres"
	"xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	"xmanager_cloud/internal/query_generator/querytools"
)

func TestExperimentsTableView(t *testing.T) {
	ctx := context.Background()
	qg, err := querytools.NewOptimizableGenerator(
		ExperimentTables,
		nil,
		nil,
		nil,
		true,
		false,
	)
	if err != nil {
		t.Fatalf("NewOptimizableGenerator failed: %v", err)
	}

	tests := []struct {
		name           string
		filter         string
		orderBy        string
		lastRowValues  map[string]any
		wantSubstrings []string
		wantArgs       []any
	}{
		{
			name:    "default select and order by xid asc",
			filter:  "",
			orderBy: "",
			wantSubstrings: []string{
				"SELECT e.Xid, e.Creator, e.Priority, e.Title, e.Urls, e.Tags, e.Notes, e.Resources, e.Status, e.ActiveState, e.LaunchState, e.CreateTime, e.UpdateTime, e.CompleteTime",
				"FROM Experiments e",
				"ORDER BY e.Xid LIMIT 11",
			},
			wantArgs: nil,
		},
		{
			name:    "array contains on tags",
			filter:  `tags:"gpu" AND tags:"tpu"`,
			orderBy: "",
			wantSubstrings: []string{
				"($1 = ANY(e.Tags))",
				"($2 = ANY(e.Tags))",
			},
			wantArgs: []any{"gpu", "tpu"},
		},
		{
			name:    "creator disjunction optimization and full text search",
			filter:  `(creator = "alice" OR creator = "bob") AND "llm"`,
			orderBy: "",
			wantSubstrings: []string{
				"(e.Creator = ANY($1))",
				"Title_Tokens @@ plainto_tsquery('simple', $2)",
				"Tags_Tokens @@ plainto_tsquery('simple', $3)",
				"Creator_Tokens @@ plainto_tsquery('simple', $4)",
			},
			wantArgs: []any{[]string{"alice", "bob"}, "llm", "llm", "llm"},
		},
		{
			name:    "nullable completed order by with cursor pagination",
			filter:  "",
			orderBy: "completed desc",
			lastRowValues: map[string]any{
				"completed": time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
				"xid":       int64(42),
			},
			wantSubstrings: []string{
				"(e.CompleteTime = $1 AND e.Xid > $2)",
				"(e.CompleteTime < $3 OR e.CompleteTime IS NULL)",
				"ORDER BY e.CompleteTime DESC NULLS LAST, e.Xid LIMIT 11",
			},
			wantArgs: []any{
				time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
				int64(42),
				time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			normalizedFilter, err := normalizer.Normalize(tc.filter, qg.Tables()[0])
			if err != nil {
				t.Fatalf("Normalize(%q) failed: %v", tc.filter, err)
			}

			stmt := postgresdialect.NewStatement("")
			err = liststatement.ListStatement(ctx, stmt, liststatement.ListParams{
				PageSize:              10,
				Filter:                normalizedFilter,
				OrderBy:               tc.orderBy,
				QueryGen:              qg,
				DefaultSelectFields:   ExperimentsSelectFields,
				DefaultFullTextFields: ExperimentsFullTextFields,
				ValidOrderByFields:    ExperimentsOrderByFields,
				AfterCursor:           tc.lastRowValues,
			})
			if err != nil {
				t.Fatalf("ListStatement failed: %v", err)
			}

			if strings.Contains(stmt.SQL, "bit_reversed_xid") || strings.Contains(stmt.SQL, "BitReversedXid") {
				t.Errorf("unexpected bit_reversed_xid reference in SQL: %s", stmt.SQL)
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
