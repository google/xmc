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
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/logging/logging"
)

// FetchActiveWorkUnitsByQueue queries Spanner for active and pending work units assigned to the given queue.
func (r *Repository) FetchActiveWorkUnitsByQueue(ctx context.Context, queueName string, pageSize int32, pageToken string) ([]*spb.JobInfo, string, error) {
	if queueName == "" {
		return nil, "", fmt.Errorf("queueName cannot be empty")
	}

	stmt := spanner.Statement{
		SQL: `SELECT wu.Xid, wu.WorkUnitId, wu.QueueName, e.Creator, wu.Priority, wu.ExecutionState, wu.CreateTime
			FROM WorkUnits wu
			JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid
			WHERE wu.QueueName = @queueName
			  AND wu.ExecutionState IN ('INITIALIZING', 'SCHEDULING', 'STARTING', 'RUNNING')
			ORDER BY wu.CreateTime ASC`,
		Params: map[string]interface{}{
			"queueName": queueName,
		},
	}

	iter := r.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	var jobs []*spb.JobInfo
	positionCounter := 1

	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			logging.Error(ctx, "failed to query active work units", "error", err)
			return nil, "", fmt.Errorf("failed to read active work unit rows: %w", err)
		}

		var xid, wid, priority int64
		var qName, creator, state string
		var createTime time.Time

		if err := row.Columns(&xid, &wid, &qName, &creator, &priority, &state, &createTime); err != nil {
			logging.Error(ctx, "failed to parse work unit row columns", "error", err)
			return nil, "", fmt.Errorf("failed to parse work unit row columns: %w", err)
		}

		posStr := "N/A-running"
		if state == "INITIALIZING" || state == "SCHEDULING" {
			posStr = strconv.Itoa(positionCounter)
			positionCounter++
		}

		jobInfo := &spb.JobInfo{
			Position:     posStr,
			Xid:          strconv.FormatInt(xid, 10),
			WorkUnitId:   strconv.FormatInt(wid, 10),
			JobName:      fmt.Sprintf("xmc-operation-%d-%d", xid, wid),
			User:         creator,
			Resources:    "CPU: 1",
			Priority:     FormatPriority(priority),
			Status:       state,
			TimeInQueue:  FormatTimeInQueue(createTime),
			QueueName:    qName,
			CreationTime: createTime.Format(time.RFC3339),
		}
		jobs = append(jobs, jobInfo)
	}

	return jobs, "", nil
}

// FormatPriority formats integer priority to a human-readable display string.
func FormatPriority(prio int64) string {
	switch {
	case prio >= 200:
		return "High"
	case prio <= 50:
		return "Low"
	default:
		return "Normal"
	}
}

// FormatTimeInQueue calculates human-readable duration string since creation.
func FormatTimeInQueue(t time.Time) string {
	duration := time.Since(t)
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
