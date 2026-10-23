// Copyright 2026 Google LLC
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

// Package artifactschema contains the PostgreSQL schema for the Artifacts table.
package artifactschema

import (
	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"

	pb "xmanager_cloud/api/protos/experiment"
)

const (
	artifactsTableName          = querytools.TableName("Artifacts")
	xidTableField               = querytools.TableField("xid")
	workUnitIDTableField        = querytools.TableField("work_unit_id")
	artifactIDTableField        = querytools.TableField("artifact_id")
	urlTableField               = querytools.TableField("url")
	iconTableField              = querytools.TableField("icon")
	displayNameTableField       = querytools.TableField("display_name")
	priorityTableField          = querytools.TableField("priority")
	tagsTableField              = querytools.TableField("tags")
	notesTableField             = querytools.TableField("notes")
	lifecyclePhaseTableField    = querytools.TableField("lifecycle_phase")
	dataTypeTableField          = querytools.TableField("data_type")
	mimeTypeTableField          = querytools.TableField("mime_type")
	additionalInfoTableField    = querytools.TableField("additional_info")
	payloadTableField           = querytools.TableField("payload")
	searchableContentTableField = querytools.TableField("searchable_content")
	payloadTypeTableField       = querytools.TableField("payload_type")
	createdTableField           = querytools.TableField("created")
	updatedTableField           = querytools.TableField("updated")
)

// ArtifactColumns is the full list of columns in the PostgreSQL Artifacts table. Protos are stored
// as BYTEA and proto enums as BIGINT, so PayloadType is populated on write from the Payload oneof
// instead of being derived in SQL as in the Spanner schema.
var ArtifactColumns = []string{
	"Xid", "WorkUnitId", "ArtifactId", "Url", "Icon", "DisplayName", "Priority", "Tags", "Notes",
	"LifecyclePhase", "DataType", "MimeType", "AdditionalInfo", "Payload", "PayloadType", "SearchableContent", "CreateTime", "UpdateTime",
}

// ArtifactUpdateColumns is the list of columns that can be mutated in the Artifacts table.
// PayloadType is updated alongside Payload so payload_type filters stay in sync.
var ArtifactUpdateColumns = []string{
	"Xid", "WorkUnitId", "ArtifactId", "Url", "Icon", "DisplayName", "Priority", "Tags", "Notes",
	"LifecyclePhase", "DataType", "MimeType", "AdditionalInfo", "Payload", "PayloadType", "SearchableContent", "UpdateTime",
}

