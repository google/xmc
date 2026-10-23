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

// Package workunitschema contains the schema for the PostgreSQL WorkUnits table used by the query
// generator.
package workunitschema

import (
	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
)

const (
	workUnitsTableName              = querytools.TableName("WorkUnits")
	xidTableField                   = querytools.TableField("xid")
	workUnitIDTableField            = querytools.TableField("work_unit_id")
	workUnitTypeTableField          = querytools.TableField("type")
	priorityTableField              = querytools.TableField("priority")
	urlsTableField                  = querytools.TableField("urls")
	tagsTableField                  = querytools.TableField("tags")
	notesTableField                 = querytools.TableField("notes")
	executableTableField            = querytools.TableField("executable")
	executionCreateTimeTableField   = querytools.TableField("execution_created")
	executionUpdateTimeTableField   = querytools.TableField("execution_updated")
	executionCompleteTimeTableField = querytools.TableField("execution_completed")
	executionIDTableField           = querytools.TableField("execution_id")
	executionMetadataTableField     = querytools.TableField("execution_metadata")
	workUnitStatusTableField        = querytools.TableField("status")
	queueNameTableField             = querytools.TableField("queue")
	executionStateTableField        = querytools.TableField("state")
	createdTableField               = querytools.TableField("created")
	updatedTableField               = querytools.TableField("updated")
)

// WorkUnitColumns is the full list of columns in the PostgreSQL WorkUnits table.
var WorkUnitColumns = []string{
	"Xid", "WorkUnitId", "WorkUnitType", "Priority", "Urls", "Tags", "Notes", "Executable",
	"ExecutionCreateTime", "ExecutionUpdateTime", "ExecutionCompleteTime", "ExecutionId",
	"ExecutionMetadata", "WorkUnitStatus", "QueueName", "ExecutionState",
	"ExecutionStatusMessageSeverity", "PositionInQueue", "CreateTime", "UpdateTime",
}

// CurrentWorkUnitExecutionColumns is the list of columns to fetch when getting the current work
// unit execution. Xid and WorkUnitId are primary keys and should always be returned.
var CurrentWorkUnitExecutionColumns = []string{
	"Xid", "WorkUnitId", "ExecutionId", "ExecutionCreateTime",
	"ExecutionUpdateTime", "ExecutionCompleteTime", "ExecutionMetadata", "WorkUnitStatus",
}

// CurrentWorkUnitExecutionUpdateColumns is the list of columns to update when updating the current
// work unit execution. Xid and WorkUnitId are primary keys and cannot be mutated.
var CurrentWorkUnitExecutionUpdateColumns = []string{
	"Xid", "WorkUnitId", "ExecutionId", "ExecutionCreateTime", "ExecutionUpdateTime",
	"ExecutionCompleteTime", "ExecutionMetadata", "WorkUnitStatus",
	"ExecutionState", "ExecutionStatusMessageSeverity", "PositionInQueue", "UpdateTime",
}

// WorkUnitUpdateColumns is the list of columns that can be mutated in the WorkUnits table.
// Xid and WorkUnitId are the primary keys and cannot be mutated.
var WorkUnitUpdateColumns = []string{
	"Xid", "WorkUnitId", "Priority", "Urls", "Tags", "Notes", "UpdateTime",
}

