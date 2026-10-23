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

package statusmessageschema

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

	pb "xmanager_cloud/api/protos/experiment"
)

func TestStatusMessagesTableView(t *testing.T) {
	ctx := context.Background()
	qg, err := querytools.NewQueryGenerator(
		StatusMessageTables,
		nil,
		nil,
		nil,
	)
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
			name:    "default select and ordered index (xid, create_time desc, work_unit_id, message_id)",
			filter:  "xid = 55",
			orderBy: "",
			wantSubstrings: []string{
				"SELECT s.Xid, s.WorkUnitId, s.MessageId, s.Severity, s.Source, s.UserFacingText, s.UserFacingUrl, s.ExecutionId, s.CreateTime, s.AuxiliaryInfo",
				"FROM StatusMessages s",
				"WHERE (s.Xid = $1)",
				"ORDER BY s.Xid, s.CreateTime DESC, s.WorkUnitId ASC NULLS FIRST, s.MessageId LIMIT 11",
			},
			wantArgs: []any{int64(55)},
		},
		{
			name:    "nullable work_unit_id in cursor pagination",
			filter:  "xid = 55",
			orderBy: "",
			afterCursor: map[string]any{
				"xid":          int64(55),
				"create_time":  time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC),
				"work_unit_id": nil,
				"message_id":   int64(123),
			},
			wantSubstrings: []string{
				"(s.Xid = $1)",
				"(s.CreateTime = $2 AND s.WorkUnitId IS NULL AND s.MessageId > $3)",
				"(s.CreateTime = $4 AND s.WorkUnitId IS NOT NULL)",
				"(s.CreateTime < $5)",
			},
			wantArgs: []any{
				int64(55),
				time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC),
				int64(123),
				time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC),
				time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC),
			},
		},
		{
			name:    "symbolic enum filter on severity and source",
			filter:  `xid = 55 AND severity = ERROR AND source = "scheduler"`,
			orderBy: "",
			wantSubstrings: []string{
				"(s.Xid = $1)",
				"(s.Severity = $2)",
				"(s.Source = $3)",
			},
			wantArgs: []any{
				int64(55),
				int64(pb.StructuredMessage_ERROR),
				int64(pb.StructuredMessage_SCHEDULER),
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
				PageSize:            10,
				Filter:              normalizedFilter,
				OrderBy:             tc.orderBy,
				QueryGen:            qg,
				DefaultSelectFields: StatusMessagesSelectFields,
				ValidOrderByFields:  StatusMessagesOrderByFields,
				AfterCursor:         tc.afterCursor,
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
