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
)

func TestNewSchedulingPaused(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	event := NewSchedulingPaused()
	after := time.Now().UTC().Add(time.Second)

	if event.Event != EventSchedulingPaused {
		t.Errorf("event.Event = %q, want %q", event.Event, EventSchedulingPaused)
	}
	if event.Timestamp.Before(before) || event.Timestamp.After(after) {
		t.Errorf("event.Timestamp = %v, want between %v and %v", event.Timestamp, before, after)
	}
	if event.Timestamp.Location() != time.UTC {
		t.Errorf("event.Timestamp.Location() = %v, want UTC", event.Timestamp.Location())
	}

	bytes, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal(event) unexpected error: %v", err)
	}

	var unmarshaled SchedulingPaused
	if err := json.Unmarshal(bytes, &unmarshaled); err != nil {
		t.Fatalf("json.Unmarshal() unexpected error: %v", err)
	}
	if unmarshaled.Event != EventSchedulingPaused {
		t.Errorf("unmarshaled.Event = %q, want %q", unmarshaled.Event, EventSchedulingPaused)
	}
}

func TestNewSchedulingResumed(t *testing.T) {
	runID := "run-12345"
	before := time.Now().UTC().Add(-time.Second)
	event := NewSchedulingResumed(runID)
	after := time.Now().UTC().Add(time.Second)

	if event.Event != EventSchedulingResumed {
		t.Errorf("event.Event = %q, want %q", event.Event, EventSchedulingResumed)
	}
	if event.ResumptionRunID != runID {
		t.Errorf("event.ResumptionRunID = %q, want %q", event.ResumptionRunID, runID)
	}
	if event.Timestamp.Before(before) || event.Timestamp.After(after) {
		t.Errorf("event.Timestamp = %v, want between %v and %v", event.Timestamp, before, after)
	}
	if event.Timestamp.Location() != time.UTC {
		t.Errorf("event.Timestamp.Location() = %v, want UTC", event.Timestamp.Location())
	}

	bytes, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal(event) unexpected error: %v", err)
	}

	var unmarshaled SchedulingResumed
	if err := json.Unmarshal(bytes, &unmarshaled); err != nil {
		t.Fatalf("json.Unmarshal() unexpected error: %v", err)
	}
	if unmarshaled.Event != EventSchedulingResumed {
		t.Errorf("unmarshaled.Event = %q, want %q", unmarshaled.Event, EventSchedulingResumed)
	}
	if unmarshaled.ResumptionRunID != runID {
		t.Errorf("unmarshaled.ResumptionRunID = %q, want %q", unmarshaled.ResumptionRunID, runID)
	}
}