var (
	// WorkUnitTableName is the name of the WorkUnits table in PostgreSQL.
	WorkUnitTableName = "WorkUnits"

	workUnitsTableView = querytools.TableView{
		Name:       workUnitsTableName,
		NameClause: safesql.New("WorkUnits wu"),
		Cost:       30,
		Fields: map[querytools.TableField]querytools.Member{
			xidTableField:                   {Name: xidTableField, QueryLiteral: safesql.New("wu.Xid"), Type: filtervisitor.Integer, Selectable: true, Indexed: true},
			workUnitIDTableField:            {Name: workUnitIDTableField, QueryLiteral: safesql.New("wu.WorkUnitId"), Type: filtervisitor.Integer, Selectable: true, Indexed: true},
			workUnitTypeTableField:          {Name: workUnitTypeTableField, QueryLiteral: safesql.New("wu.WorkUnitType"), Type: filtervisitor.Integer, Selectable: true},
			priorityTableField:              {Name: priorityTableField, QueryLiteral: safesql.New("wu.Priority"), Type: filtervisitor.Integer, Selectable: true},
			urlsTableField:                  {Name: urlsTableField, QueryLiteral: safesql.New("wu.Urls"), Type: filtervisitor.ArrayType(&filtervisitor.Bytes), Selectable: true},
			tagsTableField:                  {Name: tagsTableField, QueryLiteral: safesql.New("wu.Tags"), Type: filtervisitor.ArrayType(&filtervisitor.String), Selectable: true},
			notesTableField:                 {Name: notesTableField, QueryLiteral: safesql.New("wu.Notes"), Type: filtervisitor.String, Selectable: true},
			executableTableField:            {Name: executableTableField, QueryLiteral: safesql.New("wu.Executable"), Type: filtervisitor.Bytes, Selectable: true},
			executionCreateTimeTableField:   {Name: executionCreateTimeTableField, QueryLiteral: safesql.New("wu.ExecutionCreateTime"), Type: filtervisitor.Timestamp, Selectable: true},
			executionUpdateTimeTableField:   {Name: executionUpdateTimeTableField, QueryLiteral: safesql.New("wu.ExecutionUpdateTime"), Type: filtervisitor.Timestamp, Selectable: true},
			executionCompleteTimeTableField: {Name: executionCompleteTimeTableField, QueryLiteral: safesql.New("wu.ExecutionCompleteTime"), Type: filtervisitor.Timestamp, Selectable: true, Nullable: true},
			executionIDTableField:           {Name: executionIDTableField, QueryLiteral: safesql.New("wu.ExecutionId"), Type: filtervisitor.Integer, Selectable: true},
			executionMetadataTableField:     {Name: executionMetadataTableField, QueryLiteral: safesql.New("wu.ExecutionMetadata"), Type: filtervisitor.Bytes, Selectable: true},
			workUnitStatusTableField:        {Name: workUnitStatusTableField, QueryLiteral: safesql.New("wu.WorkUnitStatus"), Type: filtervisitor.Bytes, Selectable: true},
			queueNameTableField:             {Name: queueNameTableField, QueryLiteral: safesql.New("wu.QueueName"), Type: filtervisitor.String, Selectable: true},
			executionStateTableField:        {Name: executionStateTableField, QueryLiteral: safesql.New("wu.ExecutionState"), Type: filtervisitor.Integer, Selectable: true},
			createdTableField:               {Name: createdTableField, QueryLiteral: safesql.New("wu.CreateTime"), Type: filtervisitor.Timestamp, Selectable: true},
			updatedTableField:               {Name: updatedTableField, QueryLiteral: safesql.New("wu.UpdateTime"), Type: filtervisitor.Timestamp, Selectable: true},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: xidTableField, SortOrder: querytools.Asc},
			{TableField: workUnitIDTableField, SortOrder: querytools.Asc},
		},
	}

	// WorkUnitTables is the list of tables that can be used for WorkUnit queries.
	WorkUnitTables = []querytools.TableView{workUnitsTableView}

	// WorkUnitSelectFields is the list of fields that can be selected for WorkUnit queries.
	WorkUnitSelectFields = []querytools.TableField{
		xidTableField, workUnitIDTableField, workUnitTypeTableField, priorityTableField,
		urlsTableField, tagsTableField, notesTableField, executableTableField,
		executionCreateTimeTableField, executionUpdateTimeTableField,
		executionCompleteTimeTableField, executionIDTableField, executionMetadataTableField,
		workUnitStatusTableField, queueNameTableField, executionStateTableField,
		createdTableField, updatedTableField,
	}

	// WorkUnitFullTextFields is the list of fields that can be used for full text search for WorkUnit queries.
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
