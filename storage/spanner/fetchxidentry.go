package spanner

import (
	"context"
	"fmt"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/xidschema"
	"xmanager_cloud/validator/validator"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
	"task/go/status"
)

// FetchXidEntry retrieves a single XidEntry from storage by its unique numerical ID.
func (s *Storage) FetchXidEntry(ctx context.Context, xid int64) (*xidpb.XidEntry, error) {
	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(xid))
	return s.fetchXidEntryTransaction(ctx, xid, s.client.Single())
}

func (s *Storage) fetchXidEntryTransaction(ctx context.Context, xid int64, txn spannerTransaction) (*xidpb.XidEntry, error) {
	logging.Info(ctx, "Fetch XID entry row")
	row, err := txn.ReadRow(ctx, xidschema.XIDTableName, spanner.Key{reverseBits(xid)}, xidschema.FetchedXIDColumns)
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			return nil, fmt.Errorf("XID %d not found: %w", xid, status.ErrNotFound)
		}

		logging.Error(ctx, "Failed to read Xid row", "error", err)
		return nil, fmt.Errorf("failed to read Xid row: %w", err)
	}

	xidEntry, err := spannerdata.XIDRowToProto(row)
	if err != nil {
		logging.Error(ctx, "Failed to extract XidEntry", "error", err)
		return nil, fmt.Errorf("failed to extract XidEntry: %w", err)
	}

	logging.Info(ctx, "Fetched XID entry from storage", "xid_entry", xidEntry)
	return xidEntry, nil
}
