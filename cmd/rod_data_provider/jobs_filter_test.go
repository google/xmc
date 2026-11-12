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

package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
)

func TestParseJobsFilter(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    storage.ActiveWorkUnitFilter
		wantErr bool
	}{
		{name: "empty", input: ""},
		{name: "whitespace", input: "   "},
		{name: "author equals substring", input: `author = "alice"`, want: storage.ActiveWorkUnitFilter{AuthorSubstring: "alice", HasAuthor: true}},
		{name: "author has substring", input: `author : "alice"`, want: storage.ActiveWorkUnitFilter{AuthorSubstring: "alice", HasAuthor: true}},
		{name: "author quoted space", input: `author = "alice bob"`, want: storage.ActiveWorkUnitFilter{AuthorSubstring: "alice bob", HasAuthor: true}},
		{name: "status", input: `status = RUNNING`, want: storage.ActiveWorkUnitFilter{Status: essmsgpb.WorkUnitStatus_RUNNING}},
		{name: "author and status", input: `author = "alice@example.com" AND status = SCHEDULING`, want: storage.ActiveWorkUnitFilter{AuthorSubstring: "alice@example.com", HasAuthor: true, Status: essmsgpb.WorkUnitStatus_SCHEDULING}},
		{name: "unquoted author", input: `author = alice`, wantErr: true},
		{name: "duplicate author", input: `author = "a" AND author = "b"`, wantErr: true},
		{name: "duplicate status", input: `status = RUNNING AND status = STARTING`, wantErr: true},
		{name: "status has rejected", input: `status : RUNNING`, wantErr: true},
		{name: "completed status rejected", input: `status = COMPLETED`, wantErr: true},
		{name: "or rejected", input: `status = RUNNING OR status = STARTING`, wantErr: true},
		{name: "unsupported field", input: `queue = "q1"`, wantErr: true},
		{name: "syntax error", input: `author = "unterminated`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJobsFilter(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseJobsFilter(%q) err = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr {
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("parseJobsFilter(%q) mismatch (-want +got):\n%s", tt.input, diff)
				}
			}
		})
	}
}
