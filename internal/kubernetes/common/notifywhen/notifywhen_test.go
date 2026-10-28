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

package notifywhen

import (
	"testing"

	essmsgpb "xmanager_cloud/api/protos/experiment"
)

func TestEncode(t *testing.T) {
	tests := []struct {
		name   string
		states []essmsgpb.WorkUnitStatus_WorkUnitState
		want   string
	}{
		{name: "no states", states: nil, want: ""},
		{
			name: "canonical order regardless of input order and duplicates",
			states: []essmsgpb.WorkUnitStatus_WorkUnitState{
				essmsgpb.WorkUnitStatus_COMPLETED,
				essmsgpb.WorkUnitStatus_STARTING,
				essmsgpb.WorkUnitStatus_COMPLETED,
			},
			want: "STARTING.COMPLETED",
		},
		{
			name:   "non canonical states are ignored",
			states: []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_RESTARTING},
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Encode(tt.states); got != tt.want {
				t.Errorf("Encode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMatches(t *testing.T) {
	tests := []struct {
		name  string
		state essmsgpb.WorkUnitStatus_WorkUnitState
		value string
		want  bool
	}{
		{name: "empty value", state: essmsgpb.WorkUnitStatus_RUNNING, value: "", want: false},
		{name: "listed", state: essmsgpb.WorkUnitStatus_RUNNING, value: "STARTING.RUNNING", want: true},
		{name: "not listed", state: essmsgpb.WorkUnitStatus_FAILED, value: "STARTING.RUNNING", want: false},
		{name: "no partial token match", state: essmsgpb.WorkUnitStatus_RUNNING, value: "RUNNINGX", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Matches(tt.state, tt.value); got != tt.want {
				t.Errorf("Matches(%v, %q) = %v, want %v", tt.state, tt.value, got, tt.want)
			}
		})
	}
}

func TestEncodeMatchesRoundTrip(t *testing.T) {
	encoded := Encode(canonicalOrder)
	for _, s := range canonicalOrder {
		if !Matches(s, encoded) {
			t.Errorf("Matches(%v, Encode(all)) = false, want true", s)
		}
	}
}
