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
	"strings"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/workunitschema"
)

// FetchActiveWorkUnitsByQueues queries Spanner for active and pending work units assigned to the given queues.
//
//nolint:revive // standard repository pattern returning (results, pageToken, err)
func (r *Repository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) ([]*essmsgpb.WorkUnit, string, error) {
	return r.FetchActiveWorkUnitsByQueuesAt(ctx, queueNames, pageSize, pageToken, time.Now())
}

// FetchActiveWorkUnitsByQueuesAt queries Spanner using deterministic reference time now.
//
//nolint:revive // standard repository pattern returning (results, pageToken, err)
func (r *Repository) FetchActiveWorkUnitsByQueuesAt(ctx context.Context, queueNames []string, pageSize int32, pageToken string, now time.Time) ([]*essmsgpb.WorkUnit, string, error) {
	if len(queueNames) == 0 {
		return []*essmsgpb.WorkUnit{}, "", nil
	}

	// TODO(#186): Implement cursor-based pagination using page tokens when job list volume increases.
	sql := `SELECT ` + strings.Join(workunitschema.WorkUnitColumns, ", ") + `
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

	var workUnits []*essmsgpb.WorkUnit
	if err := iter.Do(func(row *spanner.Row) error {
		wu, err := spannerdata.WorkUnitRowToProto(row)
		if err != nil {
			logging.Error(ctx, "failed to convert WorkUnit row to WorkUnit", "error", err)
			return err
		}
		workUnits = append(workUnits, wu)
		return nil
	}); err != nil {
		logging.Error(ctx, "failed to read WorkUnit rows", "error", err)
		return nil, "", fmt.Errorf("failed to read WorkUnit rows: %w", err)
	}

	return workUnits, "", nil
}
