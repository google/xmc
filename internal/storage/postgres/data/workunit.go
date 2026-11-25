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
	structpb "google.golang.org/protobuf/types/known/structpb"
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
	Parameters                     []byte     `db:"parameters"`
	TrialID                        *string    `db:"trialid"`
}

// GetInsertArgs returns the strict named arguments required by the WorkUnits INSERT statement.
func (r WorkUnitRow) GetInsertArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid": r.Xid, "workunitid": r.WorkUnitID, "workunittype": r.WorkUnitType, "priority": r.Priority,
		"urls": r.Urls, "tags": r.Tags, "notes": r.Notes, "executable": r.Executable,
		"executioncreatetime": r.ExecutionCreateTime, "executionupdatetime": r.ExecutionUpdateTime,
		"executioncompletetime": r.ExecutionCompleteTime, "executionid": r.ExecutionID,
		"executionmetadata": r.ExecutionMetadata, "workunitstatus": r.WorkUnitStatus,
		"queuename": r.QueueName, "executionstate": r.ExecutionState,
		"executionstatusmessageseverity": r.ExecutionStatusMessageSeverity, "positioninqueue": r.PositionInQueue,
		"createtime": r.CreateTime, "updatetime": r.UpdateTime,
		"parameters": r.Parameters, "trialid": r.TrialID,
	}
}

// GetUpdateArgs returns the strict named arguments required by the WorkUnits metadata UPDATE statement.
func (r WorkUnitRow) GetUpdateArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid": r.Xid, "workunitid": r.WorkUnitID, "priority": r.Priority,
		"urls": r.Urls, "tags": r.Tags, "notes": r.Notes, "updatetime": r.UpdateTime,
	}
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

func (r WorkUnitRow) currentExecutionRow() WorkUnitExecutionRow {
	return WorkUnitExecutionRow{
		Xid: r.Xid, WorkUnitID: r.WorkUnitID, ExecutionID: r.ExecutionID,
		ExecutionMetadata: r.ExecutionMetadata, WorkUnitStatus: r.WorkUnitStatus,
		CreateTime: r.ExecutionCreateTime, UpdateTime: r.ExecutionUpdateTime, CompleteTime: r.ExecutionCompleteTime,
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
	var completeTime *timestamppb.Timestamp
	if row.CompleteTime != nil {
		completeTime = timestamppb.New(*row.CompleteTime)
	}
	return &essmsgpb.WorkUnitExecution{
		Name:         validator.ConstructWorkUnitExecutionName(row.Xid, row.WorkUnitID, row.ExecutionID),
		State:        essmsgpb.WorkUnitExecution_INACTIVE,
		Metadata:     metadata,
		Status:       status,
		CreateTime:   timestamppb.New(row.CreateTime),
		UpdateTime:   timestamppb.New(row.UpdateTime),
		CompleteTime: completeTime,
	}, nil
}

// WorkUnitCurrentExecutionRowToProto converts a WorkUnitRow containing current execution columns into an active WorkUnitExecution proto.
//
// Note: This intentionally follows Spanner WorkUnitRowToProto by populating UpdateTime and CompleteTime
// from the stored ExecutionUpdateTime and ExecutionCompleteTime columns (rather than Spanner
// WorkUnitCurrentExecutionRowToProto, which sets UpdateTime from Status.UpdateTime and omits CompleteTime).
func WorkUnitCurrentExecutionRowToProto(row WorkUnitRow) (*essmsgpb.WorkUnitExecution, error) {
	wue, err := WorkUnitExecutionRowToProto(row.currentExecutionRow())
	if err != nil {
		return nil, err
	}
	wue.State = essmsgpb.WorkUnitExecution_ACTIVE
	return wue, nil
}

// UpdatingWorkUnitRowToProto converts a partial WorkUnitRow selected by FetchUpdatingWorkUnits into a WorkUnit proto.
func UpdatingWorkUnitRowToProto(row WorkUnitRow) (*essmsgpb.WorkUnit, error) {
	execProto, err := UnmarshalProto[essmsgpb.Executable](row.Executable)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Executable: %w", err)
	}
	wue, err := WorkUnitExecutionRowToProto(row.currentExecutionRow())
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
	urls, err := unmarshalProtoSlice[essmsgpb.Url](row.Urls)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Urls: %w", err)
	}
	execProto, err := UnmarshalProto[essmsgpb.Executable](row.Executable)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Executable: %w", err)
	}
	wue, err := WorkUnitCurrentExecutionRowToProto(row)
	if err != nil {
		return nil, err
	}
	wu := &essmsgpb.WorkUnit{
		Name:                     validator.ConstructWorkUnitName(row.Xid, row.WorkUnitID),
		WorkUnitType:             essmsgpb.WorkUnit_WorkUnitType(row.WorkUnitType),
		Priority:                 essmsgpb.Priority(row.Priority),
		Urls:                     urls,
		Tags:                     normalizeTags(row.Tags),
		Notes:                    row.Notes,
		Executable:               execProto,
		CurrentWorkUnitExecution: wue,
		CreateTime:               timestamppb.New(row.CreateTime),
		UpdateTime:               timestamppb.New(row.UpdateTime),
	}
	if err := setTrialFields(wu, row.Parameters, row.TrialID); err != nil {
		return nil, err
	}
	return wu, nil
}

