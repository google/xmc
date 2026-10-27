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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	essmsgpb "xmanager_cloud/api/protos/experiment"
)

// WorkUnitStatusHistoryRow represents a row in the PostgreSQL WorkUnitStatusHistory table.
type WorkUnitStatusHistoryRow struct {
	Xid           int64       `db:"xid"`
	WorkUnitID    int64       `db:"workunitid"`
	HistoryID     int64       `db:"historyid"`
	ExecutionID   pgtype.Int8 `db:"executionid"`
	QueueName     string      `db:"queuename"`
	PreviousState pgtype.Int8 `db:"previousstate"`
	NewState      int64       `db:"newstate"`
	ResourceType  string      `db:"resourcetype"`
	ResourceUnits int64       `db:"resourceunits"`
	CreateTime    time.Time   `db:"createtime"`
}

// GetInsertArgs returns the strict named arguments required by the WorkUnitStatusHistory INSERT statement.
func (r WorkUnitStatusHistoryRow) GetInsertArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid":           r.Xid,
		"workunitid":    r.WorkUnitID,
		"historyid":     r.HistoryID,
		"executionid":   r.ExecutionID,
		"queuename":     r.QueueName,
		"previousstate": r.PreviousState,
		"newstate":      r.NewState,
		"resourcetype":  r.ResourceType,
		"resourceunits": r.ResourceUnits,
		"createtime":    r.CreateTime,
	}
}

// NewWorkUnitStatusHistoryRow converts transition details into a WorkUnitStatusHistoryRow for persistence.
func NewWorkUnitStatusHistoryRow(xid, wid, historyID int64, eid *int64, queueName string, prevState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resourceType string, resourceUnits int64, t time.Time) (WorkUnitStatusHistoryRow, error) {
	if newState == nil {
		return WorkUnitStatusHistoryRow{}, fmt.Errorf("newState cannot be nil")
	}
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return WorkUnitStatusHistoryRow{
		Xid:           xid,
		WorkUnitID:    wid,
		HistoryID:     historyID,
		ExecutionID:   nullableInt8(eid),
		QueueName:     queueName,
		PreviousState: nullableWorkUnitState(prevState),
		NewState:      int64(*newState),
		ResourceType:  resourceType,
		ResourceUnits: resourceUnits,
		CreateTime:    t,
	}, nil
}

func nullableWorkUnitState(s *essmsgpb.WorkUnitStatus_WorkUnitState) pgtype.Int8 {
	if s == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: int64(*s), Valid: true}
}
