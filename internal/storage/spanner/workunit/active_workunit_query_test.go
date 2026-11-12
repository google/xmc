// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package workunit

import (
	"maps"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"xmanager_cloud/internal/storage"
)

func TestActiveWorkUnitStatementsFilters(t *testing.T) {
	tests := []struct {
		name            string
		filter          storage.ActiveWorkUnitFilter
		join, predicate string
		args            map[string]any
	}{
		{"default", storage.ActiveWorkUnitFilter{}, "", "", map[string]any{"queueNames": []string{"q"}}},
		{"empty author", storage.ActiveWorkUnitFilter{HasAuthor: true}, ` JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid`, ` AND COALESCE(e.Creator, '') = @author`, map[string]any{"queueNames": []string{"q"}, "author": ""}},
		{"status", storage.ActiveWorkUnitFilter{Status: "RUNNING"}, "", ` AND CAST(wu.ExecutionState AS STRING) = @status`, map[string]any{"queueNames": []string{"q"}, "status": "RUNNING"}},
		{"combined", storage.ActiveWorkUnitFilter{HasAuthor: true, Author: "Bob@example.com", Status: "RUNNING"}, ` JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid`, ` AND COALESCE(e.Creator, '') = @author AND CAST(wu.ExecutionState AS STRING) = @status`, map[string]any{"queueNames": []string{"q"}, "author": "Bob@example.com", "status": "RUNNING"}},
	}
	normalize := func(sql string) string { return strings.Join(strings.Fields(sql), " ") }
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, fetch, err := activeWorkUnitStatements([]string{"q"}, 11, 20, storage.ActiveWorkUnitQuery{Filter: tt.filter})
			if err != nil {
				t.Fatal(err)
			}
			wantCount := "SELECT COUNT(*) FROM WorkUnits wu" + tt.join + ` WHERE wu.QueueName IN UNNEST(@queueNames) AND wu.IsPendingOrRunningJob IS NOT NULL` + tt.predicate
			wantFetch := `SELECT wu.BitReversedXid, wu.WorkUnitId, COALESCE(e.Creator, '') AS Creator, COALESCE(e.Title, '') AS ExperimentTitle FROM WorkUnits wu JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid WHERE wu.QueueName IN UNNEST(@queueNames) AND wu.IsPendingOrRunningJob IS NOT NULL` + tt.predicate + " ORDER BY wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC, wu.WorkUnitId ASC, wu.Xid ASC" + " LIMIT @limit OFFSET @offset"
			if diff := cmp.Diff(normalize(wantCount), normalize(count.SQL)); diff != "" {
				t.Errorf("count SQL (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(normalize(wantFetch), normalize(fetch.SQL)); diff != "" {
				t.Errorf("fetch SQL (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.args, count.Params); diff != "" {
				t.Errorf("count bindings (-want +got):\n%s", diff)
			}
			wantFetchArgs := maps.Clone(tt.args)
			wantFetchArgs["limit"], wantFetchArgs["offset"] = int64(11), int64(20)
			if diff := cmp.Diff(wantFetchArgs, fetch.Params); diff != "" {
				t.Errorf("fetch bindings (-want +got):\n%s", diff)
			}
		})
	}
}

func TestActiveWorkUnitOrder(t *testing.T) {
	tests := []struct{ order, want string }{
		{"", "wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC"},
		{"author asc", `COALESCE(e.Creator, '') ASC`},
		{"author desc", `COALESCE(e.Creator, '') DESC`},
		{"priority asc", "wu.Priority ASC"},
		{"priority desc", "wu.Priority DESC"},
		{"status asc", `CASE wu.ExecutionState WHEN 'RUNNING' THEN 0 WHEN 'STARTING' THEN 1 WHEN 'INITIALIZING' THEN 2 ELSE 3 END ASC`},
		{"status desc", `CASE wu.ExecutionState WHEN 'RUNNING' THEN 0 WHEN 'STARTING' THEN 1 WHEN 'INITIALIZING' THEN 2 ELSE 3 END DESC`},
	}
	for _, tt := range tests {
		t.Run(tt.order, func(t *testing.T) {
			got, err := activeWorkUnitOrder(tt.order)
			if err != nil {
				t.Fatal(err)
			}
			want := " ORDER BY " + tt.want + ", wu.WorkUnitId ASC, wu.Xid ASC"
			if diff := cmp.Diff(want, got.String()); diff != "" {
				t.Errorf("ordering (-want +got):\n%s", diff)
			}
		})
	}
	for _, order := range []string{"author", "author sideways", "priority desc; DROP TABLE WorkUnits"} {
		if _, _, err := activeWorkUnitStatements([]string{"q"}, 10, 0, storage.ActiveWorkUnitQuery{OrderBy: order}); err == nil {
			t.Errorf("accepted invalid order %q", order)
		}
	}
}
