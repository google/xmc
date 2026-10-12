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
	"xmanager_cloud/storage/spanner/schema/workunitschema"
	"xmanager_cloud/validator/validator" // For name construction/validation
)

// WorkUnitRowToProto converts a Spanner Row containing WorkUnits table data into a WorkUnit proto message.
func WorkUnitRowToProto(row *spanner.Row) (*pb.WorkUnit, error) {
	workUnit := &pb.WorkUnit{
		CurrentWorkUnitExecution: &pb.WorkUnitExecution{},
	}

	var xid, workUnitID int64
	if err := row.ColumnByName("Xid", &xid); err != nil {
		return nil, fmt.Errorf("failed to read column Xid: %w", err)
	}
	if err := row.ColumnByName("WorkUnitId", &workUnitID); err != nil {
		return nil, fmt.Errorf("failed to read column WorkUnitId: %w", err)
	}
	workUnit.Name = validator.ConstructWorkUnitName(xid, workUnitID)

	var workUnitType pb.WorkUnit_WorkUnitType
	if err := row.ColumnByName("WorkUnitType", &workUnitType); err != nil {
		return nil, fmt.Errorf("failed to read column WorkUnitType: %w", err)
	}
	workUnit.WorkUnitType = workUnitType

	if err := row.ColumnByName("Priority", &workUnit.Priority); err != nil {
		return nil, fmt.Errorf("failed to read column Priority: %w", err)
	}

	if err := row.ColumnByName("Notes", &workUnit.Notes); err != nil {
		return nil, fmt.Errorf("failed to read column Notes: %w", err)
	}
	if err := row.ColumnByName("Tags", &workUnit.Tags); err != nil {
		return nil, fmt.Errorf("failed to read column Tags: %w", err)
	}

	if err := row.ColumnByName("Urls", &workUnit.Urls); err != nil {
		return nil, fmt.Errorf("failed to read column Urls: %w", err)
	}
	if err := row.ColumnByName("Executable", &workUnit.Executable); err != nil {
		return nil, fmt.Errorf("failed to read column Executable: %w", err)
	}

	var executionCreateTime, executionUpdateTime, executionCompleteTime spanner.NullTime
	if err := row.ColumnByName("ExecutionCreateTime", &executionCreateTime); err != nil {
		return nil, fmt.Errorf("failed to read column ExecutionCreateTime: %w", err)
	}
	if executionCreateTime.Valid {
		workUnit.CurrentWorkUnitExecution.CreateTime = timestamppb.New(executionCreateTime.Time)
	}
	if err := row.ColumnByName("ExecutionUpdateTime", &executionUpdateTime); err != nil {
		return nil, fmt.Errorf("failed to read column ExecutionUpdateTime: %w", err)
	}
	if executionUpdateTime.Valid {
		workUnit.CurrentWorkUnitExecution.UpdateTime = timestamppb.New(executionUpdateTime.Time)
	}
	if err := row.ColumnByName("ExecutionCompleteTime", &executionCompleteTime); err != nil {
		return nil, fmt.Errorf("failed to read column ExecutionCompleteTime: %w", err)
	}
	if executionCompleteTime.Valid {
		workUnit.CurrentWorkUnitExecution.CompleteTime = timestamppb.New(executionCompleteTime.Time)
	}
	var eid int64
	if err := row.ColumnByName("ExecutionId", &eid); err != nil {
		return nil, fmt.Errorf("failed to read column ExecutionId: %w", err)
	}
	workUnit.CurrentWorkUnitExecution.Name = validator.ConstructWorkUnitExecutionName(xid, workUnitID, eid)
	if err := row.ColumnByName("ExecutionMetadata", &workUnit.CurrentWorkUnitExecution.Metadata); err != nil {
		return nil, fmt.Errorf("failed to read column ExecutionMetadata: %w", err)
	}
	if err := row.ColumnByName("WorkUnitStatus", &workUnit.CurrentWorkUnitExecution.Status); err != nil {
		return nil, fmt.Errorf("failed to read column WorkUnitStatus: %w", err)
	}
	workUnit.CurrentWorkUnitExecution.State = pb.WorkUnitExecution_ACTIVE

	var createTime, updateTime spanner.NullTime
	if err := row.ColumnByName("CreateTime", &createTime); err != nil {
		return nil, fmt.Errorf("failed to read column CreateTime: %w", err)
	}
	if createTime.Valid {
		workUnit.CreateTime = timestamppb.New(createTime.Time)
	}
	if err := row.ColumnByName("UpdateTime", &updateTime); err != nil {
		return nil, fmt.Errorf("failed to read column UpdateTime: %w", err)
	}
	if updateTime.Valid {
		workUnit.UpdateTime = timestamppb.New(updateTime.Time)
	}

	return workUnit, nil
}

