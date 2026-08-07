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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	ls "xmanager_cloud/query_generator/liststatement"
	"xmanager_cloud/query_generator/normalizer/normalizer"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/workunitschema"
)

// FetchActiveWorkUnitsByQueue queries Spanner for active and pending work units assigned to the given queue.
func (r *Repository) FetchActiveWorkUnitsByQueue(ctx context.Context, queueName string, pageSize int32, pageToken string) ([]*essmsgpb.WorkUnit, string, error) {
	if queueName == "" {
		return nil, "", fmt.Errorf("queueName cannot be empty")
	}

	filter := fmt.Sprintf("queue = %q AND state IN (\"INITIALIZING\", \"SCHEDULING\", \"STARTING\", \"RUNNING\")", queueName)

	normalizedFilter, err := normalizer.Normalize(filter, r.workUnitsQueryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	listParams := ls.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               "created ASC",
		QueryGen:              r.workUnitsQueryGen,
		DefaultSelectFields:   workunitschema.WorkUnitSelectFields,
		DefaultFullTextFields: workunitschema.WorkUnitFullTextFields,
		ValidOrderByFields:    workunitschema.WorkUnitOrderByFields,
	}

	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", fmt.Errorf("invalid page token: %w", err)
	}

	stmt, err := ls.ListStatement(ctx, listParams)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	logging.Debug(ctx, "Generated list statement", "stmt", stmt)

	wuIter := r.client.Single().Query(ctx, *stmt)
	defer wuIter.Stop()

	var workUnits []*essmsgpb.WorkUnit
	if err := wuIter.Do(func(row *spanner.Row) error {
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

	workUnits, nextPageToken, err := ls.ApplyPagination(workUnits, pageSize, ExtractWorkUnitCursor)
	if err != nil {
		return nil, "", err
	}

	return workUnits, nextPageToken, nil
}
