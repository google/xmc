// Package experimentschema contains the schema for the Spanner tables used by the query generator.
// It defines the schema held in the Experiments table.
package experimentschema

import (
	"api/filter/go/filtervisitor"
	"googlesql/safesql/go/safesql"
	"xmanager_cloud/query_generator/querytools"
)

const (
	experimentsTableName = querytools.TableName("Experiments")

	bitReversedXidTableField = querytools.TableField("bit_reversed_xid")
	xidTableField            = querytools.TableField("xid")

	titleTableField     = querytools.TableField("title")
	creatorTableField   = querytools.TableField("creator")
	priorityTableField  = querytools.TableField("priority")
	urlsTableField      = querytools.TableField("urls")
	tagsTableField      = querytools.TableField("tags")
	notesTableField     = querytools.TableField("notes")
	resourcesTableField = querytools.TableField("resources")

	statusTableField = querytools.TableField("status")

	activeStateTableField = querytools.TableField("active_state")
	launchStateTableField = querytools.TableField("launch_state")

	createdTableField   = querytools.TableField("created")
	updatedTableField   = querytools.TableField("updated")
	completedTableField = querytools.TableField("completed")
)

// ExperimentColumns is the full list of columns in the Experiments table. It is used when fetching
// or inserting into the table. Note that we return the XID column, even though the primary key is
// actually the BitReversedXID column (which should be exposed in the WHERE clause).
var ExperimentColumns = []string{
	"Xid",
	"Title",
	"Creator",
	"Priority",
	"Urls",
	"Tags",
	"Notes",
	"Resources",
	"Status",
	"CreateTime",
	"UpdateTime",
	"CompleteTime",
}

// ExperimentUpdateColumns is the list of columns that can be mutated in the Experiments table.
// It's a subset of the columns in the Experiments table that are mutable. BitReversedXid is the
// primary key and cannot be mutated.
var ExperimentUpdateColumns = []string{
	"BitReversedXid",
	"Title",
	"Priority",
	"Urls",
	"Tags",
	"Notes",
	"UpdateTime",
}

// ExperimentStatusUpdateColumns is the list of columns that can be mutated in the Experiments
// table when updating the status.BitReversedXid is the primary key and cannot be mutated.
var ExperimentStatusUpdateColumns = []string{
	"BitReversedXid",
	"Status",
	"UpdateTime",
	"CompleteTime",
}

// ExperimentResourcesUpdateColumns is the list of columns that can be mutated in the Experiments
// table when updating the resources. BitReversedXid is the primary key and cannot be mutated.
var ExperimentResourcesUpdateColumns = []string{
	"BitReversedXid",
	"Resources",
	"UpdateTime",
}

var (
	// ExperimentTableName is the name of the Experiments table in Spanner.
	ExperimentTableName = "Experiments"

	experimentsTableView = querytools.TableView{
		Name:       experimentsTableName,
		NameClause: safesql.New("Experiments e"),
		Cost:       30,
		Fields: map[querytools.TableField]querytools.Member{
			bitReversedXidTableField: querytools.Member{
				Name:         bitReversedXidTableField,
				QueryLiteral: safesql.New("e.BitReversedXid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			xidTableField: querytools.Member{
				Name:         xidTableField,
				QueryLiteral: safesql.New("e.Xid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			titleTableField: querytools.Member{
				Name:         titleTableField,
				QueryLiteral: safesql.New("e.Title"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			creatorTableField: querytools.Member{
				Name:                   creatorTableField,
				QueryLiteral:           safesql.New("e.Creator"),
				Type:                   filtervisitor.String,
				Selectable:             true,
				DisjunctionOptimizable: true,
			},
			priorityTableField: querytools.Member{
				Name:         priorityTableField,
				QueryLiteral: safesql.New("e.Priority"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
			},
			urlsTableField: querytools.Member{
				Name:            urlsTableField,
				SelectSQLString: safesql.New("e.Urls"),
				QueryLiteral:    safesql.New("UNNEST(e.Urls)"),
				Type:            filtervisitor.ArrayType(&filtervisitor.Bytes),
				Selectable:      true,
			},
			tagsTableField: querytools.Member{
				Name:            tagsTableField,
				SelectSQLString: safesql.New("e.Tags"),
				QueryLiteral:    safesql.New("UNNEST(e.Tags)"),
				Type:            filtervisitor.ArrayType(&filtervisitor.String),
				Selectable:      true,
			},
			notesTableField: querytools.Member{
				Name:         notesTableField,
				QueryLiteral: safesql.New("e.Notes"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			resourcesTableField: querytools.Member{
				Name:         resourcesTableField,
				QueryLiteral: safesql.New("e.Resources"),
				Type:         filtervisitor.Bytes,
				Selectable:   true,
			},
			statusTableField: querytools.Member{
				Name:         statusTableField,
				QueryLiteral: safesql.New("e.Status"),
				Type:         filtervisitor.Bytes,
				Selectable:   true,
			},
			activeStateTableField: querytools.Member{
				Name:         activeStateTableField,
				QueryLiteral: safesql.New("e.ActiveState"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			launchStateTableField: querytools.Member{
				Name:         launchStateTableField,
				QueryLiteral: safesql.New("e.LaunchState"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			createdTableField: querytools.Member{
				Name:         createdTableField,
				QueryLiteral: safesql.New("e.CreateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
			},
			updatedTableField: querytools.Member{
				Name:         updatedTableField,
				QueryLiteral: safesql.New("e.UpdateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
			},
			completedTableField: querytools.Member{
				Name:         completedTableField,
				QueryLiteral: safesql.New("e.CompleteTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			querytools.UserOrderedIndex{TableField: bitReversedXidTableField, SortOrder: querytools.Asc},
		},
	}

	// ExperimentTables is the list of tables that can be used for Experiment queries.
	ExperimentTables = []querytools.TableView{experimentsTableView}

	// ExperimentsSelectFields is the list of fields that can be selected for Experiments queries.
	ExperimentsSelectFields = []querytools.TableField{
		bitReversedXidTableField,
		xidTableField,
		creatorTableField,
		priorityTableField,
		titleTableField,
		urlsTableField,
		tagsTableField,
		notesTableField,
		resourcesTableField,
		statusTableField,
		activeStateTableField,
		launchStateTableField,
		createdTableField,
		updatedTableField,
		completedTableField,
	}

	// ExperimentsFullTextFields is the list of fields that can be used for full text search for Experiments queries.
	ExperimentsFullTextFields = []safesql.TrustedSQLString{safesql.New("Title_Tokens"), safesql.New("Tags_Tokens"), safesql.New("Creator_Tokens")}

	// ExperimentsOrderByFields is the list of fields that can be used for ordering Experiments queries.
	ExperimentsOrderByFields = []querytools.UserOrderedIndex{
		querytools.UserOrderedIndex{TableField: createdTableField},
		querytools.UserOrderedIndex{TableField: updatedTableField},
		querytools.UserOrderedIndex{TableField: completedTableField},
		querytools.UserOrderedIndex{TableField: xidTableField},
	}
)
