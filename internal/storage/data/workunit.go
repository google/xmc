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
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/validator"
)

// MergeWorkUnits merges an updated WorkUnit proto message into a current WorkUnit proto
// message based on an update mask.
func MergeWorkUnits(current, updated *pb.WorkUnit, updateMask *fieldmaskpb.FieldMask) (*pb.WorkUnit, error) {
	if current == nil {
		return nil, fmt.Errorf("current work unit is nil: %w", grpcerr.ErrInvalidArgument)
	}
	if updated == nil {
		return nil, fmt.Errorf("updated work unit is nil: %w", grpcerr.ErrInvalidArgument)
	}

	if len(updateMask.Paths) == 0 {
		return nil, fmt.Errorf("update mask is empty: %w", grpcerr.ErrInvalidArgument)
	}

	merged := proto.Clone(current).(*pb.WorkUnit)
	for _, path := range updateMask.GetPaths() {
		switch path {
		case "priority":
			merged.Priority = updated.Priority
		case "urls":
			if updated.Urls == nil {
				return nil, fmt.Errorf("urls field cannot be nil: %w", grpcerr.ErrInvalidArgument)
			}
			merged.Urls = updated.Urls
		case "tags":
			if updated.Tags == nil {
				return nil, fmt.Errorf("tags field cannot be nil: %w", grpcerr.ErrInvalidArgument)
			}
			merged.Tags = updated.Tags
		case "notes":
			merged.Notes = updated.Notes
		default:
			return nil, fmt.Errorf("field %q is not updateable: %w", path, grpcerr.ErrInvalidArgument)
		}
	}
	return validator.FillWorkUnitNullValues(merged), nil
}
