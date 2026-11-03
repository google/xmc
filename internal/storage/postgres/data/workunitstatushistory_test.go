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

package postgresdata

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgtype"
	essmsgpb "xmanager_cloud/api/protos/experiment"
)

func TestNewWorkUnitStatusHistoryRow(t *testing.T) {
	eid := int64(10)
	prevState := essmsgpb.WorkUnitStatus_SCHEDULING
	newState := essmsgpb.WorkUnitStatus_RUNNING
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		xid           int64
		wid           int64
		historyID     int64
		eid           *int64
		queueName     string
		prevState     *essmsgpb.WorkUnitStatus_WorkUnitState
		newState      *essmsgpb.WorkUnitStatus_WorkUnitState
		resourceType  string
		resourceUnits int64
		t             time.Time
		want          WorkUnitStatusHistoryRow
	}{
		{
			name:          "full fields populated",
			xid:           100,
			wid:           1,
			historyID:     500,
			eid:           &eid,
			queueName:     "test-queue",
			prevState:     &prevState,
			newState:      &newState,
			resourceType:  "GPU H100",
			resourceUnits: 8,
			t:             now,
			want: WorkUnitStatusHistoryRow{
				Xid:           100,
				WorkUnitID:    1,
				HistoryID:     500,
				ExecutionID:   pgtype.Int8{Int64: 10, Valid: true},
				QueueName:     "test-queue",
				PreviousState: pgtype.Int8{Int64: int64(essmsgpb.WorkUnitStatus_SCHEDULING), Valid: true},
				NewState:      int64(essmsgpb.WorkUnitStatus_RUNNING),
				ResourceType:  "GPU H100",
				ResourceUnits: 8,
				CreateTime:    now,
			},
		},
		{
			name:          "nil optional execution and previous state",
			xid:           200,
			wid:           2,
			historyID:     600,
			eid:           nil,
			queueName:     "cpu-queue",
			prevState:     nil,
			newState:      &newState,
			resourceType:  "cpu",
			resourceUnits: 4000,
			t:             now,
			want: WorkUnitStatusHistoryRow{
				Xid:           200,
				WorkUnitID:    2,
				HistoryID:     600,
				ExecutionID:   pgtype.Int8{Valid: false},
				QueueName:     "cpu-queue",
				PreviousState: pgtype.Int8{Valid: false},
				NewState:      int64(essmsgpb.WorkUnitStatus_RUNNING),
				ResourceType:  "cpu",
				ResourceUnits: 4000,
				CreateTime:    now,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewWorkUnitStatusHistoryRow(
				tt.xid, tt.wid, tt.historyID, tt.eid, tt.queueName, tt.prevState, tt.newState, tt.resourceType, tt.resourceUnits, tt.t,
			)
			if err != nil {
				t.Fatalf("NewWorkUnitStatusHistoryRow() returned error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("NewWorkUnitStatusHistoryRow() mismatch (-want +got):\n%s", diff)
			}
			args := got.GetInsertArgs()
			if len(args) != 10 || args["historyid"] != tt.historyID || args["queuename"] != tt.queueName {
				t.Errorf("GetInsertArgs() = %v, want 10 entries with matching historyid and queuename", args)
			}
		})
	}
}

func TestNewWorkUnitStatusHistoryRow_ZeroTimestampAndNilNewState(t *testing.T) {
	newState := essmsgpb.WorkUnitStatus_STARTING
	got, err := NewWorkUnitStatusHistoryRow(1, 2, 3, nil, "q1", nil, &newState, "CPU", 4, time.Time{})
	if err != nil {
		t.Fatalf("NewWorkUnitStatusHistoryRow(zero time) unexpected error: %v", err)
	}
	if got.CreateTime.IsZero() {
		t.Error("NewWorkUnitStatusHistoryRow(zero time) left CreateTime zero, want current time")
	}

	if _, err := NewWorkUnitStatusHistoryRow(1, 2, 3, nil, "q1", nil, nil, "CPU", 4, time.Now()); err == nil {
		t.Error("NewWorkUnitStatusHistoryRow(nil newState) succeeded, want error")
	}
}
