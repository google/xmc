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

	"golang/protobuf/v2/proto/proto"
	fieldmaskpb "protobuf/field_mask_go_proto"
	"task/go/status"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/validator/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// ArtifactRequiresXIDUpdate checks if an artifact update mask requires an XID update, and returns
// the masked updated XID entry if so.
func ArtifactRequiresXIDUpdate(art *pb.Artifact, updateMask *fieldmaskpb.FieldMask) (*xidmsgpb.XidEntry, *fieldmaskpb.FieldMask, error) {
	if art == nil {
		return nil, nil, fmt.Errorf("artifact is nil: %w", status.ErrInvalidArgument)
	}
	if updateMask == nil {
		return nil, nil, fmt.Errorf("update mask is nil: %w", status.ErrInvalidArgument)
	}
	if len(updateMask.Paths) == 0 {
		return nil, nil, fmt.Errorf("update mask is empty: %w", status.ErrInvalidArgument)
	}
	aid, err := validator.ValidateArtifactName(art.GetName())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to validate artifact name %q: %w", art.GetName(), err)
	}
	xidEntry := &xidmsgpb.XidEntry{
		Name: proto.String(validator.ConstructXIDName(aid)),
	}
	var paths []string
	for _, path := range updateMask.GetPaths() {
		if path == "acls" {
			if art.GetAcls() == nil {
				return nil, nil, fmt.Errorf("acls field cannot be nil: %w", status.ErrInvalidArgument)
			}

			xidEntry.Acls = art.GetAcls()
			if len(xidEntry.GetAcls().GetOwners()) == 0 && len(xidEntry.GetAcls().GetWriters()) == 0 && len(xidEntry.GetAcls().GetReaders()) == 0 {
				return nil, nil, fmt.Errorf("at least one ACL must be provided: %w", status.ErrInvalidArgument)
			}
			paths = []string{"acls"}
		}
	}
	return xidEntry, &fieldmaskpb.FieldMask{Paths: paths}, nil
}

// MergeArtifacts merges an updated Artifact proto message into a current Artifact proto
// message based on an update mask.
func MergeArtifacts(current, updated *pb.Artifact, updateMask *fieldmaskpb.FieldMask) (*pb.Artifact, error) {
	if current == nil {
		return nil, fmt.Errorf("current artifact is nil: %w", status.ErrInvalidArgument)
	}
	if updated == nil {
		return nil, fmt.Errorf("updated artifact is nil: %w", status.ErrInvalidArgument)
	}

	if len(updateMask.Paths) == 0 {
		return nil, fmt.Errorf("update mask is empty: %w", status.ErrInvalidArgument)
	}

	merged := proto.Clone(current).(*pb.Artifact)
	for _, path := range updateMask.GetPaths() {
		switch path {
		case "acls":
			merged.Acls = updated.Acls
		case "url":
			if updated.Url == nil {
				return nil, fmt.Errorf("url field cannot be nil: %w", status.ErrInvalidArgument)
			}
			merged.Url = updated.Url
		case "tags":
			if updated.Tags == nil {
				return nil, fmt.Errorf("tags field cannot be nil: %w", status.ErrInvalidArgument)
			}
			merged.Tags = updated.Tags
		case "notes":
			merged.Notes = updated.Notes
		case "lifecycle_phase":
			merged.LifecyclePhase = updated.LifecyclePhase
		case "data_type":
			merged.DataType = updated.DataType
		case "mime_type":
			merged.MimeType = updated.MimeType
		case "additional_info":
			merged.AdditionalInfo = updated.AdditionalInfo
		case "payload":
			merged.Payload = updated.Payload
		default:
			return nil, fmt.Errorf("field %q is not updateable: %w", path, status.ErrInvalidArgument)
		}
	}
	return merged, nil
}
