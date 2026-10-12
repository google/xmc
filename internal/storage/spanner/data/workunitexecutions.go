// Copyright 2025 Google LLC
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

	"cloud.google.com/go/spanner"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/storage/spanner/schema/workunitexecutionschema"
	"xmanager_cloud/storage/spanner/schema/workunitschema"
	"xmanager_cloud/validator/validator"
)

// WorkUnitExecutionRowToProto converts a Spanner Row containing WorkUnitExecutions table data into
// a WorkUnitExecution proto message. This function is used to convert rows returned from the
// WorkUnitExecutions table, NOT the current execution data in the WorkUnits table.
func WorkUnitExecutionRowToProto(row *spanner.Row) (*pb.WorkUnitExecution, error) {
	execution := &pb.WorkUnitExecution{}

	var xid, workUnitID, executionID int64
	if err := row.ColumnByName("Xid", &xid); err != nil {
		return nil, fmt.Errorf("failed to read column Xid: %w", err)
	}
	if err := row.ColumnByName("WorkUnitId", &workUnitID); err != nil {
		return nil, fmt.Errorf("failed to read column WorkUnitId: %w", err)
	}
	if err := row.ColumnByName("ExecutionId", &executionID); err != nil {
		return nil, fmt.Errorf("failed to read column ExecutionId: %w", err)
	}
	execution.Name = validator.ConstructWorkUnitExecutionName(xid, workUnitID, executionID)

	if err := row.ColumnByName("ExecutionMetadata", &execution.Metadata); err != nil {
		return nil, fmt.Errorf("failed to read column ExecutionMetadata: %w", err)
	}
	if err := row.ColumnByName("WorkUnitStatus", &execution.Status); err != nil {
		return nil, fmt.Errorf("failed to read column WorkUnitStatus: %w", err)
	}

	var createTime, updateTime, completeTime spanner.NullTime
	if err := row.ColumnByName("CreateTime", &createTime); err != nil {
		return nil, fmt.Errorf("failed to read column CreateTime: %w", err)
	}
	if createTime.Valid {
		execution.CreateTime = timestamppb.New(createTime.Time)
	}

	if err := row.ColumnByName("UpdateTime", &updateTime); err != nil {
		return nil, fmt.Errorf("failed to read column UpdateTime: %w", err)
	}
	if updateTime.Valid {
		execution.UpdateTime = timestamppb.New(updateTime.Time)
	}

	if err := row.ColumnByName("CompleteTime", &completeTime); err != nil {
		return nil, fmt.Errorf("failed to read column CompleteTime: %w", err)
	}
	if completeTime.Valid {
		execution.CompleteTime = timestamppb.New(completeTime.Time)
	}

	execution.State = pb.WorkUnitExecution_INACTIVE

	return execution, nil
}

// WorkUnitCurrentExecutionRowToProto converts a Spanner Row containing just the current execution
// data for a WorkUnit into a WorkUnitExecution proto message.
func WorkUnitCurrentExecutionRowToProto(row *spanner.Row) (*pb.WorkUnitExecution, error) {
	workUnitExecution := &pb.WorkUnitExecution{}

	var executionCreateTime spanner.NullTime
	if err := row.ColumnByName("ExecutionCreateTime", &executionCreateTime); err != nil {
		return nil, fmt.Errorf("failed to read column ExecutionCreateTime: %w", err)
	}
	if executionCreateTime.Valid {
		workUnitExecution.CreateTime = timestamppb.New(executionCreateTime.Time)
	}

	var xid, wid, eid int64
	if err := row.ColumnByName("Xid", &xid); err != nil {
		return nil, fmt.Errorf("failed to read column Xid: %w", err)
	}
	if err := row.ColumnByName("WorkUnitId", &wid); err != nil {
		return nil, fmt.Errorf("failed to read column WorkUnitId: %w", err)
	}
	if err := row.ColumnByName("ExecutionId", &eid); err != nil {
		return nil, fmt.Errorf("failed to read column ExecutionId: %w", err)
	}
	workUnitExecution.Name = validator.ConstructWorkUnitExecutionName(xid, wid, eid)
	if err := row.ColumnByName("ExecutionMetadata", &workUnitExecution.Metadata); err != nil {
		return nil, fmt.Errorf("failed to read column ExecutionMetadata: %w", err)
	}
	if err := row.ColumnByName("WorkUnitStatus", &workUnitExecution.Status); err != nil {
		return nil, fmt.Errorf("failed to read column WorkUnitStatus: %w", err)
	}
	workUnitExecution.State = pb.WorkUnitExecution_ACTIVE
	workUnitExecution.UpdateTime = workUnitExecution.Status.UpdateTime

	return workUnitExecution, nil
}

// WorkUnitExecutionProtoToInsertMutation creates a Spanner mutation to insert a WorkUnitExecution.
func WorkUnitExecutionProtoToInsertMutation(execution *pb.WorkUnitExecution) (*spanner.Mutation, error) {
	xid, wid, eid, err := validator.ValidateWorkUnitExecutionName(execution.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate WorkUnitExecution name %q: %w", execution.GetName(), err)
	}

	var completeTime spanner.NullTime
	if execution.GetCompleteTime() != nil {
		completeTime = spanner.NullTime{Time: execution.GetCompleteTime().AsTime(), Valid: true}
	}

	return spanner.Insert(
		"WorkUnitExecutions",
		workunitexecutionschema.WorkUnitExecutionsColumns,
		[]any{
			xid,                                // XID
			wid,                                // WorkUnitId
			eid,                                // ExecutionId
			execution.Metadata,                 // ExecutionMetadata
			execution.Status,                   // WorkUnitStatus
			execution.GetCreateTime().AsTime(), // CreateTime
			execution.GetUpdateTime().AsTime(), // UpdateTime
			completeTime,                       // CompleteTime
		},
	), nil
}

// CurrentWorkUnitExecutionProtoToUpdateMutation creates a Spanner mutation to update a WorkUnit's
// current execution.
func CurrentWorkUnitExecutionProtoToUpdateMutation(xid, wid, eid int64, execution *pb.WorkUnitExecution) *spanner.Mutation {
	var completeTime spanner.NullTime
	if execution.GetCompleteTime() != nil {
		completeTime = spanner.NullTime{Time: execution.GetCompleteTime().AsTime(), Valid: true}
	}

	m := spanner.Update(
		workunitschema.WorkUnitTableName,
		workunitschema.CurrentWorkUnitExecutionUpdateColumns,
		[]any{
			data.ReverseBits(xid),         // BitReversedXID
			wid,                           // WorkUnitId
			eid,                           // ExecutionId
			execution.CreateTime.AsTime(), // ExecutionCreateTime
			execution.UpdateTime.AsTime(), // ExecutionUpdateTime
			completeTime,                  // ExecutionCompleteTime
			execution.Metadata,            // ExecutionMetadata
			execution.Status,              // WorkUnitStatus
			execution.UpdateTime.AsTime(), // UpdateTime
		},
	)
	return m
}
