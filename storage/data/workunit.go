package data

import (
	"fmt"

	fieldmaskpb "protobuf/field_mask_go_proto"
	"golang/protobuf/v2/proto/proto"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/validator/validator"
	"task/go/status"
)

// MergeWorkUnits merges an updated WorkUnit proto message into a current WorkUnit proto
// message based on an update mask.
func MergeWorkUnits(current, updated *pb.WorkUnit, updateMask *fieldmaskpb.FieldMask) (*pb.WorkUnit, error) {
	if current == nil {
		return nil, fmt.Errorf("current work unit is nil: %w", status.ErrInvalidArgument)
	}
	if updated == nil {
		return nil, fmt.Errorf("updated work unit is nil: %w", status.ErrInvalidArgument)
	}

	if len(updateMask.Paths) == 0 {
		return nil, fmt.Errorf("update mask is empty: %w", status.ErrInvalidArgument)
	}

	merged := proto.Clone(current).(*pb.WorkUnit)
	for _, path := range updateMask.GetPaths() {
		switch path {
		case "priority":
			merged.Priority = updated.Priority
		case "urls":
			if updated.Urls == nil {
				return nil, fmt.Errorf("urls field cannot be nil: %w", status.ErrInvalidArgument)
			}
			merged.Urls = updated.Urls
		case "tags":
			if updated.Tags == nil {
				return nil, fmt.Errorf("tags field cannot be nil: %w", status.ErrInvalidArgument)
			}
			merged.Tags = updated.Tags
		case "notes":
			merged.Notes = updated.Notes
		default:
			return nil, fmt.Errorf("field %q is not updateable: %w", path, status.ErrInvalidArgument)
		}
	}
	return validator.FillWorkUnitNullValues(merged), nil
}
