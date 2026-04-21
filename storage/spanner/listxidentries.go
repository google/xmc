package spanner

import (
	"context"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/xidschema"

	"xmanager_cloud/query_generator/liststatement"
	"xmanager_cloud/query_generator/normalizer/normalizer"

	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// ListXidEntries returns a paginated list of XidEntry protos based on the provided filter and order.
func (s *Storage) ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration, defaultToCursorPagination bool) ([]*xidpb.XidEntry, string, error) {
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

	isCursor, err := listParams.DecodePageToken(pageToken, defaultToCursorPagination)
	if err != nil {
		return nil, "", err
	}

	stmt, pageOffset, err := liststatement.ListStatement(ctx, listParams)
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
	xidEntries, nextPageToken, err := liststatement.ApplyPagination(xidEntries, pageSize, pageOffset, isCursor, extractXidEntryCursor)
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
