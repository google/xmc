package data

import (
	"fmt"

	lropb "longrunning/operations_go_proto"
	fieldmaskpb "protobuf/field_mask_go_proto"
	"golang/protobuf/v2/proto/proto"
	"task/go/status"
)

// MergeOperations merges an updated Operation proto message into a current Operation proto
// message based on an update mask.
func MergeOperations(current, updated *lropb.Operation, updateMask *fieldmaskpb.FieldMask) (*lropb.Operation, error) {
	if current == nil {
		return nil, fmt.Errorf("current operation is nil: %w", status.ErrInvalidArgument)
	}
	if updated == nil {
		return nil, fmt.Errorf("updated operation is nil: %w", status.ErrInvalidArgument)
	}

	if len(updateMask.Paths) == 0 {
		return nil, fmt.Errorf("update mask is empty: %w", status.ErrInvalidArgument)
	}

	merged := proto.Clone(current).(*lropb.Operation)
	for _, path := range updateMask.GetPaths() {
		switch path {
		case "done":
			merged.Done = updated.Done
		case "metadata":
			if updated.Metadata == nil {
				return nil, fmt.Errorf("metadata field cannot be nil: %w", status.ErrInvalidArgument)
			}
			merged.Metadata = updated.Metadata
		case "error":
			if updated.GetError() == nil {
				return nil, fmt.Errorf("error field cannot be nil: %w", status.ErrInvalidArgument)
			}
			merged.Result = &lropb.Operation_Error{Error: updated.GetError()}
		case "response":
			if updated.GetResponse() == nil {
				return nil, fmt.Errorf("response field cannot be nil: %w", status.ErrInvalidArgument)
			}
			merged.Result = &lropb.Operation_Response{Response: updated.GetResponse()}
		default:
			return nil, fmt.Errorf("field %q is not updateable: %w", path, status.ErrInvalidArgument)
		}
	}
	return merged, nil
}
