// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package spannerdata

import (
	"testing"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestWorkUnitStatusHistoryToInsertMutation(t *testing.T) {
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
		want          *spanner.Mutation
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
			want: spanner.Insert(
				"WorkUnitStatusHistory",
				[]string{"Xid", "WorkUnitId", "HistoryId", "ExecutionId", "QueueName", "PreviousState", "NewState", "ResourceType", "ResourceUnits", "CreateTime"},
				[]any{
					int64(100),
					int64(1),
					int64(500),
					spanner.NullInt64{Valid: true, Int64: 10},
					"test-queue",
					int64(essmsgpb.WorkUnitStatus_SCHEDULING),
					int64(essmsgpb.WorkUnitStatus_RUNNING),
					"GPU H100",
					int64(8),
					now,
				},
			),
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
			want: spanner.Insert(
				"WorkUnitStatusHistory",
				[]string{"Xid", "WorkUnitId", "HistoryId", "ExecutionId", "QueueName", "PreviousState", "NewState", "ResourceType", "ResourceUnits", "CreateTime"},
				[]any{
					int64(200),
					int64(2),
					int64(600),
					spanner.NullInt64{Valid: false},
					"cpu-queue",
					nil,
					int64(essmsgpb.WorkUnitStatus_RUNNING),
					"cpu",
					int64(4000),
					now,
				},
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WorkUnitStatusHistoryToInsertMutation(
				tt.xid, tt.wid, tt.historyID, tt.eid, tt.queueName, tt.prevState, tt.newState, tt.resourceType, tt.resourceUnits, tt.t,
			)
			if err != nil {
				t.Fatalf("WorkUnitStatusHistoryToInsertMutation() returned error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform(), cmp.AllowUnexported(spanner.Mutation{})); diff != "" {
				t.Errorf("WorkUnitStatusHistoryToInsertMutation() returned unexpected diff (-want +got):\n%s", diff)
			}
		})
	}
}
