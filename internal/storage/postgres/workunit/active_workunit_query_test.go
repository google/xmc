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
	"testing"

	"github.com/google/go-cmp/cmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
)

func TestActiveWorkUnitOrder(t *testing.T) {
	tests := []struct {
		name  string
		query storage.ActiveWorkUnitQuery
		want  string
	}{
		{"default", storage.ActiveWorkUnitQuery{}, "wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC"},
		{"author asc", storage.ActiveWorkUnitQuery{SortField: storage.ActiveWorkUnitSortAuthor}, `COALESCE(e.Creator, '') COLLATE "C" ASC`},
		{"author desc", storage.ActiveWorkUnitQuery{SortField: storage.ActiveWorkUnitSortAuthor, Descending: true}, `COALESCE(e.Creator, '') COLLATE "C" DESC`},
		{"priority asc", storage.ActiveWorkUnitQuery{SortField: storage.ActiveWorkUnitSortPriority}, "wu.Priority ASC"},
		{"priority desc", storage.ActiveWorkUnitQuery{SortField: storage.ActiveWorkUnitSortPriority, Descending: true}, "wu.Priority DESC"},
		{"status asc", storage.ActiveWorkUnitQuery{SortField: storage.ActiveWorkUnitSortStatus}, `CASE wu.ExecutionState WHEN 4 THEN 0 WHEN 3 THEN 1 WHEN 1 THEN 2 ELSE 3 END ASC`},
		{"status desc", storage.ActiveWorkUnitQuery{SortField: storage.ActiveWorkUnitSortStatus, Descending: true}, `CASE wu.ExecutionState WHEN 4 THEN 0 WHEN 3 THEN 1 WHEN 1 THEN 2 ELSE 3 END DESC`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := activeWorkUnitOrder(tt.query)
			want := " ORDER BY " + tt.want + ", wu.WorkUnitId ASC, wu.Xid ASC"
			if diff := cmp.Diff(want, got.String()); diff != "" {
				t.Errorf("ordering (-want +got):\n%s", diff)
			}
		})
	}
}

func TestActiveWorkUnitPredicates(t *testing.T) {
	tests := []struct {
		name     string
		filter   storage.ActiveWorkUnitFilter
		wantSQL  string
		wantArgs []any
	}{
		{"empty", storage.ActiveWorkUnitFilter{}, "", nil},
		{"author substring", storage.ActiveWorkUnitFilter{AuthorSubstring: "alice", HasAuthor: true}, " AND STRPOS(LOWER(COALESCE(e.Creator, '')), LOWER($4)) > 0", []any{"alice"}},
		{"status", storage.ActiveWorkUnitFilter{Status: essmsgpb.WorkUnitStatus_RUNNING}, " AND wu.ExecutionState = $4", []any{int32(4)}},
		{"author and status", storage.ActiveWorkUnitFilter{AuthorSubstring: "alice", HasAuthor: true, Status: essmsgpb.WorkUnitStatus_SCHEDULING}, " AND STRPOS(LOWER(COALESCE(e.Creator, '')), LOWER($4)) > 0 AND wu.ExecutionState = $5", []any{"alice", int32(2)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSQL, gotArgs := activeWorkUnitPredicates(tt.filter, 4)
			if diff := cmp.Diff(tt.wantSQL, gotSQL.String()); diff != "" {
				t.Errorf("SQL (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantArgs, gotArgs); diff != "" {
				t.Errorf("args (-want +got):\n%s", diff)
			}
		})
	}
}
