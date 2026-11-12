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
	"xmanager_cloud/internal/storage"
)

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
