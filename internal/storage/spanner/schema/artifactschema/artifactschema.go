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
// Package artifactschema contains the Spanner schema for the Artifacts table.
package artifactschema

import (
	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
)

const (
	artifactsTableName = querytools.TableName("Artifacts")

	xidTableField                   = querytools.TableField("xid")
	bitReversedXidTableField        = querytools.TableField("bit_reversed_xid")
	workUnitIDTableField            = querytools.TableField("work_unit_id")
	artifactIDTableField            = querytools.TableField("artifact_id")
	bitReversedArtifactIDTableField = querytools.TableField("bit_reversed_artifact_id")

	urlTableField         = querytools.TableField("url")
	iconTableField        = querytools.TableField("icon")
	displayNameTableField = querytools.TableField("display_name")
	priorityTableField    = querytools.TableField("priority")

	tagsTableField  = querytools.TableField("tags")
	notesTableField = querytools.TableField("notes")

	lifecyclePhaseTableField    = querytools.TableField("lifecycle_phase")
	dataTypeTableField          = querytools.TableField("data_type")
	mimeTypeTableField          = querytools.TableField("mime_type")
	additionalInfoTableField    = querytools.TableField("additional_info")
	payloadTableField           = querytools.TableField("payload")
	searchableContentTableField = querytools.TableField("searchable_content")

	// payloadTypeTableField is a filter-only virtual field. It maps the Payload
	// oneof to a stable type name so callers can filter artifacts by payload type,
	// e.g. `payload_type = "managed_xprof"`. Extending to a new payload type is a
	// one-line addition to the CASE expression in the field definition below.
	payloadTypeTableField = querytools.TableField("payload_type")

	createdTableField = querytools.TableField("created")
	updatedTableField = querytools.TableField("updated")
)

// ArtifactColumns is the full list of columns in the Artifacts table. It is used when inserting
// into the table.
var ArtifactColumns = []string{
	"Xid",
	"WorkUnitId",
	"ArtifactId",
	"Url",
	"Icon",
	"DisplayName",
	"Priority",
	"Tags",
	"Notes",
	"LifecyclePhase",
	"DataType",
	"MimeType",
	"AdditionalInfo",
	"Payload",
	"SearchableContent",
	"CreateTime",
	"UpdateTime",
}

// ArtifactUpdateColumns is the list of columns that can be mutated in the Artifacts table.
// BitReversedXid and WorkUnitId are the primary key and cannot be mutated.
var ArtifactUpdateColumns = []string{
	"BitReversedXid",
	"WorkUnitId",
	"ArtifactId",
	"Url",
	"Icon",
	"DisplayName",
	"Priority",
	"Tags",
	"Notes",
	"LifecyclePhase",
	"DataType",
	"MimeType",
	"AdditionalInfo",
	"Payload",
	"SearchableContent",
	"UpdateTime",
}

