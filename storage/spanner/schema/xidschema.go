// Package xidschema contains the schema for the Spanner tables used by the query generator.
// It defines the schema held in the Xids table.
package xidschema

import (
	"api/filter/go/filtervisitor"
	"googlesql/safesql/go/safesql"
	"xmanager_cloud/query_generator/querytools"
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
			bitReversedXidTableField: querytools.Member{
				Name:         bitReversedXidTableField,
				QueryLiteral: safesql.New("x.BitReversedXid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			xidTableField: querytools.Member{
				Name:         xidTableField,
				QueryLiteral: safesql.New("x.Xid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			parentXidTableField: querytools.Member{
				Name:         parentXidTableField,
				QueryLiteral: safesql.New("x.ParentXid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			creatorTableField: querytools.Member{
				Name:         creatorTableField,
				QueryLiteral: safesql.New("x.Creator"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			originatingSystemTableField: querytools.Member{
				Name:         originatingSystemTableField,
				QueryLiteral: safesql.New("x.OriginatingSystem"),
				Type:         filtervisitor.Bytes,
				Selectable:   true,
			},
			endpointTableField: querytools.Member{
				Name:         endpointTableField,
				QueryLiteral: safesql.New("x.Endpoint"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			aclsTableField: querytools.Member{
				Name:         aclsTableField,
				QueryLiteral: safesql.New("x.Acls"),
				Type:         filtervisitor.Bytes,
				Selectable:   true,
			},
			createdTableField: querytools.Member{
				Name:         createdTableField,
				QueryLiteral: safesql.New("x.CreateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
			},
			updatedTableField: querytools.Member{
				Name:         updatedTableField,
				QueryLiteral: safesql.New("x.UpdateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			querytools.UserOrderedIndex{TableField: bitReversedXidTableField, SortOrder: querytools.Asc},
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
		querytools.UserOrderedIndex{TableField: createdTableField},
		querytools.UserOrderedIndex{TableField: updatedTableField},
		querytools.UserOrderedIndex{TableField: xidTableField},
	}
)
