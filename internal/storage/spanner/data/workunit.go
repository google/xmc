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
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/spanner/schema/workunitschema"
	"xmanager_cloud/internal/validator" // For name construction/validation
)

// WorkUnitRowToProto converts a Spanner Row containing WorkUnits table data into a WorkUnit proto message.
func WorkUnitRowToProto(row *spanner.Row) (*pb.WorkUnit, error) {
	exec := &pb.WorkUnitExecution{State: pb.WorkUnitExecution_ACTIVE}
	workUnit := &pb.WorkUnit{CurrentWorkUnitExecution: exec}

	var xid, workUnitID, eid int64
	var workUnitType pb.WorkUnit_WorkUnitType
	err := readColumns(row,
		colDest{"Xid", &xid},
		colDest{"WorkUnitId", &workUnitID},
		colDest{"WorkUnitType", &workUnitType},
		colDest{"Priority", &workUnit.Priority},
		colDest{"Notes", &workUnit.Notes},
		colDest{"Tags", &workUnit.Tags},
		colDest{"Urls", &workUnit.Urls},
		colDest{"Executable", &workUnit.Executable},
		colDest{"ExecutionId", &eid},
		colDest{"ExecutionMetadata", &exec.Metadata},
		colDest{"WorkUnitStatus", &exec.Status},
	)
	if err != nil {
		return nil, err
	}
	err = readNullTimestamps(row,
		tsDest{"ExecutionCreateTime", &exec.CreateTime},
		tsDest{"ExecutionUpdateTime", &exec.UpdateTime},
		tsDest{"ExecutionCompleteTime", &exec.CompleteTime},
		tsDest{"CreateTime", &workUnit.CreateTime},
		tsDest{"UpdateTime", &workUnit.UpdateTime},
	)
	if err != nil {
		return nil, err
	}

	workUnit.Name = validator.ConstructWorkUnitName(xid, workUnitID)
	workUnit.WorkUnitType = workUnitType
	exec.Name = validator.ConstructWorkUnitExecutionName(xid, workUnitID, eid)
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