var (
	// ArtifactTableName is the name of the Artifacts table in Spanner.
	ArtifactTableName = "Artifacts"

	// ArtifactsByBitReversedArtifactID is the name of the index on the BitReversedArtifactId column.
	ArtifactsByBitReversedArtifactID = "ArtifactsByBitReversedArtifactId"

	artifactsTableView = querytools.TableView{
		Name:       artifactsTableName,
		NameClause: safesql.New("Artifacts a"),
		Cost:       30,
		Fields: map[querytools.TableField]querytools.Member{
			bitReversedXidTableField: {
				Name:         bitReversedXidTableField,
				QueryLiteral: safesql.New("a.BitReversedXid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			xidTableField: {
				Name:         xidTableField,
				QueryLiteral: safesql.New("a.Xid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			workUnitIDTableField: {
				Name:         workUnitIDTableField,
				QueryLiteral: safesql.New("a.WorkUnitId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
				Nullable:     true,
			},
			artifactIDTableField: {
				Name:         artifactIDTableField,
				QueryLiteral: safesql.New("a.ArtifactId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			bitReversedArtifactIDTableField: {
				Name:         bitReversedArtifactIDTableField,
				QueryLiteral: safesql.New("a.BitReversedArtifactId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			urlTableField: {
				Name:         urlTableField,
				QueryLiteral: safesql.New("a.Url"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			iconTableField: {
				Name:         iconTableField,
				QueryLiteral: safesql.New("a.Icon"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			displayNameTableField: {
				Name:         displayNameTableField,
				QueryLiteral: safesql.New("a.DisplayName"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			priorityTableField: {
				Name:         priorityTableField,
				QueryLiteral: safesql.New("a.Priority"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			tagsTableField: {
				Name:         tagsTableField,
				QueryLiteral: safesql.New("a.Tags"),
				Type:         filtervisitor.ArrayType(&filtervisitor.String),
				Selectable:   true,
				Indexed:      false,
			},
			notesTableField: {
				Name:         notesTableField,
				QueryLiteral: safesql.New("a.Notes"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			lifecyclePhaseTableField: {
				Name:         lifecyclePhaseTableField,
				QueryLiteral: safesql.New("a.LifecyclePhase"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			dataTypeTableField: {
				Name:         dataTypeTableField,
				QueryLiteral: safesql.New("a.DataType"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			mimeTypeTableField: {
				Name:         mimeTypeTableField,
				QueryLiteral: safesql.New("a.MimeType"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			additionalInfoTableField: {
				Name:         additionalInfoTableField,
				QueryLiteral: safesql.New("a.AdditionalInfo"),
				Selectable:   true,
				Indexed:      false,
			},
			payloadTableField: {
				Name:         payloadTableField,
				QueryLiteral: safesql.New("a.Payload"),
				Selectable:   true,
				Indexed:      false,
			},
			searchableContentTableField: {
				Name:         searchableContentTableField,
				QueryLiteral: safesql.New("a.SearchableContent"),
				Selectable:   false,
				Indexed:      false,
			},

			payloadTypeTableField: {
				Name: payloadTypeTableField,
				// Maps the Payload oneof to a stable string so artifacts can be
				// filtered by type, e.g. `payload_type = "managed_xprof"`. Extending
				// to a new payload type is a one-line CASE addition. Derived and
				// non-selectable because it is computed, not a stored column.
				QueryLiteral: safesql.New(
					"(CASE " +
						"WHEN a.Payload.managed_xprof IS NOT NULL THEN 'managed_xprof' " +
						"WHEN a.Payload.code_source IS NOT NULL THEN 'code_source' " +
						"WHEN a.Payload.text IS NOT NULL THEN 'text' " +
						"WHEN a.Payload.url IS NOT NULL THEN 'url' " +
						"WHEN a.Payload.chart IS NOT NULL THEN 'chart' " +
						"ELSE '' END)"),
				Type:       filtervisitor.String,
				Derived:    true,
				Selectable: false,
				Indexed:    false,
			},

			createdTableField: {
				Name:         createdTableField,
				QueryLiteral: safesql.New("a.CreateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
			updatedTableField: {
				Name:         updatedTableField,
				QueryLiteral: safesql.New("a.UpdateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: bitReversedXidTableField, SortOrder: querytools.Asc},
			{TableField: workUnitIDTableField, SortOrder: querytools.Asc},
			{TableField: artifactIDTableField, SortOrder: querytools.Asc},
		},
	}

	// ArtifactTables is the list of tables that can be used for Artifact queries.
	ArtifactTables = []querytools.TableView{artifactsTableView}

	// ArtifactsSelectFields is the list of fields that can be selected for Artifacts queries.
	ArtifactsSelectFields = []querytools.TableField{
		bitReversedXidTableField,
		xidTableField,
		workUnitIDTableField,
		artifactIDTableField,
		bitReversedArtifactIDTableField,
		urlTableField,
		iconTableField,
		displayNameTableField,
		priorityTableField,
		tagsTableField,
		notesTableField,
		lifecyclePhaseTableField,
		dataTypeTableField,
		mimeTypeTableField,
		additionalInfoTableField,
		payloadTableField,

		createdTableField,
		updatedTableField,
	}

	// ArtifactsFullTextFields is the list of fields that can be used for full text search for Artifacts queries.
	ArtifactsFullTextFields = []safesql.TrustedSQLString{safesql.New("Url_Tokens"), safesql.New("DisplayName_Tokens"), safesql.New("Tags_Tokens"), safesql.New("Notes_Tokens"), safesql.New("MimeType_Tokens"), safesql.New("SearchableContent_Tokens")}

	// ArtifactsOrderByFields is the list of fields that can be used for ordering Artifacts queries.
	ArtifactsOrderByFields = []querytools.UserOrderedIndex{
		{TableField: createdTableField},
		{TableField: updatedTableField},
		{TableField: priorityTableField},
		{TableField: artifactIDTableField},
		{TableField: displayNameTableField},
	}
)
