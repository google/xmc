// Copyright 2025 Google LLC
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
package data

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"task/go/status"

	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
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
