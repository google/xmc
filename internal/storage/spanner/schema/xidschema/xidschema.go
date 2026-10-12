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
// Package xidschema contains the schema for the Spanner tables used by the query generator.
// It defines the schema held in the Xids table.
package xidschema

import (
	"github.com/google/xmc/api/filter/go/filtervisitor"
	"github.com/google/xmc/internal/query_generator/querytools"
	"github.com/google/xmc/internal/safesql"
)

const (
	xidsTableName = querytools.TableName("Xids")

	bitReversedXidTableField = querytools.TableField("bit_reversed_xid")
	xidTableField            = querytools.TableField("xid")
	parentXidTableField      = querytools.TableField("parent_xid")

	creatorTableField           = querytools.TableField("creator")
	originatingSystemTableField = querytools.TableField("origin_system")
	endpointTableField          = querytools.TableField("endpoint")
	aclsTableField              = querytools.TableField("acls")

	createdTableField = querytools.TableField("created")
	updatedTableField = querytools.TableField("updated")
)

// XIDTableName is the name of the Xids table in Spanner.
var XIDTableName = "Xids"

// FetchedXIDColumns is the list of columns that are fetched when getting an XidEntry.
// This is a subset of the columns in the Xids table that excludes any STORED columns.
var FetchedXIDColumns = []string{
	"Xid",
	"ParentXid",
	"Creator",
	"OriginatingSystem",
	"Acls",
	"CreateTime",
	"UpdateTime",
}

var (
	xidsTableView = querytools.TableView{
		Name:       xidsTableName,
		NameClause: safesql.New("Xids x"),
		Cost:       30,
		Fields: map[querytools.TableField]querytools.Member{
			bitReversedXidTableField: {
				Name:         bitReversedXidTableField,
				QueryLiteral: safesql.New("x.BitReversedXid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			xidTableField: {
				Name:         xidTableField,
				QueryLiteral: safesql.New("x.Xid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			parentXidTableField: {
				Name:         parentXidTableField,
				QueryLiteral: safesql.New("x.ParentXid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			creatorTableField: {
				Name:         creatorTableField,
				QueryLiteral: safesql.New("x.Creator"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			originatingSystemTableField: {
				Name:         originatingSystemTableField,
				QueryLiteral: safesql.New("x.OriginatingSystem"),
				Type:         filtervisitor.Bytes,
				Selectable:   true,
			},
			endpointTableField: {
				Name:         endpointTableField,
				QueryLiteral: safesql.New("x.Endpoint"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			aclsTableField: {
				Name:         aclsTableField,
				QueryLiteral: safesql.New("x.Acls"),
				Type:         filtervisitor.Bytes,
				Selectable:   true,
			},
			createdTableField: {
				Name:         createdTableField,
				QueryLiteral: safesql.New("x.CreateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
			},
			updatedTableField: {
				Name:         updatedTableField,
				QueryLiteral: safesql.New("x.UpdateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: bitReversedXidTableField, SortOrder: querytools.Asc},
		},
	}

	// XidTables is the list of tables that can be used for Xid queries.
	XidTables = []querytools.TableView{xidsTableView}

	// XidsSelectFields is the list of fields that can be selected for Xid queries.
	XidsSelectFields = []querytools.TableField{
		bitReversedXidTableField,
		xidTableField,
		parentXidTableField,
		creatorTableField,
		originatingSystemTableField,
		endpointTableField,
		aclsTableField,
		createdTableField,
		updatedTableField,
	}

	// XidsFullTextFields is the list of fields that can be used for full text search for Xid queries.
	// It's empty for now, but we can add fields here if we want to support full text search.
	XidsFullTextFields = []safesql.TrustedSQLString{}

	// XidsOrderByFields is the list of fields that can be used for ordering Xid queries.
	XidsOrderByFields = []querytools.UserOrderedIndex{
		{TableField: createdTableField},
		{TableField: updatedTableField},
		{TableField: xidTableField},
	}
)
