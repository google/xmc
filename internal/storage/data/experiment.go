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
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// ExperimentRequiresXIDUpdate checks if an experiment update mask requires an XID update, and returns
// the masked updated XID entry if so.
func ExperimentRequiresXIDUpdate(exp *pb.Experiment, updateMask *fieldmaskpb.FieldMask) (*xidmsgpb.XidEntry, *fieldmaskpb.FieldMask, error) {
	if exp == nil {
		return nil, nil, fmt.Errorf("experiment is nil: %w", grpcerr.ErrInvalidArgument)
	}
	if updateMask == nil {
		return nil, nil, fmt.Errorf("update mask is nil: %w", grpcerr.ErrInvalidArgument)
	}
	if len(updateMask.Paths) == 0 {
		return nil, nil, fmt.Errorf("update mask is empty: %w", grpcerr.ErrInvalidArgument)
	}
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to validate experiment name %q: %w", exp.GetName(), err)
	}
	xidEntry := &xidmsgpb.XidEntry{
		Name: proto.String(validator.ConstructXIDName(xid)),
	}
	var paths []string
	for _, path := range updateMask.GetPaths() {
		if path == "acls" {
			xidEntry.Acls = exp.GetAcls()
			if len(xidEntry.GetAcls().GetOwners()) == 0 && len(xidEntry.GetAcls().GetWriters()) == 0 && len(xidEntry.GetAcls().GetReaders()) == 0 {
				return nil, nil, fmt.Errorf("at least one ACL must be provided: %w", grpcerr.ErrInvalidArgument)
			}
			paths = []string{"acls"}
		}
	}
	return xidEntry, &fieldmaskpb.FieldMask{Paths: paths}, nil
}

// MergeExperiments merges an updated Experiment proto message into a current Experiment proto
// message based on an update mask.
func MergeExperiments(current, updated *pb.Experiment, updateMask *fieldmaskpb.FieldMask) (*pb.Experiment, error) {
	if current == nil {
		return nil, fmt.Errorf("current experiment is nil: %w", grpcerr.ErrInvalidArgument)
	}
	if updated == nil {
		return nil, fmt.Errorf("updated experiment is nil: %w", grpcerr.ErrInvalidArgument)
	}

	if len(updateMask.Paths) == 0 {
		return nil, fmt.Errorf("update mask is empty: %w", grpcerr.ErrInvalidArgument)
	}

	merged := proto.Clone(current).(*pb.Experiment)
	for _, path := range updateMask.GetPaths() {
		switch path {
		case "acls":
			merged.Acls = updated.Acls
		case "title":
			if updated.Title == "" {
				return nil, fmt.Errorf("title field is required: %w", grpcerr.ErrInvalidArgument)
			}
			merged.Title = updated.Title
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
	return validator.FillExperimentNullValues(merged), nil
}
