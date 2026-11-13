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
package spannerdata

import (
	"fmt"

	"cloud.google.com/go/spanner"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/spanner/schema/artifactschema"
	"xmanager_cloud/internal/validator"
)

// ArtifactRowToProto converts a Spanner Row containing Artifacts table data into an Artifact proto message.
func ArtifactRowToProto(row *spanner.Row) (*pb.Artifact, error) {
	urlMsg := &pb.Url{}
	artifact := &pb.Artifact{Url: urlMsg}

	var xid, aid, priority int64
	var wid spanner.NullInt64
	err := readColumns(row,
		colDest{"Xid", &xid},
		colDest{"WorkUnitId", &wid},
		colDest{"ArtifactId", &aid},
		colDest{"Url", &urlMsg.Url},
		colDest{"Icon", &urlMsg.Icon},
		colDest{"DisplayName", &urlMsg.DisplayName},
		colDest{"Priority", &priority},
		colDest{"Tags", &artifact.Tags},
		colDest{"Notes", &artifact.Notes},
		colDest{"LifecyclePhase", &artifact.LifecyclePhase},
		colDest{"DataType", &artifact.DataType},
		colDest{"MimeType", &artifact.MimeType},
	)
	if err != nil {
		return nil, err
	}
	if wid.Valid {
		artifact.Parent = validator.ConstructWorkUnitName(xid, wid.Int64)
	} else {
		artifact.Parent = validator.ConstructExperimentName(xid)
	}
	artifact.Name = validator.ConstructArtifactName(aid)
	artifact.Priority, urlMsg.Priority = int32(priority), int32(priority)

	if artifact.AdditionalInfo, err = readNullProto(row, "AdditionalInfo", &pb.Artifact_AdditionalInfo{}); err != nil {
		return nil, err
	}
	if artifact.Payload, err = readNullProto(row, "Payload", &pb.ArtifactPayload{}); err != nil {
		return nil, err
	}
	if err := readNullTimestamps(row, tsDest{"CreateTime", &artifact.CreateTime}, tsDest{"UpdateTime", &artifact.UpdateTime}); err != nil {
		return nil, err
	}
	return artifact, nil
}

// ArtifactProtoToInsertMutation creates a Spanner mutation to insert an Artifact.
func ArtifactProtoToInsertMutation(art *pb.Artifact) (*spanner.Mutation, error) {
	aid, err := validator.ValidateArtifactName(art.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate artifact name %q: %w", art.GetName(), err)
	}

	xid, wid, err := validator.ValidateArtifactParent(art.GetParent())
	if err != nil {
		return nil, fmt.Errorf("failed to validate artifact parent %q: %w", art.GetParent(), err)
	}
	workUnitID := spanner.NullInt64{Int64: wid, Valid: wid != 0}

	return spanner.Insert(
		artifactschema.ArtifactTableName,
		artifactschema.ArtifactColumns,
		[]any{
			xid,                        // Xid
			workUnitID,                 // WorkUnitId
			aid,                        // ArtifactId
			art.Url.Url,                // Url
			art.Url.Icon,               // Icon
			art.Url.DisplayName,        // DisplayName
			data.ArtifactPriority(art), // Priority
			data.ArtifactTags(art),     // Tags
			art.Notes,                  // Notes
			art.LifecyclePhase,         // LifecyclePhase
			art.DataType,               // DataType
			art.MimeType,               // MimeType
			art.AdditionalInfo,         // AdditionalInfo
			art.Payload,                // Payload
			data.ArtifactSearchableContent(art.Payload), // SearchableContent
			art.CreateTime.AsTime(),                     // CreateTime
			art.UpdateTime.AsTime(),                     // UpdateTime
		},
	), nil
}

// ArtifactProtoToUpdateMutation creates a Spanner mutation to update an Artifact.
func ArtifactProtoToUpdateMutation(art *pb.Artifact) (*spanner.Mutation, error) {
	aid, err := validator.ValidateArtifactName(art.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate artifact name %q: %w", art.GetName(), err)
	}

	xid, wid, err := validator.ValidateArtifactParent(art.GetParent())
	if err != nil {
		return nil, fmt.Errorf("failed to validate artifact parent %q: %w", art.GetParent(), err)
	}
	workUnitID := spanner.NullInt64{Int64: wid, Valid: wid != 0}

	return spanner.Update(
		artifactschema.ArtifactTableName,
		artifactschema.ArtifactUpdateColumns,
		[]any{
			data.ReverseBits(xid),      // BitReversedXid
			workUnitID,                 // WorkUnitId
			aid,                        // ArtifactId
			art.Url.Url,                // Url
			art.Url.Icon,               // Icon
			art.Url.DisplayName,        // DisplayName
			data.ArtifactPriority(art), // Priority
			data.ArtifactTags(art),     // Tags
			art.Notes,                  // Notes
			art.LifecyclePhase,         // LifecyclePhase
			art.DataType,               // DataType
			art.MimeType,               // MimeType
			art.AdditionalInfo,         // AdditionalInfo
			art.Payload,                // Payload
			data.ArtifactSearchableContent(art.Payload), // SearchableContent
			art.UpdateTime.AsTime(),                     // UpdateTime
		},
	), nil
}