var (
	// ArtifactTableName is the name of the Artifacts table in PostgreSQL.
	ArtifactTableName = "Artifacts"
	// ArtifactsByArtifactID is the name of the unique index on the ArtifactId column.
	ArtifactsByArtifactID = "ArtifactsByArtifactId"

	artifactsTableView = querytools.TableView{
		Name:       artifactsTableName,
		NameClause: safesql.New("Artifacts a"),
		Cost:       30,
		Fields: map[querytools.TableField]querytools.Member{
			xidTableField:               {Name: xidTableField, QueryLiteral: safesql.New("a.Xid"), Type: filtervisitor.Integer, Selectable: true, Indexed: true},
			workUnitIDTableField:        {Name: workUnitIDTableField, QueryLiteral: safesql.New("a.WorkUnitId"), Type: filtervisitor.Integer, Selectable: true, Indexed: true, Nullable: true},
			artifactIDTableField:        {Name: artifactIDTableField, QueryLiteral: safesql.New("a.ArtifactId"), Type: filtervisitor.Integer, Selectable: true, Indexed: true},
			urlTableField:               {Name: urlTableField, QueryLiteral: safesql.New("a.Url"), Type: filtervisitor.String, Selectable: true},
			iconTableField:              {Name: iconTableField, QueryLiteral: safesql.New("a.Icon"), Type: filtervisitor.String, Selectable: true},
			displayNameTableField:       {Name: displayNameTableField, QueryLiteral: safesql.New("a.DisplayName"), Type: filtervisitor.String, Selectable: true},
			priorityTableField:          {Name: priorityTableField, QueryLiteral: safesql.New("a.Priority"), Type: filtervisitor.Integer, Selectable: true},
			tagsTableField:              {Name: tagsTableField, QueryLiteral: safesql.New("a.Tags"), Type: filtervisitor.ArrayType(&filtervisitor.String), Selectable: true},
			notesTableField:             {Name: notesTableField, QueryLiteral: safesql.New("a.Notes"), Type: filtervisitor.String, Selectable: true},
			lifecyclePhaseTableField:    {Name: lifecyclePhaseTableField, QueryLiteral: safesql.New("a.LifecyclePhase"), Type: filtervisitor.Integer, Selectable: true, EnumValues: pb.Artifact_LifecyclePhase_value},
			dataTypeTableField:          {Name: dataTypeTableField, QueryLiteral: safesql.New("a.DataType"), Type: filtervisitor.Integer, Selectable: true, EnumValues: pb.Artifact_DataType_value},
			mimeTypeTableField:          {Name: mimeTypeTableField, QueryLiteral: safesql.New("a.MimeType"), Type: filtervisitor.String, Selectable: true},
			additionalInfoTableField:    {Name: additionalInfoTableField, QueryLiteral: safesql.New("a.AdditionalInfo"), Selectable: true},
			payloadTableField:           {Name: payloadTableField, QueryLiteral: safesql.New("a.Payload"), Selectable: true},
			searchableContentTableField: {Name: searchableContentTableField, QueryLiteral: safesql.New("a.SearchableContent"), Selectable: false},
			payloadTypeTableField:       {Name: payloadTypeTableField, QueryLiteral: safesql.New("a.PayloadType"), Type: filtervisitor.String, Selectable: false},
			createdTableField:           {Name: createdTableField, QueryLiteral: safesql.New("a.CreateTime"), Type: filtervisitor.Timestamp, Selectable: true},
			updatedTableField:           {Name: updatedTableField, QueryLiteral: safesql.New("a.UpdateTime"), Type: filtervisitor.Timestamp, Selectable: true},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: xidTableField, SortOrder: querytools.Asc},
			{TableField: workUnitIDTableField, SortOrder: querytools.Asc},
			{TableField: artifactIDTableField, SortOrder: querytools.Asc},
		},
	}

	// ArtifactTables is the list of tables that can be used for Artifact queries.
	ArtifactTables = []querytools.TableView{artifactsTableView}

	// ArtifactsSelectFields is the list of fields that can be selected for Artifacts queries.
	ArtifactsSelectFields = []querytools.TableField{
		xidTableField, workUnitIDTableField, artifactIDTableField, urlTableField, iconTableField,
		displayNameTableField, priorityTableField, tagsTableField, notesTableField, lifecyclePhaseTableField,
		dataTypeTableField, mimeTypeTableField, additionalInfoTableField, payloadTableField, createdTableField, updatedTableField,
	}

	// ArtifactsFullTextFields is the list of fields that can be used for full text search for Artifacts queries.
	ArtifactsFullTextFields = []safesql.TrustedSQLString{
		safesql.New("Url_Tokens"), safesql.New("DisplayName_Tokens"), safesql.New("Tags_Tokens"),
		safesql.New("Notes_Tokens"), safesql.New("MimeType_Tokens"), safesql.New("SearchableContent_Tokens"),
	}

	// ArtifactsOrderByFields is the list of fields that can be used for ordering Artifacts queries.
	ArtifactsOrderByFields = []querytools.UserOrderedIndex{
		{TableField: createdTableField}, {TableField: updatedTableField},
		{TableField: priorityTableField}, {TableField: artifactIDTableField}, {TableField: displayNameTableField},
	}
)
