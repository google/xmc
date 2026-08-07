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
	"context"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/timestamppb"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
)

const (
	unassignedQueuePosition int64 = 9223372036854775807
)

// FetchActiveWorkUnitsByQueues queries Spanner for active and pending work units assigned to the given queues.
func (r *Repository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) ([]*essmsgpb.WorkUnit, string, error) {
	return r.FetchActiveWorkUnitsByQueuesAt(ctx, queueNames, pageSize, pageToken, time.Now())
}

// FetchActiveWorkUnitsByQueuesAt queries Spanner using deterministic reference time now.
func (r *Repository) FetchActiveWorkUnitsByQueuesAt(ctx context.Context, queueNames []string, pageSize int32, pageToken string, now time.Time) ([]*essmsgpb.WorkUnit, string, error) {
	if len(queueNames) == 0 {
		return []*essmsgpb.WorkUnit{}, "", nil
	}

	// TODO(#186): Implement cursor-based pagination using page tokens when job list volume increases.
	sql := `SELECT 
		wu.Xid, 
		wu.WorkUnitId, 
		wu.QueueName, 
		wu.Creator, 
		wu.Priority, 
		CAST(wu.ExecutionState AS STRING), 
		wu.CreateTime, 
		wu.SortPhase, 
		wu.PositionInQueue
	FROM WorkUnits wu
	WHERE wu.QueueName IN UNNEST(@queueNames)
	  AND wu.IsPendingOrRunningJob IS NOT NULL
	ORDER BY wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC, wu.WorkUnitId ASC`

	stmt := spanner.Statement{
		SQL: sql,
		Params: map[string]interface{}{
			"queueNames": queueNames,
		},
	}

	iter := r.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	type fetchedRow struct {
		xid             int64
		wid             int64
		qName           string
		creator         string
		priority        int64
		state           string
		createTime      time.Time
		sortPhase       int64
		positionInQueue int64
	}

	var rows []fetchedRow

	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			logging.Error(ctx, "failed to query active work units", "error", err)
			return nil, "", fmt.Errorf("failed to read active work unit rows: %w", err)
		}

		var fr fetchedRow
		var creator spanner.NullString
		if err := row.Columns(&fr.xid, &fr.wid, &fr.qName, &creator, &fr.priority, &fr.state, &fr.createTime, &fr.sortPhase, &fr.positionInQueue); err != nil {
			logging.Error(ctx, "failed to parse work unit row columns", "error", err)
			return nil, "", fmt.Errorf("failed to parse work unit row columns: %w", err)
		}
		if creator.Valid {
			fr.creator = creator.StringVal
		}
		rows = append(rows, fr)
	}

	var workUnits []*essmsgpb.WorkUnit

	for _, fr := range rows {
		posInQueue := fr.positionInQueue
		if posInQueue == unassignedQueuePosition {
			posInQueue = -1
		}

		wu := &essmsgpb.WorkUnit{
			Name:     validator.ConstructWorkUnitName(fr.xid, fr.wid),
			Priority: essmsgpb.Priority(fr.priority),
			CreateTime: timestamppb.New(fr.createTime),
			Executable: &essmsgpb.Executable{
				SchedulingConstraints: &essmsgpb.SchedulingConstraints{
					QueueName: fr.qName,
				},
			},
			CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State: parseWorkUnitStateEnum(fr.state),
					SchedulingInformation: &essmsgpb.StructuredMessage{
						AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
							Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{
								QueueInfo: &essmsgpb.QueueInfo{
									PositionInQueue: int32(posInQueue),
								},
							},
						},
					},
				},
			},
			Notes: fr.creator,
		}
		workUnits = append(workUnits, wu)
	}

	return workUnits, "", nil
}

// parseWorkUnitStateEnum converts a status string to the corresponding WorkUnitState enum.
func parseWorkUnitStateEnum(state string) essmsgpb.WorkUnitStatus_WorkUnitState {
	switch state {
	case "INITIALIZING":
		return essmsgpb.WorkUnitStatus_INITIALIZING
	case "SCHEDULING":
		return essmsgpb.WorkUnitStatus_SCHEDULING
	case "STARTING":
		return essmsgpb.WorkUnitStatus_STARTING
	case "RUNNING":
		return essmsgpb.WorkUnitStatus_RUNNING
	case "PARTIALLY_FAILED":
		return essmsgpb.WorkUnitStatus_PARTIALLY_FAILED
	case "STOPPING":
		return essmsgpb.WorkUnitStatus_STOPPING
	case "RESTARTING":
		return essmsgpb.WorkUnitStatus_RESTARTING
	case "STOPPED":
		return essmsgpb.WorkUnitStatus_STOPPED
	case "DESCHEDULING":
		return essmsgpb.WorkUnitStatus_DESCHEDULING
	case "COMPLETED":
		return essmsgpb.WorkUnitStatus_COMPLETED
	case "FAILED":
		return essmsgpb.WorkUnitStatus_FAILED
	default:
		return essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED
	}
}
