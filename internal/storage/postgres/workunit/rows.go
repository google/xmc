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
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/validator"
)

// unknownQueuePosition is the PositionInQueue stored for work units whose status carries no queue
// position, so that they sort after every positioned work unit; it is the column's default.
const unknownQueuePosition int64 = math.MaxInt64

// workUnitColumns lists the WorkUnits columns that make up a WorkUnit proto, qualified with the
// table alias wu and in the order of workunitschema.WorkUnitSelectFields, which the generated
// ListWorkUnits query selects. Rows of these columns scan into workUnitRow.
const workUnitColumns = `wu.Xid, wu.WorkUnitId, wu.WorkUnitType, wu.Priority, wu.Urls, wu.Tags, wu.Notes, wu.Executable, wu.ExecutionCreateTime, wu.ExecutionUpdateTime, wu.ExecutionCompleteTime, wu.ExecutionId, wu.ExecutionMetadata, wu.WorkUnitStatus, wu.QueueName, wu.ExecutionState, wu.CreateTime, wu.UpdateTime`

// workUnitRow is one WorkUnits row of workUnitColumns. pgx matches the exported fields to the
// returned columns by name, ignoring case, so the field order does not matter.
type workUnitRow struct {
	XID                   int64
	WorkUnitID            int64
	WorkUnitType          int64
	Priority              int64
	Urls                  [][]byte
	Tags                  []string
	Notes                 string
	Executable            []byte
	ExecutionCreateTime   time.Time
	ExecutionUpdateTime   time.Time
	ExecutionCompleteTime pgtype.Timestamptz
	ExecutionID           int64
	ExecutionMetadata     []byte
	WorkUnitStatus        []byte
	QueueName             string
	ExecutionState        int64
	CreateTime            time.Time
	UpdateTime            time.Time
}

// toProto converts the row into a WorkUnit proto whose CurrentWorkUnitExecution is ACTIVE,
// mirroring spannerdata.WorkUnitRowToProto.
func (row *workUnitRow) toProto() (*essmsgpb.WorkUnit, error) {
	urls, err := unmarshalURLs(row.Urls)
	if err != nil {
		return nil, err
	}
	executable := &essmsgpb.Executable{}
	if err := unmarshalColumn("Executable", row.Executable, executable); err != nil {
		return nil, err
	}
	execution := workUnitExecutionRow{
		XID:               row.XID,
		WorkUnitID:        row.WorkUnitID,
		ExecutionID:       row.ExecutionID,
		ExecutionMetadata: row.ExecutionMetadata,
		WorkUnitStatus:    row.WorkUnitStatus,
		CreateTime:        row.ExecutionCreateTime,
		UpdateTime:        row.ExecutionUpdateTime,
		CompleteTime:      row.ExecutionCompleteTime,
	}
	currentExecution, err := execution.toProto(essmsgpb.WorkUnitExecution_ACTIVE)
	if err != nil {
		return nil, err
	}
	return &essmsgpb.WorkUnit{
		Name:                     validator.ConstructWorkUnitName(row.XID, row.WorkUnitID),
		WorkUnitType:             essmsgpb.WorkUnit_WorkUnitType(row.WorkUnitType),
		Priority:                 essmsgpb.Priority(row.Priority),
		Urls:                     urls,
		Tags:                     row.Tags,
		Notes:                    row.Notes,
		Executable:               executable,
		CurrentWorkUnitExecution: currentExecution,
		CreateTime:               timestamppb.New(row.CreateTime),
		UpdateTime:               timestamppb.New(row.UpdateTime),
	}, nil
}

// workUnitExecutionRow is one execution, either a WorkUnitExecutions row or the current execution
// columns of a WorkUnits row aliased to the same names.
type workUnitExecutionRow struct {
	XID               int64
	WorkUnitID        int64
	ExecutionID       int64
	ExecutionMetadata []byte
	WorkUnitStatus    []byte
	CreateTime        time.Time
	UpdateTime        time.Time
	CompleteTime      pgtype.Timestamptz
}

// toProto converts the row into a WorkUnitExecution proto in the given state with the timestamps
// of the row, mirroring spannerdata.WorkUnitExecutionRowToProto.
func (row *workUnitExecutionRow) toProto(state essmsgpb.WorkUnitExecution_WorkUnitExecutionState) (*essmsgpb.WorkUnitExecution, error) {
	execution, err := row.protoWithoutTimestamps(state)
	if err != nil {
		return nil, err
	}
	execution.CreateTime = timestamppb.New(row.CreateTime)
	execution.UpdateTime = timestamppb.New(row.UpdateTime)
	execution.CompleteTime = nullableTimestampProto(row.CompleteTime)
	return execution, nil
}

// toCurrentExecutionProto converts the current execution columns of a WorkUnits row into an ACTIVE
// WorkUnitExecution proto, mirroring spannerdata.WorkUnitCurrentExecutionRowToProto: UpdateTime
// is taken from the status rather than from the row and CompleteTime is not populated.
func (row *workUnitExecutionRow) toCurrentExecutionProto() (*essmsgpb.WorkUnitExecution, error) {
	execution, err := row.protoWithoutTimestamps(essmsgpb.WorkUnitExecution_ACTIVE)
	if err != nil {
		return nil, err
	}
	execution.CreateTime = timestamppb.New(row.CreateTime)
	execution.UpdateTime = execution.GetStatus().GetUpdateTime()
	return execution, nil
}

