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

	"cloud.google.com/go/spanner"
	"googlesql/safesql/go/safesql"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
)

const (
	defaultPageSize = int32(100)
	maxPageSize     = int32(1000)
)

var fetchActiveWorkUnitsByQueuesSQL = safesql.New(
	`SELECT Xid, WorkUnitId, WorkUnitType, Priority, Urls, Tags, Notes, Executable, ExecutionCreateTime, ExecutionUpdateTime, ExecutionCompleteTime, ExecutionId, ExecutionMetadata, WorkUnitStatus, QueueName, ExecutionState, ExecutionStatusMessageSeverity, CreateTime, UpdateTime
	FROM WorkUnits wu
	WHERE wu.QueueName IN UNNEST(@queueNames)
	  AND wu.IsPendingOrRunningJob IS NOT NULL
	ORDER BY wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC, wu.WorkUnitId ASC
	LIMIT @pageSize`,
)

// ActiveWorkUnitsResult holds the result of querying active work units.
type ActiveWorkUnitsResult struct {
	WorkUnits     []*essmsgpb.WorkUnit
	NextPageToken string
}

// FetchActiveWorkUnitsByQueues queries Spanner for active and pending work units assigned to the given queues.
func (r *Repository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) (*ActiveWorkUnitsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if r == nil || r.client == nil {
		return nil, fmt.Errorf("spanner storage client is uninitialized")
	}

	if len(queueNames) == 0 {
		return &ActiveWorkUnitsResult{
			WorkUnits:     []*essmsgpb.WorkUnit{},
			NextPageToken: "",
		}, nil
	}

	pageSizeVal := pageSize
	if pageSizeVal <= 0 {
		pageSizeVal = defaultPageSize
	} else if pageSizeVal > maxPageSize {
		pageSizeVal = maxPageSize
	}

	stmt := spanner.Statement{
		SQL: fetchActiveWorkUnitsByQueuesSQL.String(),
		Params: map[string]interface{}{
			"queueNames": queueNames,
			"pageSize":   int64(pageSizeVal),
		},
	}

	iter := r.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	var workUnits []*essmsgpb.WorkUnit
	if err := iter.Do(func(row *spanner.Row) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		wu, err := spannerdata.WorkUnitRowToProto(row)
		if err != nil {
			return fmt.Errorf("failed to convert WorkUnit row to WorkUnit: %w", err)
		}
		workUnits = append(workUnits, wu)
		return nil
	}); err != nil {
		logging.Error(ctx, "failed to read WorkUnit rows", "error", err)
		return nil, fmt.Errorf("failed to read WorkUnit rows: %w", err)
	}

	return &ActiveWorkUnitsResult{
		WorkUnits:     workUnits,
		NextPageToken: "",
	}, nil
}
