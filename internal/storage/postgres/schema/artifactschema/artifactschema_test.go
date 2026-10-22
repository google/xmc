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

package artifactschema

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

func TestArtifactsTableView(t *testing.T) {
	ctx := context.Background()
	qg, err := querytools.NewQueryGenerator(
		ArtifactTables,
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
			name:    "default select and order by xid, work_unit_id, artifact_id",
			filter:  "xid = 10",
			orderBy: "",
			wantSubstrings: []string{
				"SELECT a.Xid, a.WorkUnitId, a.ArtifactId, a.Url, a.Icon, a.DisplayName, a.Priority, a.Tags, a.Notes, a.LifecyclePhase, a.DataType, a.MimeType, a.AdditionalInfo, a.Payload, a.CreateTime, a.UpdateTime",
				"FROM Artifacts a",
				"WHERE (a.Xid = $1)",
				"ORDER BY a.Xid, a.WorkUnitId ASC NULLS FIRST, a.ArtifactId LIMIT 11",
			},
			wantArgs: []any{int64(10)},
		},
		{
			name:    "tags array contains, payload_type column filter, and full text search",
			filter:  `xid = 10 AND tags:"checkpoint" AND payload_type = "managed_xprof" AND "profile"`,
			orderBy: "",
			wantSubstrings: []string{
				"(a.Xid = $1)",
				"($2 = ANY(a.Tags))",
				"(a.PayloadType = $3)",
				"Url_Tokens @@ plainto_tsquery('simple', $4)",
				"SearchableContent_Tokens @@ plainto_tsquery('simple', $9)",
			},
			wantArgs: []any{
				int64(10),
				"checkpoint",
				"managed_xprof",
				"profile", "profile", "profile", "profile", "profile", "profile",
			},
		},
		{
			name:    "nullable work_unit_id in cursor pagination",
			filter:  "xid = 10",
			orderBy: "priority",
			afterCursor: map[string]any{
				"priority":     int64(5),
				"xid":          int64(10),
				"work_unit_id": nil,
				"artifact_id":  int64(99),
			},
			wantSubstrings: []string{
				"(a.Xid = $1)",
				"(a.Priority = $2 AND a.WorkUnitId IS NULL AND a.ArtifactId > $3)",
				"(a.Priority = $4 AND a.WorkUnitId IS NOT NULL)",
				"(a.Priority > $5)",
				"ORDER BY a.Priority, a.Xid, a.WorkUnitId ASC NULLS FIRST, a.ArtifactId LIMIT 11",
			},
			wantArgs: []any{int64(10), int64(5), int64(99), int64(5), int64(5)},
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
				DefaultSelectFields:   ArtifactsSelectFields,
				DefaultFullTextFields: ArtifactsFullTextFields,
				ValidOrderByFields:    ArtifactsOrderByFields,
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
