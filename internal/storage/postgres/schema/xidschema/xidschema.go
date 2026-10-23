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

// Package xidschema contains the PostgreSQL schema for the Xids table used by the query generator.
package xidschema

import (
	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
)

const (
	xidsTableName               = querytools.TableName("Xids")
	xidTableField               = querytools.TableField("xid")
	parentXidTableField         = querytools.TableField("parent_xid")
	creatorTableField           = querytools.TableField("creator")
	originatingSystemTableField = querytools.TableField("origin_system")
	endpointTableField          = querytools.TableField("endpoint")
	aclsTableField              = querytools.TableField("acls")
	createdTableField           = querytools.TableField("created")
	updatedTableField           = querytools.TableField("updated")
)

// XIDTableName is the name of the Xids table in PostgreSQL.
var XIDTableName = "Xids"

// FetchedXIDColumns is the list of columns that are fetched when getting an XidEntry.
var FetchedXIDColumns = []string{
	"Xid", "ParentXid", "Creator", "OriginatingSystem", "Acls", "CreateTime", "UpdateTime",
}

// XIDColumns is the full list of columns in the PostgreSQL Xids table.
var XIDColumns = []string{
	"Xid", "ParentXid", "Creator", "OriginatingSystem", "Endpoint", "Acls", "CreateTime", "UpdateTime",
}

var (
	xidsTableView = querytools.TableView{
		Name:       xidsTableName,
		NameClause: safesql.New("Xids x"),
		Cost:       30,
		Fields: map[querytools.TableField]querytools.Member{
			xidTableField:               {Name: xidTableField, QueryLiteral: safesql.New("x.Xid"), Type: filtervisitor.Integer, Selectable: true, Indexed: true},
			parentXidTableField:         {Name: parentXidTableField, QueryLiteral: safesql.New("x.ParentXid"), Type: filtervisitor.Integer, Selectable: true},
			creatorTableField:           {Name: creatorTableField, QueryLiteral: safesql.New("x.Creator"), Type: filtervisitor.String, Selectable: true},
			originatingSystemTableField: {Name: originatingSystemTableField, QueryLiteral: safesql.New("x.OriginatingSystem"), Type: filtervisitor.Bytes, Selectable: true},
			endpointTableField:          {Name: endpointTableField, QueryLiteral: safesql.New("x.Endpoint"), Type: filtervisitor.String, Selectable: true},
			aclsTableField:              {Name: aclsTableField, QueryLiteral: safesql.New("x.Acls"), Type: filtervisitor.Bytes, Selectable: true},
			createdTableField:           {Name: createdTableField, QueryLiteral: safesql.New("x.CreateTime"), Type: filtervisitor.Timestamp, Selectable: true},
			updatedTableField:           {Name: updatedTableField, QueryLiteral: safesql.New("x.UpdateTime"), Type: filtervisitor.Timestamp, Selectable: true},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: xidTableField, SortOrder: querytools.Asc},
		},
	}

	// XidTables is the list of tables that can be used for Xid queries.
	XidTables = []querytools.TableView{xidsTableView}

	// XidsSelectFields is the list of fields that can be selected for Xid queries.
	XidsSelectFields = []querytools.TableField{
		xidTableField, parentXidTableField, creatorTableField,
		originatingSystemTableField, endpointTableField, aclsTableField,
		createdTableField, updatedTableField,
	}

	// XidsFullTextFields is the list of fields that can be used for full text search for Xid queries.
	XidsFullTextFields = []safesql.TrustedSQLString{}

	// XidsOrderByFields is the list of fields that can be used for ordering Xid queries.
	XidsOrderByFields = []querytools.UserOrderedIndex{
		{TableField: createdTableField},
		{TableField: updatedTableField},
		{TableField: xidTableField},
	}
)
