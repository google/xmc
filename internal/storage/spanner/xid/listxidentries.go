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

package xid

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	spannerdata "github.com/google/xmc/internal/storage/spanner/data"
	"github.com/google/xmc/internal/storage/spanner/schema/xidschema"
	"github.com/google/xmc/internal/storage/spanner/utils"

	"github.com/google/xmc/internal/query_generator/liststatement"
	"github.com/google/xmc/internal/query_generator/normalizer"

	xidpb "github.com/google/xmc/api/protos/xid"
	"github.com/google/xmc/internal/logging"
	"github.com/google/xmc/internal/validator"
)

// ListXidEntries returns a paginated list of XidEntry protos based on the provided filter and order.
func (r *Repository) ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*xidpb.XidEntry, string, error) {
	// 1. Normalize the filter.
	normalizedFilter, err := normalizer.Normalize(filter, r.xidsQueryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	// 1.1. Generate the list statement.
	listParams := liststatement.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              r.xidsQueryGen,
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
	xidIter := r.client.Single().WithTimestampBound(spanner.ExactStaleness(staleness)).Query(ctx, *stmt)
	defer xidIter.Stop()

	// 3. Convert the rows to XidEntry protos.
	var xidEntries []*xidpb.XidEntry
	if err := xidIter.Do(func(row *spanner.Row) error {
		xidEntry, err := spannerdata.XIDRowToProto(row)
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
	xidEntries, nextPageToken, err := liststatement.ApplyPagination(xidEntries, pageSize, ExtractXidEntryCursor)
	if err != nil {
		return nil, "", err
	}

	// 5. Return the results.
	return xidEntries, nextPageToken, nil
}

// ExtractXidEntryCursor extracts pagination cursor values from an XidEntry proto.
func ExtractXidEntryCursor(entry *xidpb.XidEntry) (map[string]any, error) {
	xid, err := validator.ValidateXIDName(entry.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate XID name %q: %w", entry.GetName(), err)
	}

	cursor := map[string]any{
		"xid":              xid,
		"bit_reversed_xid": spannerutils.ReverseBits(xid),
	}

	if entry.CreateTime != nil {
		cursor["created"] = entry.CreateTime.AsTime()
	}
	if entry.UpdateTime != nil {
		cursor["updated"] = entry.UpdateTime.AsTime()
	}

	return cursor, nil
}
