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
	"strconv"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/protobuf/proto"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/schema/xidschema"
	"xmanager_cloud/internal/storage/spanner/utils"

	spannerdialect "xmanager_cloud/internal/query_generator/dialect/spanner"
	"xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"

	fpb "xmanager_cloud/api/filter"
	xidpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
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
	stmt := spannerdialect.NewStatement(nil)
	if err := liststatement.ListStatement(ctx, stmt, listParams); err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	// 2. Execute the query.
	xidIter := r.client.Single().WithTimestampBound(spanner.ExactStaleness(staleness)).Query(ctx, *stmt.Spanner())
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

// RewriteXidRestriction rewrites "xid = V" and "xid != V" to the bit-reversed primary key
// column so Spanner performs key lookups instead of scanning the unindexed Xid column.
// Range comparisons stay on Xid because bit reversal does not preserve ordering.
func RewriteXidRestriction(r *fpb.Restriction) (*fpb.Restriction, error) {
	if r.GetLhs().GetValue() != "xid" {
		return r, nil
	}
	v, err := strconv.ParseInt(r.GetRhs().GetValue(), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid integer literal for field \"xid\": %w", grpcerr.ErrInvalidArgument)
	}
	if c := r.GetComparator(); c != fpb.Restriction_EQUALS && c != fpb.Restriction_NOT_EQUALS {
		return r, nil
	}
	return fpb.Restriction_builder{
		Comparator: r.GetComparator(),
		Lhs:        fpb.Expression_builder{Value: proto.String("bit_reversed_xid")}.Build(),
		Rhs:        fpb.Expression_builder{Value: proto.String(strconv.FormatInt(spannerutils.ReverseBits(v), 10))}.Build(),
	}.Build(), nil
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
