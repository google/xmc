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

package pausedtask

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/validator"
)

// workUnitRow holds the WorkUnits columns that pendingTasksSQL selects, in that order. It is the
// PostgreSQL counterpart of the Spanner row that spannerdata.WorkUnitRowToProto decodes.
type workUnitRow struct {
	xid                   int64
	wid                   int64
	workUnitType          int64
	priority              int64
	urls                  [][]byte
	tags                  []string
	notes                 string
	executable            []byte
	executionCreateTime   time.Time
	executionUpdateTime   time.Time
	executionCompleteTime *time.Time
	executionID           int64
	executionMetadata     []byte
	workUnitStatus        []byte
	createTime            time.Time
	updateTime            time.Time
}

// scanTargets returns the scan destinations of the WorkUnits columns of pendingTasksSQL.
func (r *workUnitRow) scanTargets() []any {
	return []any{
		&r.xid, &r.wid, &r.workUnitType, &r.priority, &r.urls, &r.tags, &r.notes, &r.executable,
		&r.executionCreateTime, &r.executionUpdateTime, &r.executionCompleteTime, &r.executionID,
		&r.executionMetadata, &r.workUnitStatus, &r.createTime, &r.updateTime,
	}
}

// toProto converts the row into a WorkUnit proto whose CurrentWorkUnitExecution is ACTIVE, exactly
// as the Spanner implementation decodes a WorkUnits row.
func (r *workUnitRow) toProto() (*pb.WorkUnit, error) {
	execution := &pb.WorkUnitExecution{
		Name:       validator.ConstructWorkUnitExecutionName(r.xid, r.wid, r.executionID),
		CreateTime: timestamppb.New(r.executionCreateTime),
		UpdateTime: timestamppb.New(r.executionUpdateTime),
		Metadata:   &pb.WorkUnitExecutionMetadata{},
		Status:     &pb.WorkUnitStatus{},
		State:      pb.WorkUnitExecution_ACTIVE,
	}
	if r.executionCompleteTime != nil {
		execution.CompleteTime = timestamppb.New(*r.executionCompleteTime)
	}
	if err := proto.Unmarshal(r.executionMetadata, execution.Metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal column ExecutionMetadata: %w", err)
	}
	if err := proto.Unmarshal(r.workUnitStatus, execution.Status); err != nil {
		return nil, fmt.Errorf("failed to unmarshal column WorkUnitStatus: %w", err)
	}

	workUnit := &pb.WorkUnit{
		Name:                     validator.ConstructWorkUnitName(r.xid, r.wid),
		WorkUnitType:             pb.WorkUnit_WorkUnitType(r.workUnitType),
		Priority:                 pb.Priority(r.priority),
		Urls:                     make([]*pb.Url, len(r.urls)),
		Executable:               &pb.Executable{},
		Tags:                     r.tags,
		Notes:                    r.notes,
		CurrentWorkUnitExecution: execution,
		CreateTime:               timestamppb.New(r.createTime),
		UpdateTime:               timestamppb.New(r.updateTime),
	}
	for i, raw := range r.urls {
		workUnit.Urls[i] = &pb.Url{}
		if err := proto.Unmarshal(raw, workUnit.Urls[i]); err != nil {
			return nil, fmt.Errorf("failed to unmarshal column Urls[%d]: %w", i, err)
		}
	}
	if err := proto.Unmarshal(r.executable, workUnit.Executable); err != nil {
		return nil, fmt.Errorf("failed to unmarshal column Executable: %w", err)
	}
	return workUnit, nil
}
