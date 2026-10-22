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

package workunitschema

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

func TestWorkUnitsTableView(t *testing.T) {
	ctx := context.Background()
	qg, err := querytools.NewQueryGenerator(
		WorkUnitTables,
		nil,
		nil,
		nil,
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
			name:    "default select and composite order by xid asc, work_unit_id asc",
			filter:  "xid = 100",
			orderBy: "",
			wantSubstrings: []string{
				"SELECT wu.Xid, wu.WorkUnitId, wu.WorkUnitType, wu.Priority, wu.Urls, wu.Tags, wu.Notes, wu.Executable, wu.ExecutionCreateTime, wu.ExecutionUpdateTime, wu.ExecutionCompleteTime, wu.ExecutionId, wu.ExecutionMetadata, wu.WorkUnitStatus, wu.QueueName, wu.ExecutionState, wu.CreateTime, wu.UpdateTime",
				"FROM WorkUnits wu",
				"WHERE (wu.Xid = $1)",
				"ORDER BY wu.Xid, wu.WorkUnitId LIMIT 11",
			},
			wantArgs: []any{int64(100)},
		},
		{
			name:    "array contains on tags and equality on notes",
			filter:  `xid = 100 AND tags:"eval" AND notes = "checkpoint"`,
			orderBy: "",
			wantSubstrings: []string{
				"(wu.Xid = $1)",
				"($2 = ANY(wu.Tags))",
				"(wu.Notes = $3)",
			},
			wantArgs: []any{int64(100), "eval", "checkpoint"},
		},
		{
			name:    "nullable execution_completed order by with cursor pagination",
			filter:  "xid = 100",
			orderBy: "execution_completed desc",
			lastRowValues: map[string]any{
				"execution_completed": time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC),
				"xid":                 int64(100),
				"work_unit_id":        int64(7),
			},
			wantSubstrings: []string{
				"(wu.Xid = $1)",
				"(wu.ExecutionCompleteTime = $2 AND wu.WorkUnitId > $3)",
				"(wu.ExecutionCompleteTime < $4 OR wu.ExecutionCompleteTime IS NULL)",
				"ORDER BY wu.ExecutionCompleteTime DESC NULLS LAST, wu.Xid, wu.WorkUnitId LIMIT 11",
			},
			wantArgs: []any{
				int64(100),
				time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC),
				int64(7),
				time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC),
			},
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
				DefaultSelectFields:   WorkUnitSelectFields,
				DefaultFullTextFields: WorkUnitFullTextFields,
				ValidOrderByFields:    WorkUnitOrderByFields,
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