// setTrialFields sets the study trial fields of wu from the nullable Parameters and TrialId
// columns. NULL, stored for work units that are not trials, leaves the fields unset.
func setTrialFields(wu *essmsgpb.WorkUnit, parameters []byte, trialID *string) error {
	if parameters != nil {
		params, err := UnmarshalProto[structpb.Struct](parameters)
		if err != nil {
			return fmt.Errorf("failed to unmarshal Parameters: %w", err)
		}
		wu.Parameters = params
	}
	if trialID != nil {
		wu.TrialId = *trialID
	}
	return nil
}

// trialFieldsToColumns returns the nullable Parameters and TrialId column values of wu, which are
// NULL for work units that are not study trials.
func trialFieldsToColumns(wu *essmsgpb.WorkUnit) ([]byte, *string, error) {
	parameters, err := MarshalNullableProto(wu.GetParameters())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal Parameters: %w", err)
	}
	if wu.GetTrialId() == "" {
		return parameters, nil, nil
	}
	trialID := wu.GetTrialId()
	return parameters, &trialID, nil
}

// WorkUnitProtoToRow converts a WorkUnit proto into a WorkUnitRow for insertion.
func WorkUnitProtoToRow(wu *essmsgpb.WorkUnit) (WorkUnitRow, error) {
	exec := wu.GetCurrentWorkUnitExecution()
	xid, wid, eid, err := validator.ValidateWorkUnitExecutionName(exec.GetName())
	if err != nil {
		return WorkUnitRow{}, fmt.Errorf("failed to validate WorkUnitExecution name %q: %w", exec.GetName(), err)
	}
	urls, err := marshalUrls(wu.GetUrls())
	if err != nil {
		return WorkUnitRow{}, err
	}
	execBytes, err := MarshalProto(wu.GetExecutable())
	if err != nil {
		return WorkUnitRow{}, fmt.Errorf("failed to marshal Executable: %w", err)
	}
	row, err := CurrentWorkUnitExecutionProtoToRow(xid, wid, eid, exec)
	if err != nil {
		return WorkUnitRow{}, err
	}
	row.Parameters, row.TrialID, err = trialFieldsToColumns(wu)
	if err != nil {
		return WorkUnitRow{}, err
	}
	row.WorkUnitType = int64(wu.GetWorkUnitType())
	row.Priority = int64(wu.GetPriority())
	row.Urls = urls
	row.Tags = normalizeTags(wu.GetTags())
	row.Notes = wu.GetNotes()
	row.Executable = execBytes
	row.QueueName = wu.GetExecutable().GetSchedulingConstraints().GetQueueName()
	row.CreateTime = wu.GetCreateTime().AsTime()
	row.UpdateTime = wu.GetUpdateTime().AsTime()
	return row, nil
}

// WorkUnitProtoToUpdateRow converts a WorkUnit proto into a WorkUnitRow for metadata updates.
func WorkUnitProtoToUpdateRow(wu *essmsgpb.WorkUnit) (WorkUnitRow, error) {
	xid, wid, err := validator.ValidateWorkUnitName(wu.GetName())
	if err != nil {
		return WorkUnitRow{}, fmt.Errorf("failed to validate work unit name %q: %w", wu.GetName(), err)
	}
	urls, err := marshalUrls(wu.GetUrls())
	if err != nil {
		return WorkUnitRow{}, err
	}
	return WorkUnitRow{
		Xid: xid, WorkUnitID: wid, Priority: int64(wu.GetPriority()),
		Urls: urls, Tags: normalizeTags(wu.GetTags()), Notes: wu.GetNotes(),
		UpdateTime: wu.GetUpdateTime().AsTime(),
	}, nil
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
	var completeTime *time.Time
	if ts := execution.GetCompleteTime(); ts != nil {
		t := ts.AsTime()
		completeTime = &t
	}
	row := WorkUnitRow{
		Xid: xid, WorkUnitID: wid, ExecutionID: eid,
		ExecutionCreateTime: execution.GetCreateTime().AsTime(), ExecutionUpdateTime: execution.GetUpdateTime().AsTime(),
		ExecutionCompleteTime: completeTime, ExecutionMetadata: metadataBytes, WorkUnitStatus: statusBytes,
		UpdateTime: execution.GetUpdateTime().AsTime(),
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
	return row.currentExecutionRow(), nil
}

func populateDerivedStatusFields(row *WorkUnitRow, status *essmsgpb.WorkUnitStatus) {
	row.ExecutionState = int64(status.GetState())
	row.ExecutionStatusMessageSeverity = int64(max(status.GetSystemMessage().GetSeverity(), status.GetUserMessage().GetSeverity()))
	row.PositionInQueue = math.MaxInt64
	if qi := status.GetSchedulingInformation().GetAuxiliaryInfo().GetQueueInfo(); qi.GetPositionInQueue() > 0 {
		row.PositionInQueue = int64(qi.GetPositionInQueue())
	}
}

func marshalUrls(urls []*essmsgpb.Url) ([][]byte, error) {
	res := make([][]byte, len(urls))
	for i, u := range urls {
		b, err := MarshalProto(u)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Url[%d]: %w", i, err)
		}
		res[i] = b
	}
	return res, nil
}

func normalizeTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}
