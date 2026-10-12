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
	"strings"

	"cloud.google.com/go/spanner"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/spanner/schema/artifactschema"
	"xmanager_cloud/validator/validator"
)

// ArtifactRowToProto converts a Spanner Row containing Artifacts table data into an Artifact proto message.
func ArtifactRowToProto(row *spanner.Row) (*pb.Artifact, error) {
	artifact := &pb.Artifact{}
	artifact.Url = &pb.Url{}

	var xid int64
	if err := row.ColumnByName("Xid", &xid); err != nil {
		return nil, fmt.Errorf("failed to read column Xid: %w", err)
	}
	var wid spanner.NullInt64
	if err := row.ColumnByName("WorkUnitId", &wid); err != nil {
		return nil, fmt.Errorf("failed to read column WorkUnitId: %w", err)
	}
	if wid.Valid {
		artifact.Parent = validator.ConstructWorkUnitName(xid, wid.Int64)
	} else {
		artifact.Parent = validator.ConstructExperimentName(xid)
	}

	var aid int64
	if err := row.ColumnByName("ArtifactId", &aid); err != nil {
		return nil, fmt.Errorf("failed to read column ArtifactId: %w", err)
	}
	artifact.Name = validator.ConstructArtifactName(aid)

	if err := row.ColumnByName("Url", &artifact.Url.Url); err != nil {
		return nil, fmt.Errorf("failed to read column Url: %w", err)
	}
	if err := row.ColumnByName("Icon", &artifact.Url.Icon); err != nil {
		return nil, fmt.Errorf("failed to read column Icon: %w", err)
	}
	if err := row.ColumnByName("DisplayName", &artifact.Url.DisplayName); err != nil {
		return nil, fmt.Errorf("failed to read column DisplayName: %w", err)
	}
	var priority int64
	if err := row.ColumnByName("Priority", &priority); err != nil {
		return nil, fmt.Errorf("failed to read column Priority: %w", err)
	}
	artifact.Priority = int32(priority)
	artifact.Url.Priority = int32(priority)
	if err := row.ColumnByName("Tags", &artifact.Tags); err != nil {
		return nil, fmt.Errorf("failed to read column Tags: %w", err)
	}
	if err := row.ColumnByName("Notes", &artifact.Notes); err != nil {
		return nil, fmt.Errorf("failed to read column Notes: %w", err)
	}
	if err := row.ColumnByName("LifecyclePhase", &artifact.LifecyclePhase); err != nil {
		return nil, fmt.Errorf("failed to read column LifecyclePhase: %w", err)
	}
	if err := row.ColumnByName("DataType", &artifact.DataType); err != nil {
		return nil, fmt.Errorf("failed to read column DataType: %w", err)
	}
	if err := row.ColumnByName("MimeType", &artifact.MimeType); err != nil {
		return nil, fmt.Errorf("failed to read column MimeType: %w", err)
	}
	additionalInfo := spanner.NullProtoMessage{ProtoMessageVal: &pb.Artifact_AdditionalInfo{}}
	if err := row.ColumnByName("AdditionalInfo", &additionalInfo); err != nil {
		return nil, fmt.Errorf("failed to read column AdditionalInfo: %w", err)
	}
	if additionalInfo.Valid {
		artifact.AdditionalInfo = additionalInfo.ProtoMessageVal.(*pb.Artifact_AdditionalInfo)
	}

	payload := spanner.NullProtoMessage{ProtoMessageVal: &pb.ArtifactPayload{}}
	if err := row.ColumnByName("Payload", &payload); err != nil {
		return nil, fmt.Errorf("failed to read column Payload: %w", err)
	}
	if payload.Valid {
		artifact.Payload = payload.ProtoMessageVal.(*pb.ArtifactPayload)
	}

	// Time fields.
	var createTime, updateTime spanner.NullTime
	if err := row.ColumnByName("CreateTime", &createTime); err != nil {
		return nil, fmt.Errorf("failed to read column CreateTime: %w", err)
	}
	if createTime.Valid {
		artifact.CreateTime = timestamppb.New(createTime.Time)
	}

	if err := row.ColumnByName("UpdateTime", &updateTime); err != nil {
		return nil, fmt.Errorf("failed to read column UpdateTime: %w", err)
	}
	if updateTime.Valid {
		artifact.UpdateTime = timestamppb.New(updateTime.Time)
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
			xid,                               // Xid
			workUnitID,                        // WorkUnitId
			aid,                               // ArtifactId
			art.Url.Url,                       // Url
			art.Url.Icon,                      // Icon
			art.Url.DisplayName,               // DisplayName
			getPriority(art),                  // Priority
			getTags(art),                      // Tags
			art.Notes,                         // Notes
			art.LifecyclePhase,                // LifecyclePhase
			art.DataType,                      // DataType
			art.MimeType,                      // MimeType
			art.AdditionalInfo,                // AdditionalInfo
			art.Payload,                       // Payload
			getSearchableContent(art.Payload), // SearchableContent
			art.CreateTime.AsTime(),           // CreateTime
			art.UpdateTime.AsTime(),           // UpdateTime
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
			data.ReverseBits(xid),             // BitReversedXid
			workUnitID,                        // WorkUnitId
			aid,                               // ArtifactId
			art.Url.Url,                       // Url
			art.Url.Icon,                      // Icon
			art.Url.DisplayName,               // DisplayName
			getPriority(art),                  // Priority
			getTags(art),                      // Tags
			art.Notes,                         // Notes
			art.LifecyclePhase,                // LifecyclePhase
			art.DataType,                      // DataType
			art.MimeType,                      // MimeType
			art.AdditionalInfo,                // AdditionalInfo
			art.Payload,                       // Payload
			getSearchableContent(art.Payload), // SearchableContent
			art.UpdateTime.AsTime(),           // UpdateTime
		},
	), nil
}

