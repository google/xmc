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

package broadcast

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestNewSchedulingPaused(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	ev := NewSchedulingPaused("admin@google.com", "maintenance window")
	after := time.Now().UTC().Add(time.Second)

	if ev.Event != EventSchedulingPaused {
		t.Errorf("Event = %q, want %q", ev.Event, EventSchedulingPaused)
	}
	if ev.UpdatedBy != "admin@google.com" {
		t.Errorf("UpdatedBy = %q, want %q", ev.UpdatedBy, "admin@google.com")
	}
	if ev.Reason != "maintenance window" {
		t.Errorf("Reason = %q, want %q", ev.Reason, "maintenance window")
	}
	if ev.Timestamp.Before(before) || ev.Timestamp.After(after) {
		t.Errorf("Timestamp = %v, want between %v and %v", ev.Timestamp, before, after)
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal() unexpected error: %v", err)
	}
	var unmarshaled SchedulingPaused
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("json.Unmarshal() unexpected error: %v", err)
	}
	if diff := cmp.Diff(ev, unmarshaled); diff != "" {
		t.Errorf("json roundtrip mismatch (-want +got):\n%s", diff)
	}
}

func TestNewSchedulingResumed(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	ev := NewSchedulingResumed("run-12345", "admin@google.com")
	after := time.Now().UTC().Add(time.Second)

	if ev.Event != EventSchedulingResumed {
		t.Errorf("Event = %q, want %q", ev.Event, EventSchedulingResumed)
	}
	if ev.ResumptionRunID != "run-12345" {
		t.Errorf("ResumptionRunID = %q, want %q", ev.ResumptionRunID, "run-12345")
	}
	if ev.UpdatedBy != "admin@google.com" {
		t.Errorf("UpdatedBy = %q, want %q", ev.UpdatedBy, "admin@google.com")
	}
	if ev.Timestamp.Before(before) || ev.Timestamp.After(after) {
		t.Errorf("Timestamp = %v, want between %v and %v", ev.Timestamp, before, after)
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal() unexpected error: %v", err)
	}
	var unmarshaled SchedulingResumed
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("json.Unmarshal() unexpected error: %v", err)
	}
	if diff := cmp.Diff(ev, unmarshaled); diff != "" {
		t.Errorf("json roundtrip mismatch (-want +got):\n%s", diff)
	}
}
