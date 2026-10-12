// Copyright 2026 Google LLC
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

package workunit

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/logging"
	ls "xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	"xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/schema/workunitschema"
	"xmanager_cloud/internal/storage/spanner/utils"
	"xmanager_cloud/internal/validator"
)

// ListWorkUnits returns a paginated list of WorkUnit protos based on the provided filter and order.
func (r *Repository) ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error) {
	ctx = logging.WithExperimentName(ctx, parent)
	logging.Info(ctx, "Fetch work units", "parent", parent, "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy, "staleness", staleness)

	// 1. Ensure the results are filtered to a single XID.
	xid, err := validator.ValidateExperimentName(parent)
	if err != nil {
		return nil, "", fmt.Errorf("failed to validate experiment name: %w", err)
	}
	bitReversedXid := spannerutils.ReverseBits(xid)
	if filter == "" {
		filter = fmt.Sprintf("bit_reversed_xid = %d", bitReversedXid)
	} else {
		filter = fmt.Sprintf("bit_reversed_xid = %d AND (%s)", bitReversedXid, filter)
	}

	normalizedFilter, err := normalizer.Normalize(filter, r.workUnitsQueryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	// 2.1. Generate the list statement.
	listParams := ls.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              r.workUnitsQueryGen,
		DefaultSelectFields:   workunitschema.WorkUnitSelectFields,
		DefaultFullTextFields: workunitschema.WorkUnitFullTextFields,
		ValidOrderByFields:    workunitschema.WorkUnitOrderByFields,
	}

	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", err
	}

	stmt, err := ls.ListStatement(ctx, listParams)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	logging.Debug(ctx, "Generated list statement", "stmt", stmt)

	// 3. Execute the query.
	wuIter := r.client.Single().WithTimestampBound(spanner.ExactStaleness(staleness)).Query(ctx, *stmt)
	defer wuIter.Stop()

	// 4. Convert the rows to WorkUnit protos.
	var workUnits []*essmsgpb.WorkUnit
	if err := wuIter.Do(func(row *spanner.Row) error {
		exp, err := spannerdata.WorkUnitRowToProto(row)
		if err != nil {
			logging.Error(ctx, "failed to convert WorkUnit row to WorkUnit", "error", err)
			return err
		}
		workUnits = append(workUnits, exp)
		return nil
	}); err != nil {
		logging.Error(ctx, "failed to read WorkUnit rows", "error", err)
		return nil, "", fmt.Errorf("failed to read WorkUnit rows: %w", err)
	}

	// 5. If there are more results than the page size, return the next page token.
	workUnits, nextPageToken, err := ls.ApplyPagination(workUnits, pageSize, ExtractWorkUnitCursor)
	if err != nil {
		return nil, "", err
	}

	logging.Info(ctx, "Fetched work units", "count", len(workUnits), "nextPageToken", nextPageToken)

	// 6. Return the results.
	return workUnits, nextPageToken, nil
}

// ExtractWorkUnitCursor extracts pagination cursor values from a WorkUnit proto.
func ExtractWorkUnitCursor(wu *essmsgpb.WorkUnit) (map[string]any, error) {
	xid, wid, err := validator.ValidateWorkUnitName(wu.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate work unit name %q: %w", wu.GetName(), err)
	}

	cursor := map[string]any{
		"xid":              xid,
		"bit_reversed_xid": spannerutils.ReverseBits(xid),
		"work_unit_id":     wid,
	}

	if wu.CreateTime != nil {
		cursor["created"] = wu.CreateTime.AsTime()
	}
	if wu.UpdateTime != nil {
		cursor["updated"] = wu.UpdateTime.AsTime()
	}

	return cursor, nil
}
