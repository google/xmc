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

func TestParseActiveWorkUnitFilter(t *testing.T) {
	tests := []struct {
		input   string
		want    ActiveWorkUnitFilter
		invalid bool
	}{
		{"", ActiveWorkUnitFilter{}, false},
		{`author = "alice@example.com"`, ActiveWorkUnitFilter{Author: "alice@example.com", HasAuthor: true}, false},
		{`author = ""`, ActiveWorkUnitFilter{HasAuthor: true}, false},
		{`status = RUNNING AND author = "alice@example.com"`, ActiveWorkUnitFilter{Author: "alice@example.com", HasAuthor: true, Status: "RUNNING"}, false},
		{`status = STARTING`, ActiveWorkUnitFilter{Status: "STARTING"}, false},
		{`status = INITIALIZING`, ActiveWorkUnitFilter{Status: "INITIALIZING"}, false},
		{`status = SCHEDULING`, ActiveWorkUnitFilter{Status: "SCHEDULING"}, false},
		{`author = "alice" OR status = RUNNING`, ActiveWorkUnitFilter{}, true},
		{`status = COMPLETED`, ActiveWorkUnitFilter{}, true},
		{`status = RUNNING AND status = STARTING`, ActiveWorkUnitFilter{}, true},
		{`author = "alice" AND author = "bob"`, ActiveWorkUnitFilter{}, true},
		{`author : "alice"`, ActiveWorkUnitFilter{}, true},
		{`priority = 1`, ActiveWorkUnitFilter{}, true},
		{`author = now()`, ActiveWorkUnitFilter{}, true},
		{`author = alice`, ActiveWorkUnitFilter{}, true},
		{`author = "`, ActiveWorkUnitFilter{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseActiveWorkUnitFilter(tt.input)
			if (err != nil) != tt.invalid {
				t.Fatalf("error = %v, invalid=%v", err, tt.invalid)
			}
			if !tt.invalid && got != tt.want {
				t.Errorf("filter = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseActiveWorkUnitQuery(t *testing.T) {
	for _, invalid := range [][2]string{{"invalid", ""}, {"", "status = COMPLETED"}} {
		if _, err := ParseActiveWorkUnitQuery(invalid[0], invalid[1]); err == nil {
			t.Errorf("accepted invalid query %q", invalid)
		}
	}
	if got, err := ParseActiveWorkUnitQuery("", ""); err != nil || got != (ActiveWorkUnitQuery{}) {
		t.Fatalf("empty query = %+v, %v", got, err)
	}
	got, err := ParseActiveWorkUnitQuery("author", "status = RUNNING")
	want := ActiveWorkUnitQuery{OrderBy: "author asc", Filter: ActiveWorkUnitFilter{Status: "RUNNING"}}
	if err != nil || got != want {
		t.Fatalf("query = %+v, %v; want %+v", got, err, want)
	}
}
