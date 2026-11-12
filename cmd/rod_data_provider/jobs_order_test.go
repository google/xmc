// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"testing"

	"xmanager_cloud/internal/storage"
)

func TestParseJobsOrderBy(t *testing.T) {
	tests := []struct {
		input   string
		want    storage.ActiveWorkUnitQuery
		invalid bool
	}{
		{"", storage.ActiveWorkUnitQuery{}, false},
		{"  ", storage.ActiveWorkUnitQuery{}, false},
		{"author", storage.ActiveWorkUnitQuery{SortField: storage.ActiveWorkUnitSortAuthor}, false},
		{"status DESC", storage.ActiveWorkUnitQuery{SortField: storage.ActiveWorkUnitSortStatus, Descending: true}, false},
		{" priority asc ", storage.ActiveWorkUnitQuery{SortField: storage.ActiveWorkUnitSortPriority}, false},
		{"Author asc", storage.ActiveWorkUnitQuery{SortField: storage.ActiveWorkUnitSortAuthor}, false},
		{"STATUS desc", storage.ActiveWorkUnitQuery{SortField: storage.ActiveWorkUnitSortStatus, Descending: true}, false},
		{"other", storage.ActiveWorkUnitQuery{}, true},
		{"author sideways", storage.ActiveWorkUnitQuery{}, true},
		{"author asc, priority desc", storage.ActiveWorkUnitQuery{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseJobsOrderBy(tt.input)
			if (err != nil) != tt.invalid || got != tt.want {
				t.Errorf("parseJobsOrderBy(%q) = (%+v, %v), want (%+v, invalid=%v)", tt.input, got, err, tt.want, tt.invalid)
			}
		})
	}
}
