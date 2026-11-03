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
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// PausedTaskRow represents the insertable columns of a row in the PostgreSQL PausedTasks table.
type PausedTaskRow struct {
	Xid           int64     `db:"xid"`
	WorkUnitID    int64     `db:"workunitid"`
	TaskID        string    `db:"taskid"`
	Target        string    `db:"target"`
	ActionProto   []byte    `db:"actionproto"`
	OperationName string    `db:"operationname"`
	TraceID       string    `db:"traceid"`
	Priority      int64     `db:"priority"`
	CreateTime    time.Time `db:"createtime"`
}

// GetInsertArgs returns the strict named arguments required by the PausedTasks INSERT statement.
func (r PausedTaskRow) GetInsertArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid": r.Xid, "workunitid": r.WorkUnitID, "taskid": r.TaskID, "target": r.Target,
		"actionproto": r.ActionProto, "operationname": r.OperationName, "traceid": r.TraceID,
		"priority": r.Priority, "createtime": r.CreateTime,
	}
}

// PendingPausedTaskRow is a PausedTasks row joined with its parent WorkUnits row. WorkUnits columns
// whose names collide with PausedTasks columns are selected with a "workunit" prefix.
// TODO: decode the WorkUnit with the shared WorkUnitRow converter once it exists in this package.
type PendingPausedTaskRow struct {
	PausedTaskRow
	WorkUnitType          int64      `db:"workunittype"`
	WorkUnitPriority      int64      `db:"workunitpriority"`
	Urls                  [][]byte   `db:"urls"`
	Tags                  []string   `db:"tags"`
	Notes                 string     `db:"notes"`
	Executable            []byte     `db:"executable"`
	ExecutionCreateTime   time.Time  `db:"executioncreatetime"`
	ExecutionUpdateTime   time.Time  `db:"executionupdatetime"`
	ExecutionCompleteTime *time.Time `db:"executioncompletetime"`
	ExecutionID           int64      `db:"executionid"`
	ExecutionMetadata     []byte     `db:"executionmetadata"`
	WorkUnitStatus        []byte     `db:"workunitstatus"`
	WorkUnitCreateTime    time.Time  `db:"workunitcreatetime"`
	WorkUnitUpdateTime    time.Time  `db:"workunitupdatetime"`
}

// PausedTaskToRow converts a storage.PausedTask into a PausedTaskRow for insertion.
func PausedTaskToRow(task *storage.PausedTask) (PausedTaskRow, error) {
	action, err := MarshalProto(task.Action)
	if err != nil {
		return PausedTaskRow{}, fmt.Errorf("failed to marshal ActionProto: %w", err)
	}
	return PausedTaskRow{
		Xid: task.XID, WorkUnitID: task.WID, TaskID: task.TaskID, Target: task.Target, ActionProto: action,
		OperationName: task.OperationName, TraceID: task.TraceID, Priority: task.Priority, CreateTime: task.CreateTime,
	}, nil
}

// PendingPausedTaskRowToTask converts a scanned PendingPausedTaskRow into a storage.PausedTask
// populated with its parent WorkUnit.
func PendingPausedTaskRowToTask(row PendingPausedTaskRow) (*storage.PausedTask, error) {
	action, err := UnmarshalProto[actuatortaskpb.Action](row.ActionProto)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal ActionProto: %w", err)
	}
	workUnit, err := pendingRowToWorkUnit(row)
	if err != nil {
		return nil, err
	}
	return &storage.PausedTask{
		XID: row.Xid, WID: row.WorkUnitID, TaskID: row.TaskID, Target: row.Target, Action: action, WorkUnit: workUnit,
		OperationName: row.OperationName, TraceID: row.TraceID, Priority: row.Priority, CreateTime: row.CreateTime,
	}, nil
}

func pendingRowToWorkUnit(row PendingPausedTaskRow) (*essmsgpb.WorkUnit, error) {
	urls := make([]*essmsgpb.Url, len(row.Urls))
	for i, b := range row.Urls {
		url, err := UnmarshalProto[essmsgpb.Url](b)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal Urls[%d]: %w", i, err)
		}
		urls[i] = url
	}
	executable, err := UnmarshalProto[essmsgpb.Executable](row.Executable)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Executable: %w", err)
	}
	execution, err := pendingRowToExecution(row)
	if err != nil {
		return nil, err
	}
	return &essmsgpb.WorkUnit{
		Name: validator.ConstructWorkUnitName(row.Xid, row.WorkUnitID), WorkUnitType: essmsgpb.WorkUnit_WorkUnitType(row.WorkUnitType),
		Priority: essmsgpb.Priority(row.WorkUnitPriority), Urls: urls, Tags: row.Tags, Notes: row.Notes, Executable: executable,
		CurrentWorkUnitExecution: execution, CreateTime: timestamppb.New(row.WorkUnitCreateTime), UpdateTime: timestamppb.New(row.WorkUnitUpdateTime),
	}, nil
}

func pendingRowToExecution(row PendingPausedTaskRow) (*essmsgpb.WorkUnitExecution, error) {
	metadata, err := UnmarshalProto[essmsgpb.WorkUnitExecutionMetadata](row.ExecutionMetadata)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal ExecutionMetadata: %w", err)
	}
	status, err := UnmarshalProto[essmsgpb.WorkUnitStatus](row.WorkUnitStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal WorkUnitStatus: %w", err)
	}
	execution := &essmsgpb.WorkUnitExecution{
		Name:     validator.ConstructWorkUnitExecutionName(row.Xid, row.WorkUnitID, row.ExecutionID),
		State:    essmsgpb.WorkUnitExecution_ACTIVE,
		Metadata: metadata, Status: status,
		CreateTime: timestamppb.New(row.ExecutionCreateTime), UpdateTime: timestamppb.New(row.ExecutionUpdateTime),
	}
	if row.ExecutionCompleteTime != nil {
		execution.CompleteTime = timestamppb.New(*row.ExecutionCompleteTime)
	}
	return execution, nil
}
