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
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/validator"
)

// WorkUnitRow represents a row in the PostgreSQL WorkUnits table.
type WorkUnitRow struct {
	Xid                            int64      `db:"xid"`
	WorkUnitID                     int64      `db:"workunitid"`
	WorkUnitType                   int64      `db:"workunittype"`
	Priority                       int64      `db:"priority"`
	Urls                           [][]byte   `db:"urls"`
	Tags                           []string   `db:"tags"`
	Notes                          string     `db:"notes"`
	Executable                     []byte     `db:"executable"`
	ExecutionCreateTime            time.Time  `db:"executioncreatetime"`
	ExecutionUpdateTime            time.Time  `db:"executionupdatetime"`
	ExecutionCompleteTime          *time.Time `db:"executioncompletetime"`
	ExecutionID                    int64      `db:"executionid"`
	ExecutionMetadata              []byte     `db:"executionmetadata"`
	WorkUnitStatus                 []byte     `db:"workunitstatus"`
	QueueName                      string     `db:"queuename"`
	ExecutionState                 int64      `db:"executionstate"`
	ExecutionStatusMessageSeverity int64      `db:"executionstatusmessageseverity"`
	PositionInQueue                int64      `db:"positioninqueue"`
	CreateTime                     time.Time  `db:"createtime"`
	UpdateTime                     time.Time  `db:"updatetime"`
	Creator                        string     `db:"creator"`
}

// GetCurrentExecutionUpdateArgs returns the strict named arguments required to update the current execution on WorkUnits.
func (r WorkUnitRow) GetCurrentExecutionUpdateArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid": r.Xid, "workunitid": r.WorkUnitID, "executionid": r.ExecutionID,
		"executioncreatetime": r.ExecutionCreateTime, "executionupdatetime": r.ExecutionUpdateTime,
		"executioncompletetime": r.ExecutionCompleteTime, "executionmetadata": r.ExecutionMetadata,
		"workunitstatus": r.WorkUnitStatus, "executionstate": r.ExecutionState,
		"executionstatusmessageseverity": r.ExecutionStatusMessageSeverity,
		"positioninqueue":                r.PositionInQueue, "updatetime": r.UpdateTime,
	}
}

// WorkUnitExecutionRow represents a row in the PostgreSQL WorkUnitExecutions table.
type WorkUnitExecutionRow struct {
	Xid               int64      `db:"xid"`
	WorkUnitID        int64      `db:"workunitid"`
	ExecutionID       int64      `db:"executionid"`
	ExecutionMetadata []byte     `db:"executionmetadata"`
	WorkUnitStatus    []byte     `db:"workunitstatus"`
	CreateTime        time.Time  `db:"createtime"`
	UpdateTime        time.Time  `db:"updatetime"`
	CompleteTime      *time.Time `db:"completetime"`
}

// GetInsertArgs returns the strict named arguments required by the WorkUnitExecutions INSERT statement.
func (r WorkUnitExecutionRow) GetInsertArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid": r.Xid, "workunitid": r.WorkUnitID, "executionid": r.ExecutionID,
		"executionmetadata": r.ExecutionMetadata, "workunitstatus": r.WorkUnitStatus,
		"createtime": r.CreateTime, "updatetime": r.UpdateTime, "completetime": r.CompleteTime,
	}
}

// WorkUnitExecutionRowToProto converts a scanned WorkUnitExecutionRow into an inactive WorkUnitExecution proto.
func WorkUnitExecutionRowToProto(row WorkUnitExecutionRow) (*essmsgpb.WorkUnitExecution, error) {
	metadata, err := UnmarshalProto[essmsgpb.WorkUnitExecutionMetadata](row.ExecutionMetadata)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal ExecutionMetadata: %w", err)
	}
	status, err := UnmarshalProto[essmsgpb.WorkUnitStatus](row.WorkUnitStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal WorkUnitStatus: %w", err)
	}
	return &essmsgpb.WorkUnitExecution{
		Name:         validator.ConstructWorkUnitExecutionName(row.Xid, row.WorkUnitID, row.ExecutionID),
		State:        essmsgpb.WorkUnitExecution_INACTIVE,
		Metadata:     metadata,
		Status:       status,
		CreateTime:   timestamppb.New(row.CreateTime),
		UpdateTime:   timestamppb.New(row.UpdateTime),
		CompleteTime: optionalTimestamp(row.CompleteTime),
	}, nil
}

// WorkUnitCurrentExecutionRowToProto converts a WorkUnitRow containing current execution columns into an active WorkUnitExecution proto.
func WorkUnitCurrentExecutionRowToProto(row WorkUnitRow) (*essmsgpb.WorkUnitExecution, error) {
	wue, err := WorkUnitExecutionRowToProto(WorkUnitExecutionRow{
		Xid: row.Xid, WorkUnitID: row.WorkUnitID, ExecutionID: row.ExecutionID,
		ExecutionMetadata: row.ExecutionMetadata, WorkUnitStatus: row.WorkUnitStatus,
		CreateTime: row.ExecutionCreateTime, UpdateTime: row.ExecutionUpdateTime, CompleteTime: row.ExecutionCompleteTime,
	})
	if err != nil {
		return nil, err
	}
	wue.State = essmsgpb.WorkUnitExecution_ACTIVE
	if wue.Status.GetUpdateTime() != nil {
		wue.UpdateTime = wue.Status.UpdateTime
	}
	return wue, nil
}

