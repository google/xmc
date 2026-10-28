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

package xid

import (
	"context"
	"fmt"
	"time"

	xidpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/logging"
	postgresdialect "xmanager_cloud/internal/query_generator/dialect/postgres"
	"xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/storage/postgres/schema/xidschema"
	"xmanager_cloud/internal/validator"
)

// ListXidEntries returns a page of XidEntry protos matching the AIP-160 filter, ordered by orderBy
// (a comma-separated list of xidschema.XidsOrderByFields, each optionally followed by "desc") and
// then by the default ordering of the Xids table view, together with the token of the next page.
// PostgreSQL has no bounded-staleness reads, so staleness is ignored and every read is strong.
func (r *Repository) ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*xidpb.XidEntry, string, error) {
	if r.db == nil {
		return nil, "", errNilPool()
	}
	queryGen, err := xidsQueryGen()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create Xids query generator: %w", err)
	}

	// 1. Normalize the filter.
	normalizedFilter, err := normalizer.Normalize(filter, queryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	// 1.1. Generate the list statement.
	listParams := liststatement.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              queryGen,
		DefaultSelectFields:   xidschema.XidsSelectFields,
		DefaultFullTextFields: xidschema.XidsFullTextFields,
		ValidOrderByFields:    xidschema.XidsOrderByFields,
	}
	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", err
	}

	// Generate the statement with pagination.
	stmt := postgresdialect.NewStatement("")
	if err := liststatement.ListStatement(ctx, stmt, listParams); err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	// 2. Execute the query and convert the rows to XidEntry protos.
	rows, err := r.db.Query(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		logging.Error(ctx, "failed to query Xid rows", "error", err)
		return nil, "", pgdb.TranslateError(err, entityName)
	}
	xidEntries, err := scanXidEntries(rows)
	if err != nil {
		logging.Error(ctx, "failed to read Xid rows", "error", err)
		return nil, "", pgdb.TranslateError(err, entityName)
	}

	// 3. If there are more results than the page size, return the next page token.
	return liststatement.ApplyPagination(xidEntries, pageSize, ExtractXidEntryCursor)
}

// ExtractXidEntryCursor extracts the pagination cursor of entry: the values of every field that
// ListXidEntries may order by, keyed by the field names of the Xids table view.
func ExtractXidEntryCursor(entry *xidpb.XidEntry) (map[string]any, error) {
	xid, err := validator.ValidateXIDName(entry.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate XID name %q: %w", entry.GetName(), err)
	}

	cursor := map[string]any{"xid": xid}
	if entry.GetCreateTime() != nil {
		cursor["created"] = entry.GetCreateTime().AsTime()
	}
	if entry.GetUpdateTime() != nil {
		cursor["updated"] = entry.GetUpdateTime().AsTime()
	}
	return cursor, nil
}