// protoWithoutTimestamps builds the execution proto from the row's name columns and proto columns.
func (row *workUnitExecutionRow) protoWithoutTimestamps(state essmsgpb.WorkUnitExecution_WorkUnitExecutionState) (*essmsgpb.WorkUnitExecution, error) {
	metadata := &essmsgpb.WorkUnitExecutionMetadata{}
	if err := unmarshalColumn("ExecutionMetadata", row.ExecutionMetadata, metadata); err != nil {
		return nil, err
	}
	status := &essmsgpb.WorkUnitStatus{}
	if err := unmarshalColumn("WorkUnitStatus", row.WorkUnitStatus, status); err != nil {
		return nil, err
	}
	return &essmsgpb.WorkUnitExecution{
		Name:     validator.ConstructWorkUnitExecutionName(row.XID, row.WorkUnitID, row.ExecutionID),
		Metadata: metadata,
		Status:   status,
		State:    state,
	}, nil
}

// executionProtos holds the serialized proto columns of an execution.
type executionProtos struct {
	metadata []byte
	status   []byte
}

// marshalExecutionProtos serializes the ExecutionMetadata and WorkUnitStatus columns of execution.
func marshalExecutionProtos(execution *essmsgpb.WorkUnitExecution) (executionProtos, error) {
	metadata, err := proto.Marshal(execution.GetMetadata())
	if err != nil {
		return executionProtos{}, fmt.Errorf("failed to marshal execution metadata: %w", err)
	}
	status, err := proto.Marshal(execution.GetStatus())
	if err != nil {
		return executionProtos{}, fmt.Errorf("failed to marshal work unit status: %w", err)
	}
	return executionProtos{metadata: metadata, status: status}, nil
}

// derivedExecutionColumns holds the WorkUnits columns that Spanner derives from WorkUnitStatus
// with generated-column expressions and that PostgreSQL populates on every write of the status.
type derivedExecutionColumns struct {
	// state is WorkUnitStatus.state.
	state int64
	// severity is the greater of the system and user message severities, 0 without messages.
	severity int64
	// position is the queue position reported by the scheduler, unknownQueuePosition when the
	// status carries none.
	position int64
}

// deriveExecutionColumns computes the derived columns for a work unit whose current execution has
// status st.
func deriveExecutionColumns(st *essmsgpb.WorkUnitStatus) derivedExecutionColumns {
	return derivedExecutionColumns{
		state:    int64(st.GetState()),
		severity: int64(max(st.GetSystemMessage().GetSeverity(), st.GetUserMessage().GetSeverity())),
		position: queuePosition(st),
	}
}

// queuePosition returns the scheduler's queue position of a work unit with status st. Positions
// are 1-based, so the proto default 0, which the Spanner expression does not observe either, means
// the position is unknown.
func queuePosition(st *essmsgpb.WorkUnitStatus) int64 {
	position := st.GetSchedulingInformation().GetAuxiliaryInfo().GetQueueInfo().GetPositionInQueue()
	if position == 0 {
		return unknownQueuePosition
	}
	return int64(position)
}

// queueName returns the QueueName column value of a work unit running executable.
func queueName(executable *essmsgpb.Executable) string {
	return executable.GetSchedulingConstraints().GetQueueName()
}

// marshalURLs serializes urls for the BYTEA[] Urls column, storing nil as an empty array.
func marshalURLs(urls []*essmsgpb.Url) ([][]byte, error) {
	encoded := make([][]byte, 0, len(urls))
	for _, url := range urls {
		b, err := proto.Marshal(url)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal url: %w", err)
		}
		encoded = append(encoded, b)
	}
	return encoded, nil
}

// unmarshalURLs decodes the BYTEA[] Urls column.
func unmarshalURLs(encoded [][]byte) ([]*essmsgpb.Url, error) {
	urls := make([]*essmsgpb.Url, 0, len(encoded))
	for _, b := range encoded {
		url := &essmsgpb.Url{}
		if err := unmarshalColumn("Urls", b, url); err != nil {
			return nil, err
		}
		urls = append(urls, url)
	}
	return urls, nil
}

// marshalColumn serializes msg for the proto column name.
func marshalColumn(name string, msg proto.Message) ([]byte, error) {
	b, err := proto.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal column %s: %w", name, err)
	}
	return b, nil
}

// unmarshalColumn decodes the serialized proto column name into msg.
func unmarshalColumn(name string, b []byte, msg proto.Message) error {
	if err := proto.Unmarshal(b, msg); err != nil {
		return fmt.Errorf("failed to read column %s: %w", name, err)
	}
	return nil
}

// tagsOrEmpty returns tags, or an empty slice for nil, for the NOT NULL Tags column.
func tagsOrEmpty(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

// nullableTimestamp maps a nil proto timestamp to SQL NULL.
func nullableTimestamp(ts *timestamppb.Timestamp) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: ts.AsTime(), Valid: ts != nil}
}

// nullableTimestampProto maps a SQL NULL timestamp to a nil proto timestamp.
func nullableTimestampProto(ts pgtype.Timestamptz) *timestamppb.Timestamp {
	if !ts.Valid {
		return nil
	}
	return timestamppb.New(ts.Time)
}
