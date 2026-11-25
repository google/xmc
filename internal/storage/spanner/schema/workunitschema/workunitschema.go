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
// Package workunitschema contains the schema for the Spanner tables used by the query generator.
// It defines the schema held in the WorkUnits table.
package workunitschema

import (
	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
)

const (
	workUnitsTableName = querytools.TableName("WorkUnits")

	bitReversedXidTableField = querytools.TableField("bit_reversed_xid")
	xidTableField            = querytools.TableField("xid")
	workUnitIDTableField     = querytools.TableField("work_unit_id")

	workUnitTypeTableField = querytools.TableField("type")
	priorityTableField     = querytools.TableField("priority")
	urlsTableField         = querytools.TableField("urls")
	tagsTableField         = querytools.TableField("tags")
	notesTableField        = querytools.TableField("notes")
	executableTableField   = querytools.TableField("executable")

	executionCreateTimeTableField   = querytools.TableField("execution_created")
	executionUpdateTimeTableField   = querytools.TableField("execution_updated")
	executionCompleteTimeTableField = querytools.TableField("execution_completed")
	executionIDTableField           = querytools.TableField("execution_id")
	executionMetadataTableField     = querytools.TableField("execution_metadata")
	workUnitStatusTableField        = querytools.TableField("status")

	queueNameTableField      = querytools.TableField("queue")
	executionStateTableField = querytools.TableField("state")

	createdTableField = querytools.TableField("created")
	updatedTableField = querytools.TableField("updated")

	parametersTableField = querytools.TableField("parameters")
	trialIDTableField    = querytools.TableField("trial_id")
)

// WorkUnitColumns is the full list of columns in the WorkUnits table. It is used when fetching
// or inserting into the table. Note that we return the XID column, even though the primary key is
// actually the BitReversedXID column (which should be exposed in the WHERE clause).
var WorkUnitColumns = []string{
	"Xid",
	"WorkUnitId",
	"WorkUnitType",
	"Priority",
	"Urls",
	"Tags",
	"Notes",
	"Executable",
	"ExecutionCreateTime",
	"ExecutionUpdateTime",
	"ExecutionCompleteTime",
	"ExecutionId",
	"ExecutionMetadata",
	"WorkUnitStatus",
	"CreateTime",
	"UpdateTime",
	"Parameters",
	"TrialId",
}

// CurrentWorkUnitExecutionColumns is the list of columns to fetch when getting the current work
// unit execution. Xid and WorkUnitId are primary keys and should always be returned.
var CurrentWorkUnitExecutionColumns = []string{
	"Xid",
	"WorkUnitId",
	"ExecutionId",
	"ExecutionCreateTime",
	"ExecutionUpdateTime",
	"ExecutionCompleteTime",
	"ExecutionMetadata",
	"WorkUnitStatus",
}

// CurrentWorkUnitExecutionUpdateColumns is the list of columns to update when updating the current
// work unit execution. BitReversedXid and WorkUnitId are primary keys and cannot be mutated.
var CurrentWorkUnitExecutionUpdateColumns = []string{
	"BitReversedXid",
	"WorkUnitId",
	"ExecutionId",
	"ExecutionCreateTime",
	"ExecutionUpdateTime",
	"ExecutionCompleteTime",
	"ExecutionMetadata",
	"WorkUnitStatus",
	"UpdateTime",
}

// WorkUnitUpdateColumns is the list of columns that can be mutated in the
// WorkUnits table. It's a subset of the columns in the WorkUnitExecutions table that are
// mutable. BitReversedXid and WorkUnitId are the primary keys and cannot be mutated.
var WorkUnitUpdateColumns = []string{
	"BitReversedXid",
	"WorkUnitId",
	"Priority",
	"Urls",
	"Tags",
	"Notes",
	"UpdateTime",
}

