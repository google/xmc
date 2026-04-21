package spanner

import (
	"context"
	"fmt"
	"time"

	timestamppb "protobuf/timestamp_go_proto"
	"googlesql/safesql/go/safesql"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/logging/logging"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/validator/validator"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// We cannot use Mutations here, because we need to return the XID. Mutations do not support THEN
// RETURN.
var createXidSQL = safesql.New(`
INSERT INTO Xids (
  ParentXid,
	Creator,
	OriginatingSystem,
	Acls,
  CreateTime,
  UpdateTime
)
VALUES (
	@ParentXid,
	@Creator,
	@OriginatingSystem,
	@Acls,
	@CreateTime,
	@UpdateTime
)
THEN RETURN Xid
`)

// CreateXidEntry creates a new XidEntry in storage.
func (s *Storage) CreateXidEntry(ctx context.Context, entry *xidpb.XidEntry) (*xidpb.XidEntry, error) {
	// This needs to be in a transaction so we can ensure the time is consistent.
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		now := timestamppb.New(time.Now())
		entry.CreateTime = now
		entry.UpdateTime = now

		params, err := spannerdata.XIDProtoToParamsMap(entry)
		if err != nil {
			return err
		}

		stmt := spanner.Statement{
			SQL:    createXidSQL.String(),
			Params: params,
		}

		iter := tx.Query(ctx, stmt)
		if err := iter.Do(func(r *spanner.Row) error {
			var xid int64
			if err := r.ColumnByName("Xid", &xid); err != nil {
				return fmt.Errorf("failed to get XID from row: %v", r)
			}

			name := validator.ConstructXIDName(xid)
			entry.Name = &name

			return nil
		}); err != nil {
			logging.Error(ctx, "failed to execute create XID statement", "error", err)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return entry, nil
}
