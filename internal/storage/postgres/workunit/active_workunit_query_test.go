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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"xmanager_cloud/internal/storage"
)

func TestActiveWorkUnitStatementsFilters(t *testing.T) {
	tests := []struct {
		name                        string
		filter                      storage.ActiveWorkUnitFilter
		join, predicate, pagination string
		args                        []any
	}{
		{"default", storage.ActiveWorkUnitFilter{}, "", "", " LIMIT $2 OFFSET $3", []any{[]string{"q"}}},
		{"empty author", storage.ActiveWorkUnitFilter{HasAuthor: true}, ` JOIN Experiments e ON wu.Xid = e.Xid`, ` AND COALESCE(e.Creator, '') = $2`, " LIMIT $3 OFFSET $4", []any{[]string{"q"}, ""}},
		{"status", storage.ActiveWorkUnitFilter{Status: "RUNNING"}, "", ` AND wu.ExecutionState = $2`, " LIMIT $3 OFFSET $4", []any{[]string{"q"}, int32(4)}},
		{"combined", storage.ActiveWorkUnitFilter{HasAuthor: true, Author: "Bob@example.com", Status: "RUNNING"}, ` JOIN Experiments e ON wu.Xid = e.Xid`, ` AND COALESCE(e.Creator, '') = $2 AND wu.ExecutionState = $3`, " LIMIT $4 OFFSET $5", []any{[]string{"q"}, "Bob@example.com", int32(4)}},
	}
	normalize := func(sql string) string { return strings.Join(strings.Fields(sql), " ") }
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, fetch, err := activeWorkUnitStatements([]string{"q"}, 11, 20, storage.ActiveWorkUnitQuery{Filter: tt.filter})
			if err != nil {
				t.Fatal(err)
			}
			wantCount := "SELECT COUNT(*) FROM WorkUnits wu" + tt.join + ` WHERE wu.QueueName = ANY($1) AND wu.IsPendingOrRunningJob IS NOT NULL` + tt.predicate
			wantFetch := `SELECT wu.Xid, wu.WorkUnitId, wu.WorkUnitType, wu.Priority, wu.Urls, wu.Tags, wu.Notes, wu.Executable, wu.ExecutionCreateTime, wu.ExecutionUpdateTime, wu.ExecutionCompleteTime, wu.ExecutionId, wu.ExecutionMetadata, wu.WorkUnitStatus, wu.CreateTime, wu.UpdateTime, COALESCE(e.Creator, '') AS Creator, COALESCE(e.Title, '') AS experiment_title FROM WorkUnits wu JOIN Experiments e ON wu.Xid = e.Xid WHERE wu.QueueName = ANY($1) AND wu.IsPendingOrRunningJob IS NOT NULL` + tt.predicate + " ORDER BY wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC, wu.WorkUnitId ASC, wu.Xid ASC" + tt.pagination
			if diff := cmp.Diff(normalize(wantCount), normalize(count.sql.String())); diff != "" {
				t.Errorf("count SQL (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(normalize(wantFetch), normalize(fetch.sql.String())); diff != "" {
				t.Errorf("fetch SQL (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.args, count.args); diff != "" {
				t.Errorf("count bindings (-want +got):\n%s", diff)
			}
			wantFetchArgs := append(append([]any{}, tt.args...), int64(11), int64(20))
			if diff := cmp.Diff(wantFetchArgs, fetch.args); diff != "" {
				t.Errorf("fetch bindings (-want +got):\n%s", diff)
			}
		})
	}
}

func TestActiveWorkUnitOrder(t *testing.T) {
	tests := []struct{ order, want string }{
		{"", "wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC"},
		{"author asc", `COALESCE(e.Creator, '') COLLATE "C" ASC`},
		{"author desc", `COALESCE(e.Creator, '') COLLATE "C" DESC`},
		{"priority asc", "wu.Priority ASC"},
		{"priority desc", "wu.Priority DESC"},
		{"status asc", `CASE wu.ExecutionState WHEN 4 THEN 0 WHEN 3 THEN 1 WHEN 1 THEN 2 ELSE 3 END ASC`},
		{"status desc", `CASE wu.ExecutionState WHEN 4 THEN 0 WHEN 3 THEN 1 WHEN 1 THEN 2 ELSE 3 END DESC`},
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
