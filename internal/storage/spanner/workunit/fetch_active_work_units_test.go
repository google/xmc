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
	"context"
	"testing"
	"time"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestParseWorkUnitStateEnum(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		wantState essmsgpb.WorkUnitStatus_WorkUnitState
	}{
		{
			name:      "Initializing",
			state:     "INITIALIZING",
			wantState: essmsgpb.WorkUnitStatus_INITIALIZING,
		},
		{
			name:      "Scheduling",
			state:     "SCHEDULING",
			wantState: essmsgpb.WorkUnitStatus_SCHEDULING,
		},
		{
			name:      "Running",
			state:     "RUNNING",
			wantState: essmsgpb.WorkUnitStatus_RUNNING,
		},
		{
			name:      "Unknown",
			state:     "UNKNOWN_STATE",
			wantState: essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWorkUnitStateEnum(tt.state)
			if got != tt.wantState {
				t.Errorf("parseWorkUnitStateEnum(%q) = %v, want %v", tt.state, got, tt.wantState)
			}
		})
	}
}

func TestFetchActiveWorkUnitsByQueues_EmptyQueue(t *testing.T) {
	repo := &Repository{}
	workUnits, _, err := repo.FetchActiveWorkUnitsByQueues(context.Background(), nil, 10, "")
	if err != nil {
		t.Errorf("FetchActiveWorkUnitsByQueues(nil) expected nil error, got %v", err)
	}
	if len(workUnits) != 0 {
		t.Errorf("FetchActiveWorkUnitsByQueues(nil) expected empty workUnits slice, got %d", len(workUnits))
	}
}

func TestFetchActiveWorkUnitsByQueuesAt_EmptyQueuesList(t *testing.T) {
	repo := &Repository{}
	workUnits, _, err := repo.FetchActiveWorkUnitsByQueuesAt(context.Background(), []string{}, 10, "", time.Now())
	if err != nil {
		t.Errorf("FetchActiveWorkUnitsByQueuesAt(empty) expected nil error, got %v", err)
	}
	if len(workUnits) != 0 {
		t.Errorf("FetchActiveWorkUnitsByQueuesAt(empty) expected empty workUnits slice, got %d", len(workUnits))
	}
}
