// Package spannerdata provides translation layer functions.
package spannerdata

import (
	"fmt"

	"golang/protobuf/v2/proto/proto"

	timestamppb "protobuf/timestamp_go_proto"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/schema/xidschema"
	"xmanager_cloud/validator/validator"
	pb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// XIDRowToProto converts a Spanner Row containing Xids table data into an XidEntry proto message.
func XIDRowToProto(row *spanner.Row) (*pb.XidEntry, error) {
	entry := &pb.XidEntry{}

	var xid int64
	if err := row.ColumnByName("Xid", &xid); err != nil {
		return nil, fmt.Errorf("failed to read column Xid: %w", err)
	}
	entry.Name = proto.String(validator.ConstructXIDName(xid))

	var parentXID int64
	if err := row.ColumnByName("ParentXid", &parentXID); err != nil {
		return nil, fmt.Errorf("failed to read column ParentXid: %w", err)
	}
	if parentXID != 0 {
		entry.ParentXid = proto.String(validator.ConstructXIDName(parentXID))
	}

	if err := row.ColumnByName("Creator", &entry.Creator); err != nil {
		return nil, fmt.Errorf("failed to read column Creator: %w", err)
	}
	if err := row.ColumnByName("OriginatingSystem", &entry.Origin); err != nil {
		return nil, fmt.Errorf("failed to read column OriginatingSystem: %w", err)
	}
	if err := row.ColumnByName("Acls", &entry.Acls); err != nil {
		return nil, fmt.Errorf("failed to read column Acls: %w", err)
	}
	var createTime, updateTime spanner.NullTime
	if err := row.ColumnByName("CreateTime", &createTime); err != nil {
		return nil, fmt.Errorf("failed to read column CreateTime: %w", err)
	}
	if createTime.Valid {
		entry.CreateTime = timestamppb.New(createTime.Time)
	}
	if err := row.ColumnByName("UpdateTime", &updateTime); err != nil {
		return nil, fmt.Errorf("failed to read column UpdateTime: %w", err)
	}
	if updateTime.Valid {
		entry.UpdateTime = timestamppb.New(updateTime.Time)
	}

	return entry, nil
}

// XIDProtoToParamsMap converts an XidEntry proto message into a map suitable for Spanner mutations (INSERT/UPDATE).
func XIDProtoToParamsMap(entry *pb.XidEntry) (map[string]any, error) {
	parentXID := int64(0)
	if entry.GetParentXid() != "" {
		var err error
		parentXID, err = validator.ValidateXIDName(entry.GetParentXid())
		if err != nil {
			return nil, fmt.Errorf("failed to parse parent XID: %w", err)
		}
	}

	m := map[string]any{
		"Creator":           entry.Creator,
		"OriginatingSystem": entry.Origin,
		"Acls":              entry.Acls,
		"ParentXid":         parentXID,
	}

	if entry.CreateTime != nil {
		m["CreateTime"] = entry.CreateTime.AsTime()
	} else {
		m["CreateTime"] = spanner.NullTime{}
	}

	if entry.UpdateTime != nil {
		m["UpdateTime"] = entry.UpdateTime.AsTime()
	} else {
		m["UpdateTime"] = spanner.NullTime{}
	}

	return m, nil
}

// XIDEntryToMutation converts an XidEntry proto message into a Spanner mutation for updating the
// Xids table.
func XIDEntryToMutation(xid int64, entry *pb.XidEntry) *spanner.Mutation {
	return spanner.Update(
		xidschema.XIDTableName,
		[]string{
			"BitReversedXid",
			"Acls",
			"UpdateTime",
		},
		[]any{
			data.ReverseBits(xid),
			entry.Acls,
			entry.UpdateTime.AsTime(),
		},
	)
}