// WorkUnitProtoToInsertMutation creates a Spanner mutation to insert a WorkUnit.
func WorkUnitProtoToInsertMutation(workUnit *pb.WorkUnit) (*spanner.Mutation, error) {
	xid, wid, eid, err := validator.ValidateWorkUnitExecutionName(workUnit.CurrentWorkUnitExecution.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate WorkUnitExecution name %q: %w", workUnit.CurrentWorkUnitExecution.GetName(), err)
	}

	var executionCompleteTime spanner.NullTime
	if workUnit.CurrentWorkUnitExecution.GetCompleteTime() != nil {
		executionCompleteTime = spanner.NullTime{Time: workUnit.CurrentWorkUnitExecution.GetCompleteTime().AsTime(), Valid: true}
	}

	urls := workUnit.Urls
	if urls == nil {
		urls = []*pb.Url{}
	}
	tags := workUnit.Tags
	if tags == nil {
		tags = []string{}
	}

	return spanner.Insert(
		workunitschema.WorkUnitTableName,
		workunitschema.WorkUnitColumns,
		[]any{
			xid,                      // XID
			wid,                      // WorkUnitId
			workUnit.WorkUnitType,    // WorkUnitType
			int64(workUnit.Priority), // Priority
			urls,                     // Urls
			tags,                     // Tags
			workUnit.Notes,           // Notes
			workUnit.Executable,      // Executable
			workUnit.CurrentWorkUnitExecution.CreateTime.AsTime(), // ExecutionCreateTime
			workUnit.CurrentWorkUnitExecution.UpdateTime.AsTime(), // ExecutionUpdateTime
			executionCompleteTime, // ExecutionCompleteTime
			eid,                   // ExecutionId
			workUnit.CurrentWorkUnitExecution.Metadata, // ExecutionMetadata
			workUnit.CurrentWorkUnitExecution.Status,   // WorkUnitStatus
			workUnit.CreateTime.AsTime(),               // CreateTime
			workUnit.UpdateTime.AsTime(),               // UpdateTime
		},
	), nil
}

// WorkUnitProtoToUpdateMutation creates a Spanner mutation to update a WorkUnits fields.
func WorkUnitProtoToUpdateMutation(workUnit *pb.WorkUnit) (*spanner.Mutation, error) {
	xid, wid, err := validator.ValidateWorkUnitName(workUnit.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate work unit name %q: %w", workUnit.GetName(), err)
	}
	urls := workUnit.Urls
	if urls == nil {
		urls = []*pb.Url{}
	}
	tags := workUnit.Tags
	if tags == nil {
		tags = []string{}
	}
	return spanner.Update(
		workunitschema.WorkUnitTableName,
		workunitschema.WorkUnitUpdateColumns,
		[]any{
			data.ReverseBits(xid),        // BitReversedXID
			wid,                          // WorkUnitId
			int64(workUnit.Priority),     // Priority
			urls,                         // Urls
			tags,                         // Tags
			workUnit.Notes,               // Notes
			workUnit.UpdateTime.AsTime(), // UpdateTime
		},
	), nil
}
