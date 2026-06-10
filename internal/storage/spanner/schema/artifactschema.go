// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// Package artifactschema contains the Spanner schema for the Artifacts table.
package artifactschema

import (
	"api/filter/go/filtervisitor"
	"googlesql/safesql/go/safesql"
	"xmanager_cloud/query_generator/querytools"
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

	lifecyclePhaseTableField = querytools.TableField("lifecycle_phase")
	dataTypeTableField       = querytools.TableField("data_type")
	mimeTypeTableField       = querytools.TableField("mime_type")
	additionalInfoTableField = querytools.TableField("additional_info")
	payloadTableField        = querytools.TableField("payload")
	searchableContentTableField = querytools.TableField("searchable_content")

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
			bitReversedXidTableField: querytools.Member{
				Name:         bitReversedXidTableField,
				QueryLiteral: safesql.New("a.BitReversedXid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			xidTableField: querytools.Member{
				Name:         xidTableField,
				QueryLiteral: safesql.New("a.Xid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			workUnitIDTableField: querytools.Member{
				Name:         workUnitIDTableField,
				QueryLiteral: safesql.New("a.WorkUnitId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			artifactIDTableField: querytools.Member{
				Name:         artifactIDTableField,
				QueryLiteral: safesql.New("a.ArtifactId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			bitReversedArtifactIDTableField: querytools.Member{
				Name:         bitReversedArtifactIDTableField,
				QueryLiteral: safesql.New("a.BitReversedArtifactId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			urlTableField: querytools.Member{
				Name:         urlTableField,
				QueryLiteral: safesql.New("a.Url"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			iconTableField: querytools.Member{
				Name:         iconTableField,
				QueryLiteral: safesql.New("a.Icon"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			displayNameTableField: querytools.Member{
				Name:         displayNameTableField,
				QueryLiteral: safesql.New("a.DisplayName"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			priorityTableField: querytools.Member{
				Name:         priorityTableField,
				QueryLiteral: safesql.New("a.Priority"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			tagsTableField: querytools.Member{
				Name:            tagsTableField,
				SelectSQLString: safesql.New("a.Tags"),
				QueryLiteral:    safesql.New("UNNEST(a.Tags)"),
				Type:            filtervisitor.ArrayType(&filtervisitor.String),
				Selectable:      true,
				Indexed:         false,
			},
			notesTableField: querytools.Member{
				Name:         notesTableField,
				QueryLiteral: safesql.New("a.Notes"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			lifecyclePhaseTableField: querytools.Member{
				Name:         lifecyclePhaseTableField,
				QueryLiteral: safesql.New("a.LifecyclePhase"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			dataTypeTableField: querytools.Member{
				Name:         dataTypeTableField,
				QueryLiteral: safesql.New("a.DataType"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			mimeTypeTableField: querytools.Member{
				Name:         mimeTypeTableField,
				QueryLiteral: safesql.New("a.MimeType"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			additionalInfoTableField: querytools.Member{
				Name:         additionalInfoTableField,
				QueryLiteral: safesql.New("a.AdditionalInfo"),
				Selectable:   true,
				Indexed:      false,
			},
			payloadTableField: querytools.Member{
				Name:         payloadTableField,
				QueryLiteral: safesql.New("a.Payload"),
				Selectable:   true,
				Indexed:      false,
			},
			searchableContentTableField: querytools.Member{
				Name:         searchableContentTableField,
				QueryLiteral: safesql.New("a.SearchableContent"),
				Selectable:   false,
				Indexed:      false,
			},

			createdTableField: querytools.Member{
				Name:         createdTableField,
				QueryLiteral: safesql.New("a.CreateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
			updatedTableField: querytools.Member{
				Name:         updatedTableField,
				QueryLiteral: safesql.New("a.UpdateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			querytools.UserOrderedIndex{TableField: bitReversedXidTableField, SortOrder: querytools.Asc},
			querytools.UserOrderedIndex{TableField: workUnitIDTableField, SortOrder: querytools.Asc},
			querytools.UserOrderedIndex{TableField: artifactIDTableField, SortOrder: querytools.Asc},
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
		querytools.UserOrderedIndex{TableField: createdTableField},
		querytools.UserOrderedIndex{TableField: updatedTableField},
		querytools.UserOrderedIndex{TableField: priorityTableField},
		querytools.UserOrderedIndex{TableField: artifactIDTableField},
		querytools.UserOrderedIndex{TableField: displayNameTableField},
	}
)
