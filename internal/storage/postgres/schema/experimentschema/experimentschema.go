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

// Package experimentschema contains the schema for the PostgreSQL Experiments table used by the
// query generator.
package experimentschema

import (
	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
)

const (
	experimentsTableName  = querytools.TableName("Experiments")
	xidTableField         = querytools.TableField("xid")
	titleTableField       = querytools.TableField("title")
	creatorTableField     = querytools.TableField("creator")
	priorityTableField    = querytools.TableField("priority")
	urlsTableField        = querytools.TableField("urls")
	tagsTableField        = querytools.TableField("tags")
	notesTableField       = querytools.TableField("notes")
	resourcesTableField   = querytools.TableField("resources")
	statusTableField      = querytools.TableField("status")
	activeStateTableField = querytools.TableField("active_state")
	launchStateTableField = querytools.TableField("launch_state")
	createdTableField     = querytools.TableField("created")
	updatedTableField     = querytools.TableField("updated")
	completedTableField   = querytools.TableField("completed")
)

// ExperimentColumns is the full list of columns in the PostgreSQL Experiments table.
var ExperimentColumns = []string{
	"Xid", "Title", "Creator", "Priority", "Urls", "Tags", "Notes",
	"Resources", "Status", "ActiveState", "LaunchState",
	"CreateTime", "UpdateTime", "CompleteTime",
}

// ExperimentUpdateColumns is the list of columns that can be mutated in the Experiments table.
// Xid is the primary key and cannot be mutated.
var ExperimentUpdateColumns = []string{
	"Xid", "Title", "Priority", "Urls", "Tags", "Notes", "UpdateTime",
}

// ExperimentStatusUpdateColumns is the list of columns that can be mutated in the Experiments
// table when updating the status. Xid is the primary key and cannot be mutated.
var ExperimentStatusUpdateColumns = []string{
	"Xid", "Status", "ActiveState", "LaunchState", "UpdateTime", "CompleteTime",
}

// ExperimentResourcesUpdateColumns is the list of columns that can be mutated in the Experiments
// table when updating the resources. Xid is the primary key and cannot be mutated.
var ExperimentResourcesUpdateColumns = []string{
	"Xid", "Resources", "UpdateTime",
}

var (
	// ExperimentTableName is the name of the Experiments table in PostgreSQL.
	ExperimentTableName = "Experiments"

	experimentsTableView = querytools.TableView{
		Name:       experimentsTableName,
		NameClause: safesql.New("Experiments e"),
		Cost:       30,
		Fields: map[querytools.TableField]querytools.Member{
			xidTableField:         {Name: xidTableField, QueryLiteral: safesql.New("e.Xid"), Type: filtervisitor.Integer, Selectable: true, Indexed: true},
			titleTableField:       {Name: titleTableField, QueryLiteral: safesql.New("e.Title"), Type: filtervisitor.String, Selectable: true},
			creatorTableField:     {Name: creatorTableField, QueryLiteral: safesql.New("e.Creator"), Type: filtervisitor.String, Selectable: true, DisjunctionOptimizable: true},
			priorityTableField:    {Name: priorityTableField, QueryLiteral: safesql.New("e.Priority"), Type: filtervisitor.Integer, Selectable: true},
			urlsTableField:        {Name: urlsTableField, QueryLiteral: safesql.New("e.Urls"), Type: filtervisitor.ArrayType(&filtervisitor.Bytes), Selectable: true},
			tagsTableField:        {Name: tagsTableField, QueryLiteral: safesql.New("e.Tags"), Type: filtervisitor.ArrayType(&filtervisitor.String), Selectable: true},
			notesTableField:       {Name: notesTableField, QueryLiteral: safesql.New("e.Notes"), Type: filtervisitor.String, Selectable: true},
			resourcesTableField:   {Name: resourcesTableField, QueryLiteral: safesql.New("e.Resources"), Type: filtervisitor.Bytes, Selectable: true},
			statusTableField:      {Name: statusTableField, QueryLiteral: safesql.New("e.Status"), Type: filtervisitor.Bytes, Selectable: true},
			activeStateTableField: {Name: activeStateTableField, QueryLiteral: safesql.New("e.ActiveState"), Type: filtervisitor.Integer, Selectable: true},
			launchStateTableField: {Name: launchStateTableField, QueryLiteral: safesql.New("e.LaunchState"), Type: filtervisitor.Integer, Selectable: true},
			createdTableField:     {Name: createdTableField, QueryLiteral: safesql.New("e.CreateTime"), Type: filtervisitor.Timestamp, Selectable: true},
			updatedTableField:     {Name: updatedTableField, QueryLiteral: safesql.New("e.UpdateTime"), Type: filtervisitor.Timestamp, Selectable: true},
			completedTableField:   {Name: completedTableField, QueryLiteral: safesql.New("e.CompleteTime"), Type: filtervisitor.Timestamp, Selectable: true, Nullable: true},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: xidTableField, SortOrder: querytools.Asc},
		},
	}

	// ExperimentTables is the list of tables that can be used for Experiment queries.
	ExperimentTables = []querytools.TableView{experimentsTableView}

	// ExperimentsSelectFields is the list of fields that can be selected for Experiments queries.
	ExperimentsSelectFields = []querytools.TableField{
		xidTableField, creatorTableField, priorityTableField, titleTableField,
		urlsTableField, tagsTableField, notesTableField, resourcesTableField,
		statusTableField, activeStateTableField, launchStateTableField,
		createdTableField, updatedTableField, completedTableField,
	}

	// ExperimentsFullTextFields is the list of fields that can be used for full text search for Experiments queries.
	ExperimentsFullTextFields = []safesql.TrustedSQLString{
		safesql.New("Title_Tokens"),
		safesql.New("Tags_Tokens"),
		safesql.New("Creator_Tokens"),
	}

	// ExperimentsOrderByFields is the list of fields that can be used for ordering Experiments queries.
	ExperimentsOrderByFields = []querytools.UserOrderedIndex{
		{TableField: createdTableField},
		{TableField: updatedTableField},
		{TableField: completedTableField},
		{TableField: xidTableField},
	}
)
