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

package storage

import "testing"

func TestActiveWorkUnitOrderBy(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		invalid bool
	}{
		{"", "", false}, {"  ", "", false},
		{"author", "author asc", false}, {"status DESC", "status desc", false},
		{" priority asc ", "priority asc", false},
		{"other", "", true}, {"author sideways", "", true},
		{"author asc, priority desc", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ActiveWorkUnitOrderBy(tt.input)
			if (err != nil) != tt.invalid || got != tt.want {
				t.Errorf("ActiveWorkUnitOrderBy(%q) = (%q, %v), want (%q, invalid=%v)", tt.input, got, err, tt.want, tt.invalid)
			}
		})
	}
}
