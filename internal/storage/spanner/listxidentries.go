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
package spanner

import (
	"context"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/xidschema"

	"xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"

	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// ListXidEntries returns a paginated list of XidEntry protos based on the provided filter and order.
func (s *Storage) ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*xidpb.XidEntry, string, error) {
	// 1. Normalize the filter.
	normalizedFilter, err := normalizer.Normalize(filter, s.xidsQueryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	// 1.1. Generate the list statement.
	listParams := liststatement.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              s.xidsQueryGen,
		DefaultSelectFields:   xidschema.XidsSelectFields,
		DefaultFullTextFields: xidschema.XidsFullTextFields,
		ValidOrderByFields:    xidschema.XidsOrderByFields,
	}

	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", err
	}

	// Generate the statement with pagination
	stmt, err := liststatement.ListStatement(ctx, listParams)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	// 2. Execute the query.
	xidIter := s.client.Single().WithTimestampBound(spanner.ExactStaleness(staleness)).Query(ctx, *stmt)
	defer xidIter.Stop()

	// 3. Convert the rows to XidEntry protos.
	var xidEntries []*xidpb.XidEntry
	if err := xidIter.Do(func(r *spanner.Row) error {
		xidEntry, err := spannerdata.XIDRowToProto(r)
		if err != nil {
			logging.Error(ctx, "failed to convert Xid row to XidEntry", "error", err)
			return err
		}

		xidEntries = append(xidEntries, xidEntry)

		return nil
	}); err != nil {
		logging.Error(ctx, "failed to read Xid rows", "error", err)
		return nil, "", fmt.Errorf("failed to read Xid rows: %w", err)
	}

	// 4. If there are more results than the page size, return the next page token.
	xidEntries, nextPageToken, err := liststatement.ApplyPagination(xidEntries, pageSize, extractXidEntryCursor)
	if err != nil {
		return nil, "", err
	}

	// 5. Return the results.
	return xidEntries, nextPageToken, nil
}

func extractXidEntryCursor(entry *xidpb.XidEntry) (map[string]any, error) {
	xid, err := validator.ValidateXIDName(entry.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate XID name %q: %w", entry.GetName(), err)
	}

	cursor := map[string]any{
		"xid": xid,
	}

	if entry.CreateTime != nil {
		cursor["created"] = entry.CreateTime.AsTime()
	}
	if entry.UpdateTime != nil {
		cursor["updated"] = entry.UpdateTime.AsTime()
	}

	return cursor, nil
}
