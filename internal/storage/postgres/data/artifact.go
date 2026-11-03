// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package postgresdata

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/validator"
)

// ArtifactRow represents a row in the PostgreSQL Artifacts table.
type ArtifactRow struct {
	Xid               int64       `db:"xid"`
	WorkUnitID        pgtype.Int8 `db:"workunitid"`
	ArtifactID        int64       `db:"artifactid"`
	URL               string      `db:"url"`
	Icon              string      `db:"icon"`
	DisplayName       string      `db:"displayname"`
	Priority          int64       `db:"priority"`
	Tags              []string    `db:"tags"`
	Notes             string      `db:"notes"`
	LifecyclePhase    int64       `db:"lifecyclephase"`
	DataType          int64       `db:"datatype"`
	MimeType          string      `db:"mimetype"`
	AdditionalInfo    []byte      `db:"additionalinfo"`
	Payload           []byte      `db:"payload"`
	PayloadType       string      `db:"payloadtype"`
	SearchableContent string      `db:"searchablecontent"`
	CreateTime        time.Time   `db:"createtime"`
	UpdateTime        time.Time   `db:"updatetime"`
}

// GetInsertArgs returns the strict named arguments required by the Artifacts INSERT statement.
func (r ArtifactRow) GetInsertArgs() pgx.StrictNamedArgs {
	args := r.GetUpdateArgs()
	args["workunitid"] = r.WorkUnitID
	args["createtime"] = r.CreateTime
	return args
}

// GetUpdateArgs returns the strict named arguments required by the Artifacts UPDATE statement.
func (r ArtifactRow) GetUpdateArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid": r.Xid, "artifactid": r.ArtifactID,
		"url": r.URL, "icon": r.Icon, "displayname": r.DisplayName,
		"priority": r.Priority, "tags": r.Tags, "notes": r.Notes,
		"lifecyclephase": r.LifecyclePhase, "datatype": r.DataType, "mimetype": r.MimeType,
		"additionalinfo": r.AdditionalInfo, "payload": r.Payload,
		"payloadtype": r.PayloadType, "searchablecontent": r.SearchableContent,
		"updatetime": r.UpdateTime,
	}
}

// ArtifactRowToProto converts a scanned PostgreSQL ArtifactRow into an essmsgpb.Artifact proto.
func ArtifactRowToProto(row ArtifactRow) (*essmsgpb.Artifact, error) {
	artifact := &essmsgpb.Artifact{
		Name: validator.ConstructArtifactName(row.ArtifactID), Parent: constructArtifactParent(row.Xid, row.WorkUnitID),
		Url:      &essmsgpb.Url{Url: row.URL, Icon: row.Icon, DisplayName: row.DisplayName, Priority: int32(row.Priority)},
		Priority: int32(row.Priority), Tags: row.Tags, Notes: row.Notes,
		LifecyclePhase: essmsgpb.Artifact_LifecyclePhase(row.LifecyclePhase),
		DataType:       essmsgpb.Artifact_DataType(row.DataType), MimeType: row.MimeType,
		CreateTime: timestamppb.New(row.CreateTime), UpdateTime: timestamppb.New(row.UpdateTime),
	}
	if err := unmarshalArtifactPayloads(artifact, row); err != nil {
		return nil, err
	}
	return artifact, nil
}

// ArtifactProtoToRow converts an essmsgpb.Artifact proto into an ArtifactRow for persistence.
func ArtifactProtoToRow(art *essmsgpb.Artifact) (ArtifactRow, error) {
	aid, err := validator.ValidateArtifactName(art.GetName())
	if err != nil {
		return ArtifactRow{}, fmt.Errorf("failed to validate artifact name %q: %w", art.GetName(), err)
	}
	xid, wid, err := validator.ValidateArtifactParent(art.GetParent())
	if err != nil {
		return ArtifactRow{}, fmt.Errorf("failed to validate artifact parent %q: %w", art.GetParent(), err)
	}
	row := buildArtifactRowScalars(art, xid, wid, aid)
	if err := setArtifactPayloads(&row, art); err != nil {
		return ArtifactRow{}, err
	}
	return row, nil
}

func buildArtifactRowScalars(art *essmsgpb.Artifact, xid, wid, aid int64) ArtifactRow {
	return ArtifactRow{
		Xid: xid, WorkUnitID: pgtype.Int8{Int64: wid, Valid: wid != 0}, ArtifactID: aid,
		URL: art.GetUrl().GetUrl(), Icon: art.GetUrl().GetIcon(), DisplayName: art.GetUrl().GetDisplayName(),
		Priority: data.ArtifactPriority(art), Tags: data.ArtifactTags(art), Notes: art.GetNotes(),
		LifecyclePhase: int64(art.GetLifecyclePhase()), DataType: int64(art.GetDataType()), MimeType: art.GetMimeType(),
		PayloadType: artifactPayloadType(art.GetPayload()), SearchableContent: data.ArtifactSearchableContent(art.GetPayload()),
		CreateTime: art.GetCreateTime().AsTime(), UpdateTime: art.GetUpdateTime().AsTime(),
	}
}

func unmarshalArtifactPayloads(artifact *essmsgpb.Artifact, row ArtifactRow) error {
	if row.AdditionalInfo != nil {
		info, err := UnmarshalProto[essmsgpb.Artifact_AdditionalInfo](row.AdditionalInfo)
		if err != nil {
			return fmt.Errorf("failed to unmarshal AdditionalInfo: %w", err)
		}
		artifact.AdditionalInfo = info
	}
	if row.Payload != nil {
		payload, err := UnmarshalProto[essmsgpb.ArtifactPayload](row.Payload)
		if err != nil {
			return fmt.Errorf("failed to unmarshal Payload: %w", err)
		}
		artifact.Payload = payload
	}
	return nil
}

func setArtifactPayloads(row *ArtifactRow, art *essmsgpb.Artifact) (err error) {
	if row.AdditionalInfo, err = MarshalNullableProto(art.GetAdditionalInfo()); err != nil {
		return fmt.Errorf("failed to marshal AdditionalInfo: %w", err)
	}
	if row.Payload, err = MarshalNullableProto(art.GetPayload()); err != nil {
		return fmt.Errorf("failed to marshal Payload: %w", err)
	}
	return nil
}

func constructArtifactParent(xid int64, wid pgtype.Int8) string {
	if wid.Valid {
		return validator.ConstructWorkUnitName(xid, wid.Int64)
	}
	return validator.ConstructExperimentName(xid)
}

func artifactPayloadType(payload *essmsgpb.ArtifactPayload) string {
	switch payload.GetType().(type) {
	case *essmsgpb.ArtifactPayload_ManagedXprof:
		return "managed_xprof"
	case *essmsgpb.ArtifactPayload_CodeSource:
		return "code_source"
	case *essmsgpb.ArtifactPayload_Text:
		return "text"
	case *essmsgpb.ArtifactPayload_Url:
		return "url"
	case *essmsgpb.ArtifactPayload_Chart:
		return "chart"
	default:
		return ""
	}
}