func getSearchableContent(payload *pb.ArtifactPayload) string {
	if payload == nil {
		return ""
	}

	var parts []string

	switch p := payload.Type.(type) {
	case *pb.ArtifactPayload_ManagedXprof:
		if p.ManagedXprof == nil {
			break
		}
		if p.ManagedXprof.DisplayName != "" {
			parts = append(parts, p.ManagedXprof.DisplayName)
		}
		if p.ManagedXprof.RunGroup != "" {
			parts = append(parts, p.ManagedXprof.RunGroup)
		}
	case *pb.ArtifactPayload_CodeSource:
		if p.CodeSource == nil {
			break
		}
		if p.CodeSource.Title != "" {
			parts = append(parts, p.CodeSource.Title)
		}
		if p.CodeSource.Description != "" {
			parts = append(parts, p.CodeSource.Description)
		}
		if p.CodeSource.CodeBlock != "" {
			parts = append(parts, p.CodeSource.CodeBlock)
		}
		if p.CodeSource.CodeUrl != "" {
			parts = append(parts, p.CodeSource.CodeUrl)
		}
	case *pb.ArtifactPayload_Text:
		if p.Text == nil {
			break
		}
		if p.Text.Title != "" {
			parts = append(parts, p.Text.Title)
		}
		if p.Text.Text != "" {
			parts = append(parts, p.Text.Text)
		}
	case *pb.ArtifactPayload_Url:
		if p.Url == nil {
			break
		}
		if p.Url.Url != "" {
			parts = append(parts, p.Url.Url)
		}
		if p.Url.DisplayName != "" {
			parts = append(parts, p.Url.DisplayName)
		}
	case *pb.ArtifactPayload_Chart:
		if p.Chart == nil {
			break
		}
		if p.Chart.Title != "" {
			parts = append(parts, p.Chart.Title)
		}
		if len(p.Chart.MetricNames) > 0 {
			parts = append(parts, p.Chart.MetricNames...)
		}
	}

	return strings.Join(parts, " ")
}

func getPriority(art *pb.Artifact) int64 {
	if art.GetPriority() != 0 {
		return int64(art.GetPriority())
	}
	return int64(art.GetUrl().GetPriority())
}

func getTags(art *pb.Artifact) []string {
	if art.GetTags() == nil {
		return []string{}
	}
	return art.GetTags()
}
