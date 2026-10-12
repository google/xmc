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

	lropb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/google/xmc/internal/grpcerr"
	"google.golang.org/protobuf/proto"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
)

// MergeOperations merges an updated Operation proto message into a current Operation proto
// message based on an update mask.
func MergeOperations(current, updated *lropb.Operation, updateMask *fieldmaskpb.FieldMask) (*lropb.Operation, error) {
	if current == nil {
		return nil, fmt.Errorf("current operation is nil: %w", grpcerr.ErrInvalidArgument)
	}
	if updated == nil {
		return nil, fmt.Errorf("updated operation is nil: %w", grpcerr.ErrInvalidArgument)
	}

	if len(updateMask.Paths) == 0 {
		return nil, fmt.Errorf("update mask is empty: %w", grpcerr.ErrInvalidArgument)
	}

	merged := proto.Clone(current).(*lropb.Operation)
	for _, path := range updateMask.GetPaths() {
		switch path {
		case "done":
			merged.Done = updated.Done
		case "metadata":
			if updated.Metadata == nil {
				return nil, fmt.Errorf("metadata field cannot be nil: %w", grpcerr.ErrInvalidArgument)
			}
			merged.Metadata = updated.Metadata
		case "error":
			if updated.GetError() == nil {
				return nil, fmt.Errorf("error field cannot be nil: %w", grpcerr.ErrInvalidArgument)
			}
			merged.Result = &lropb.Operation_Error{Error: updated.GetError()}
		case "response":
			if updated.GetResponse() == nil {
				return nil, fmt.Errorf("response field cannot be nil: %w", grpcerr.ErrInvalidArgument)
			}
			merged.Result = &lropb.Operation_Response{Response: updated.GetResponse()}
		default:
			return nil, fmt.Errorf("field %q is not updateable: %w", path, grpcerr.ErrInvalidArgument)
		}
	}
	return merged, nil
}
