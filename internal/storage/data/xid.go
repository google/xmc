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

	pb "github.com/google/xmc/api/protos/xid"
	"github.com/google/xmc/internal/grpcerr"
	"google.golang.org/protobuf/proto"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
)

// MergeXIDEntries merges an updated XidEntry proto message into a current XidEntry proto message
// based on an update mask.
func MergeXIDEntries(current, updated *pb.XidEntry, updateMask *fieldmaskpb.FieldMask) (*pb.XidEntry, error) {
	if len(updateMask.Paths) == 0 {
		return nil, fmt.Errorf("update mask is empty: %w", grpcerr.ErrInvalidArgument)
	}

	merged := proto.Clone(current).(*pb.XidEntry)
	for _, path := range updateMask.GetPaths() {
		switch path {
		case "acls":
			if updated.Acls == nil {
				return nil, fmt.Errorf("acls field is required: %w", grpcerr.ErrInvalidArgument)
			}
			merged.Acls = updated.Acls
		default:
			return nil, fmt.Errorf("field %q is not updateable: %w", path, grpcerr.ErrInvalidArgument)
		}
	}
	return merged, nil
}
