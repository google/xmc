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
	statusArgs := []any{
		int32(essmsgpb.WorkUnitStatus_RUNNING),
		int32(essmsgpb.WorkUnitStatus_STARTING),
		int32(essmsgpb.WorkUnitStatus_INITIALIZING),
	}
	tests := []struct {
		order, want string
		args        []any
	}{
		{"", "wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC", nil},
		{"author asc", `COALESCE(e.Creator, '') COLLATE "C" ASC`, nil},
		{"author desc", `COALESCE(e.Creator, '') COLLATE "C" DESC`, nil},
		{"priority asc", "wu.Priority ASC", nil},
		{"priority desc", "wu.Priority DESC", nil},
		{"status asc", `CASE wu.ExecutionState WHEN $4 THEN 0 WHEN $5 THEN 1 WHEN $6 THEN 2 ELSE 3 END ASC`, statusArgs},
		{"status desc", `CASE wu.ExecutionState WHEN $4 THEN 0 WHEN $5 THEN 1 WHEN $6 THEN 2 ELSE 3 END DESC`, statusArgs},
	}
	for _, tt := range tests {
		t.Run(tt.order, func(t *testing.T) {
			got, gotArgs, err := activeWorkUnitOrder(tt.order, 3)
			if err != nil {
				t.Fatal(err)
			}
			want := " ORDER BY " + tt.want + ", wu.WorkUnitId ASC, wu.Xid ASC"
			if diff := cmp.Diff(want, got.String()); diff != "" {
				t.Errorf("ordering (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.args, gotArgs); diff != "" {
				t.Errorf("order args (-want +got):\n%s", diff)
			}
		})
	}
	for _, order := range []string{"author", "author sideways", "priority desc; DROP TABLE WorkUnits"} {
		if _, _, err := activeWorkUnitStatements([]string{"q"}, 10, 0, storage.ActiveWorkUnitQuery{OrderBy: order}); err == nil {
			t.Errorf("accepted invalid order %q", order)
		}
	}
}
