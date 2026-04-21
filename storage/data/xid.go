package data

import (
	"fmt"

	"golang/protobuf/v2/proto/proto"
	"task/go/status"

	fieldmaskpb "protobuf/field_mask_go_proto"
	pb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// MergeXIDEntries merges an updated XidEntry proto message into a current XidEntry proto message
// based on an update mask.
func MergeXIDEntries(current, updated *pb.XidEntry, updateMask *fieldmaskpb.FieldMask) (*pb.XidEntry, error) {
	if len(updateMask.Paths) == 0 {
		return nil, fmt.Errorf("update mask is empty: %w", status.ErrInvalidArgument)
	}

	merged := proto.Clone(current).(*pb.XidEntry)
	for _, path := range updateMask.GetPaths() {
		switch path {
		case "acls":
			if updated.Acls == nil {
				return nil, fmt.Errorf("acls field is required: %w", status.ErrInvalidArgument)
			}
			merged.Acls = updated.Acls
		default:
			return nil, fmt.Errorf("field %q is not updateable: %w", path, status.ErrInvalidArgument)
		}
	}
	return merged, nil
}
