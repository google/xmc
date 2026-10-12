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
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	essmsgpb "github.com/google/xmc/api/protos/experiment"
)

// WorkUnitStatusHistoryItem represents a single transition row in WorkUnitStatusHistory.
type WorkUnitStatusHistoryItem struct {
	Xid           int64
	WorkUnitId    int64
	HistoryId     int64
	ExecutionId   *int64
	QueueName     string
	PreviousState *essmsgpb.WorkUnitStatus_WorkUnitState
	NewState      essmsgpb.WorkUnitStatus_WorkUnitState
	ResourceType  string
	ResourceUnits int64
	CreateTime    time.Time
}

// WorkUnitStatusHistoryToInsertMutation converts transition details into a Spanner insert mutation.
func WorkUnitStatusHistoryToInsertMutation(xid, wid, historyID int64, eid *int64, queueName string, prevState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resourceType string, resourceUnits int64, t time.Time) (*spanner.Mutation, error) {
	if newState == nil {
		return nil, fmt.Errorf("newState cannot be nil")
	}

	var executionID spanner.NullInt64
	if eid != nil {
		executionID = spanner.NullInt64{Valid: true, Int64: *eid}
	}

	var previousStateVal any
	if prevState != nil {
		previousStateVal = int64(*prevState)
	}

	newStateVal := int64(*newState)

	if t.IsZero() {
		t = time.Now()
	}

	return spanner.Insert(
		"WorkUnitStatusHistory",
		[]string{"Xid", "WorkUnitId", "HistoryId", "ExecutionId", "QueueName", "PreviousState", "NewState", "ResourceType", "ResourceUnits", "CreateTime"},
		[]any{
			xid,
			wid,
			historyID,
			executionID,
			queueName,
			previousStateVal,
			newStateVal,
			resourceType,
			resourceUnits,
			t,
		},
	), nil
}
