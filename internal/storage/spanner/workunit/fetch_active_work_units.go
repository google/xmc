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
	"strconv"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/timestamppb"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/logging/logging"
)

const (
	defaultPageSize         int32 = 50
	maxPageSize             int32 = 100
	unassignedQueuePosition int64 = 9223372036854775807
)

// FetchActiveWorkUnitsByQueues queries Spanner for active and pending work units assigned to the given queues.
func (r *Repository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) ([]*spb.DashboardJob, string, error) {
	return r.FetchActiveWorkUnitsByQueuesAt(ctx, queueNames, pageSize, pageToken, time.Now())
}

// FetchActiveWorkUnitsByQueuesAt queries Spanner using deterministic reference time now.
func (r *Repository) FetchActiveWorkUnitsByQueuesAt(ctx context.Context, queueNames []string, pageSize int32, pageToken string, now time.Time) ([]*spb.DashboardJob, string, error) {
	if len(queueNames) == 0 {
		return []*spb.DashboardJob{}, "", nil
	}

	// TODO(#186): Implement cursor-based pagination using page tokens when job list volume increases.
	sql := `SELECT 
		wu.Xid, 
		wu.WorkUnitId, 
		wu.QueueName, 
		e.Creator, 
		wu.Priority, 
		CAST(wu.ExecutionState AS STRING), 
		wu.CreateTime, 
		wu.SortPhase, 
		wu.PositionInQueue
	FROM WorkUnits wu
	JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid
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

	var jobs []*spb.DashboardJob

	for _, fr := range rows {
		posInQueue := fr.positionInQueue
		if posInQueue == unassignedQueuePosition {
			posInQueue = -1
		}

		timeInQueueSec := int64(now.Sub(fr.createTime).Seconds())
		if timeInQueueSec < 0 {
			timeInQueueSec = 0
		}

		dashboardJob := &spb.DashboardJob{
			JobId:              fmt.Sprintf("xmc-operation-%d-%d", fr.xid, fr.wid),
			Xid:                strconv.FormatInt(fr.xid, 10),
			User:               fr.creator,
			Priority:           fr.priority,
			Status:             ParseWorkUnitState(fr.state),
			TimeInQueueSeconds: timeInQueueSec,
			PositionInQueue:    posInQueue,
			CreateTime:         timestamppb.New(fr.createTime),
			WorkUnitId:         fr.wid,
		}
		jobs = append(jobs, dashboardJob)
	}

	return jobs, "", nil
}

// ParseWorkUnitState converts a status string to the corresponding WorkUnitState enum.
func ParseWorkUnitState(state string) spb.WorkUnitState {
	switch state {
	case "INITIALIZING":
		return spb.WorkUnitState_INITIALIZING
	case "SCHEDULING":
		return spb.WorkUnitState_SCHEDULING
	case "STARTING":
		return spb.WorkUnitState_STARTING
	case "RUNNING":
		return spb.WorkUnitState_RUNNING
	case "SUCCEEDED":
		return spb.WorkUnitState_SUCCEEDED
	case "FAILED":
		return spb.WorkUnitState_FAILED
	case "CANCELLED":
		return spb.WorkUnitState_CANCELLED
	default:
		return spb.WorkUnitState_WORK_UNIT_STATE_UNSPECIFIED
	}
}

// FormatTimeInQueue calculates human-readable duration string since creation relative to reference time now.
func FormatTimeInQueue(t time.Time, now time.Time) string {
	duration := now.Sub(t)
	if duration < 0 {
		duration = 0
	}
	if duration < time.Minute {
		return "just now"
	}
	if duration < time.Hour {
		return fmt.Sprintf("%dm ago", int(duration.Minutes()))
	}
	if duration < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(duration.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(duration.Hours()/24))
}