// WorkUnitTables is the list of tables that can be used for WorkUnit queries.
var (
	WorkUnitTableName = "WorkUnits"

	workUnitsTableView = querytools.TableView{
		Name:       workUnitsTableName,
		NameClause: safesql.New("WorkUnits wu"),
		Cost:       30,
		Fields: map[querytools.TableField]querytools.Member{
			bitReversedXidTableField: {
				Name:         bitReversedXidTableField,
				QueryLiteral: safesql.New("wu.BitReversedXid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			xidTableField: {
				Name:         xidTableField,
				QueryLiteral: safesql.New("wu.Xid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			workUnitIDTableField: {
				Name:         workUnitIDTableField,
				QueryLiteral: safesql.New("wu.WorkUnitId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			workUnitTypeTableField: {
				Name:         workUnitTypeTableField,
				QueryLiteral: safesql.New("wu.WorkUnitType"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			priorityTableField: {
				Name:         priorityTableField,
				QueryLiteral: safesql.New("wu.Priority"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			urlsTableField: {
				Name:         urlsTableField,
				QueryLiteral: safesql.New("wu.Urls"),
				Type:         filtervisitor.ArrayType(&filtervisitor.Bytes),
				Selectable:   true,
				Indexed:      false,
			},
			tagsTableField: {
				Name:         tagsTableField,
				QueryLiteral: safesql.New("wu.Tags"),
				Type:         filtervisitor.ArrayType(&filtervisitor.String),
				Selectable:   true,
				Indexed:      false,
			},
			notesTableField: {
				Name:         notesTableField,
				QueryLiteral: safesql.New("wu.Notes"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			executableTableField: {
				Name:         executableTableField,
				QueryLiteral: safesql.New("wu.Executable"),
				Type:         filtervisitor.Bytes,
				Selectable:   true,
				Indexed:      false,
			},
			executionCreateTimeTableField: {
				Name:         executionCreateTimeTableField,
				QueryLiteral: safesql.New("wu.ExecutionCreateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
			executionUpdateTimeTableField: {
				Name:         executionUpdateTimeTableField,
				QueryLiteral: safesql.New("wu.ExecutionUpdateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
			executionCompleteTimeTableField: {
				Name:         executionCompleteTimeTableField,
				QueryLiteral: safesql.New("wu.ExecutionCompleteTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
				// NULL until the current execution completes.
				Nullable: true,
			},
			executionIDTableField: {
				Name:         executionIDTableField,
				QueryLiteral: safesql.New("wu.ExecutionId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			executionMetadataTableField: {
				Name:         executionMetadataTableField,
				QueryLiteral: safesql.New("wu.ExecutionMetadata"),
				Type:         filtervisitor.Bytes,
				Selectable:   true,
				Indexed:      false,
			},
			workUnitStatusTableField: {
				Name:         workUnitStatusTableField,
				QueryLiteral: safesql.New("wu.WorkUnitStatus"),
				Type:         filtervisitor.Bytes,
				Selectable:   true,
				Indexed:      false,
			},
			queueNameTableField: {
				Name:         queueNameTableField,
				QueryLiteral: safesql.New("wu.QueueName"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			executionStateTableField: {
				Name:         executionStateTableField,
				QueryLiteral: safesql.New("wu.ExecutionState"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			createdTableField: {
				Name:         createdTableField,
				QueryLiteral: safesql.New("wu.CreateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
			updatedTableField: {
				Name:         updatedTableField,
				QueryLiteral: safesql.New("wu.UpdateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
			// Selectable only: study trial fields are not filterable.
			parametersTableField: {Name: parametersTableField, QueryLiteral: safesql.New("wu.Parameters"), Selectable: true},
			trialIDTableField:    {Name: trialIDTableField, QueryLiteral: safesql.New("wu.TrialId"), Selectable: true},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: bitReversedXidTableField, SortOrder: querytools.Asc},
			{TableField: workUnitIDTableField, SortOrder: querytools.Asc},
		},
	}

	// WorkUnitTables is the list of tables that can be used for WorkUnit queries.
	WorkUnitTables = []querytools.TableView{workUnitsTableView}

	// WorkUnitSelectFields is the list of fields that can be selected for WorkUnit queries.
	WorkUnitSelectFields = []querytools.TableField{
		bitReversedXidTableField,
		xidTableField,
		workUnitIDTableField,
		workUnitTypeTableField,
		priorityTableField,
		urlsTableField,
		tagsTableField,
		notesTableField,
		executableTableField,
		executionCreateTimeTableField,
		executionUpdateTimeTableField,
		executionCompleteTimeTableField,
		executionIDTableField,
		executionMetadataTableField,
		workUnitStatusTableField,
		queueNameTableField,
		executionStateTableField,
		createdTableField,
		updatedTableField,
		parametersTableField,
		trialIDTableField,
	}

	// WorkUnitFullTextFields is the list of fields that can be used for full text search for WorkUnit queries.
	// It's empty for now, but we can add fields here if we want to support full text search.
	WorkUnitFullTextFields = []safesql.TrustedSQLString{}

	// WorkUnitOrderByFields is the list of fields that can be used for ordering WorkUnit queries.
	WorkUnitOrderByFields = []querytools.UserOrderedIndex{
		{TableField: createdTableField},
		{TableField: updatedTableField},
		{TableField: xidTableField},
		{TableField: workUnitIDTableField},
		{TableField: executionStateTableField},
		{TableField: executionCompleteTimeTableField},
	}
)