// UpdatingWorkUnitRowToProto converts a partial WorkUnitRow selected by FetchUpdatingWorkUnits into a WorkUnit proto.
func UpdatingWorkUnitRowToProto(row WorkUnitRow) (*essmsgpb.WorkUnit, error) {
	execProto, err := UnmarshalProto[essmsgpb.Executable](row.Executable)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Executable: %w", err)
	}
	wue, err := WorkUnitExecutionRowToProto(WorkUnitExecutionRow{
		Xid: row.Xid, WorkUnitID: row.WorkUnitID, ExecutionID: row.ExecutionID,
		ExecutionMetadata: row.ExecutionMetadata, WorkUnitStatus: row.WorkUnitStatus,
		CreateTime: row.ExecutionCreateTime, UpdateTime: row.ExecutionUpdateTime, CompleteTime: row.ExecutionCompleteTime,
	})
	if err != nil {
		return nil, err
	}
	return &essmsgpb.WorkUnit{
		Name:                     validator.ConstructWorkUnitName(row.Xid, row.WorkUnitID),
		Executable:               execProto,
		CurrentWorkUnitExecution: wue,
	}, nil
}

// WorkUnitRowToProto converts a scanned PostgreSQL WorkUnitRow into a WorkUnit proto.
func WorkUnitRowToProto(row WorkUnitRow) (*essmsgpb.WorkUnit, error) {
	urls, err := unmarshalUrls(row.Urls)
	if err != nil {
		return nil, err
	}
	wu, err := UpdatingWorkUnitRowToProto(row)
	if err != nil {
		return nil, err
	}
	wu.CurrentWorkUnitExecution.State = essmsgpb.WorkUnitExecution_ACTIVE
	wu.WorkUnitType = essmsgpb.WorkUnit_WorkUnitType(row.WorkUnitType)
	wu.Priority = essmsgpb.Priority(row.Priority)
	wu.Urls = urls
	wu.Tags = normalizeTags(row.Tags)
	wu.Notes = row.Notes
	wu.CreateTime = timestamppb.New(row.CreateTime)
	wu.UpdateTime = timestamppb.New(row.UpdateTime)
	return wu, nil
}

// CurrentWorkUnitExecutionProtoToRow converts a WorkUnitExecution proto into a WorkUnitRow for updating the current execution.
func CurrentWorkUnitExecutionProtoToRow(xid, wid, eid int64, execution *essmsgpb.WorkUnitExecution) (WorkUnitRow, error) {
	metadataBytes, err := MarshalProto(execution.GetMetadata())
	if err != nil {
		return WorkUnitRow{}, fmt.Errorf("failed to marshal ExecutionMetadata: %w", err)
	}
	statusBytes, err := MarshalProto(execution.GetStatus())
	if err != nil {
		return WorkUnitRow{}, fmt.Errorf("failed to marshal WorkUnitStatus: %w", err)
	}
	row := WorkUnitRow{
		Xid: xid, WorkUnitID: wid, ExecutionID: eid,
		ExecutionCreateTime:   execution.GetCreateTime().AsTime(),
		ExecutionUpdateTime:   execution.GetUpdateTime().AsTime(),
		ExecutionCompleteTime: optionalTime(execution.GetCompleteTime()),
		ExecutionMetadata:     metadataBytes,
		WorkUnitStatus:        statusBytes,
		UpdateTime:            execution.GetUpdateTime().AsTime(),
	}
	populateDerivedStatusFields(&row, execution.GetStatus())
	return row, nil
}

// WorkUnitExecutionProtoToRow converts a WorkUnitExecution proto into a WorkUnitExecutionRow for persistence.
func WorkUnitExecutionProtoToRow(execution *essmsgpb.WorkUnitExecution) (WorkUnitExecutionRow, error) {
	xid, wid, eid, err := validator.ValidateWorkUnitExecutionName(execution.GetName())
	if err != nil {
		return WorkUnitExecutionRow{}, fmt.Errorf("failed to validate WorkUnitExecution name %q: %w", execution.GetName(), err)
	}
	row, err := CurrentWorkUnitExecutionProtoToRow(xid, wid, eid, execution)
	if err != nil {
		return WorkUnitExecutionRow{}, err
	}
	return WorkUnitExecutionRow{
		Xid: xid, WorkUnitID: wid, ExecutionID: eid,
		ExecutionMetadata: row.ExecutionMetadata, WorkUnitStatus: row.WorkUnitStatus,
		CreateTime: row.ExecutionCreateTime, UpdateTime: row.ExecutionUpdateTime, CompleteTime: row.ExecutionCompleteTime,
	}, nil
}

func populateDerivedStatusFields(row *WorkUnitRow, status *essmsgpb.WorkUnitStatus) {
	row.ExecutionState = int64(status.GetState())
	row.ExecutionStatusMessageSeverity = int64(max(status.GetSystemMessage().GetSeverity(), status.GetUserMessage().GetSeverity()))
	row.PositionInQueue = math.MaxInt64
	if qi := status.GetSchedulingInformation().GetAuxiliaryInfo().GetQueueInfo(); qi != nil {
		row.PositionInQueue = int64(qi.GetPositionInQueue())
	}
}

func unmarshalUrls(raw [][]byte) ([]*essmsgpb.Url, error) {
	res := make([]*essmsgpb.Url, len(raw))
	for i, b := range raw {
		u, err := UnmarshalProto[essmsgpb.Url](b)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal Url[%d]: %w", i, err)
		}
		res[i] = u
	}
	return res, nil
}

func normalizeTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

func optionalTime(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}

func optionalTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
