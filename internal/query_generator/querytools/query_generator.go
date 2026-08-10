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
// Package querytools contains a query generation library for spanner queries.
// It supports user-defined filtering using the AIP-160 syntax, query
// planning to select the optimal table (or tables with joins) to satisfy the
// requested set of fields, and pagination.
package querytools

import (
	"fmt"
	"strconv"
	"strings"

	"api/filter/go/filtervisitor"
	"go/log"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/protobuf/v2/proto/proto"
	"googlesql/safesql/go/safesql"
	"task/go/status"
	"xmanager_cloud/query_generator/equalityvisitor"
	"xmanager_cloud/query_generator/filter/queryfilter"

	fpb "api/filter/filter_expression_go_proto"
)

// QueryGenerator defines a generator containing the table schema and a space
// of allowed operations (which fields are allowed in user query strings, what
// custom functions are allowed, etc.). The generator can be reused to produce
// a variety of queries within this schema.
type QueryGenerator interface {
	// GenerateQuery generates a query that selects the fields listed in
	// selectFields, optionally with filters from baseFilterExp and
	// userFilterExp, optionally with the page token field values defined
	// in ptFields, with LIMIT pageSize and OFFSET offset, optionally with
	// preamble inserted before the SELECT keyword.
	// userOrderBy, optionally adds the specified fields in the ORDER BY
	// clause of the query.
	// The query will use the most efficient table and joins to fulfill
	// the requested filters and select fields, based on the schema
	// metadata provided.
	// It returns the query, a map indicating the indices at which page
	// token fields can be found in the select list, an Unnester instance
	// that can be used to patch any unnested fields back into repeated
	// proto fields, and any error that occurred.
	GenerateQuery(selectFields []TableField, fullTextFields []safesql.TrustedSQLString, baseFilterExp *fpb.Expression, userFilterExp *fpb.Expression, userOrderBy []UserOrderedIndex, pageSize, offset int, ptFields map[TableField]any, preamble *safesql.TrustedSQLString) (*spanner.Statement, map[TableField]int, Unnester, error)

	// LastFromTable returns the table that was last selected from. This is filled in after calling
	// GenerateQuery(). This is generally to be used for logging or metric purposes by the client.
	LastFromTable() TableName

	// Tables returns the tables that can be used for queries.
	Tables() []TableView
}

// TableField represents a field in a base table. While the way we access this
// field through distinct TableViews may change, the TableField gives us a way
// to refer to this field in a view agnostic way.
type TableField string

// String returns the string representation of the TableField.
func (t TableField) String() string {
	return string(t)
}

// TableName is a string that is used to refer to a spanner table or one of its
// indices.
type TableName string

// SortOrder specifies the sort order of the field.
type SortOrder int

const (
	// None specifies no sort order.
	None SortOrder = iota

	// Asc is the ascending sort order.
	Asc

	// Desc is the descendig sort order.
	Desc
)

// Member holds the query literal that may be used to access a given field, its
// primitive type, and metadata defining how the field can be used.
type Member struct {
	// Name is the name that we'll use to refer to this Member from a table
	// agnostic view. Must be set for fields being accessed from user
	// queries as we may have looked up this Member based on a string that
	// is an alias, needing a connection back to the TableField in the
	// schema. Otherwise, optional but recommended.
	Name TableField
	// SelectSQLString is the SQL string used to select this Member in the
	// SELECT clause.
	SelectSQLString safesql.TrustedSQLString
	// QueryLiteral is the literal used to access this Member in SQL.
	QueryLiteral safesql.TrustedSQLString
	// Derived indicates whether this field is a base table field or whether
	// it is modified in some way.
	Derived bool
	// Type is the spanner type of this Member using the enums defined by
	// the filter parsing library.
	// If this field is not set, the Member will not be usable in filter
	// expressions but may still be selected.
	Type filtervisitor.PrimType
	// UnnestClause contains all of the SQL clauses and metadata required
	// to unnest this member in the query and patch it back together later.
	UnnestClause *Unnest
	// Whether this member causes the query to need a GROUP BY. This will
	// be true if the member requires any aggregation.
	NeedGroupBy bool
	// Whether this member needs to be omitted from the GROUP BY clause.
	// It may be the case that NeedGroupBy && OmitFromGroupBy; this means
	// the member causes the *query* to need aggregation but should not
	// itself be listed in the GROUP BY.
	OmitFromGroupBy bool
	// Whether this member may be returned in a SELECT statement.
	Selectable bool
	// Whether this member is indexed in the spanner table.
	Indexed bool
	// NullFiltered indicates a field is null filtered, meaning its row will not exist in the table if
	// its value is null regardless of the values of other columns in the row. This helps the query
	// generator decide if it can select from a table given a query.
	//
	// It currently allows selection from a null filtered table if the query filters on *any* of its
	// null filtered fields. It should be only allowing this if the query filters on *all* of a
	// table's null filtered fields using conjunctions. But due to limitations, we can't check this
	// right now.
	//
	// So you should only use null filtered tables with the query generator if your system can
	// guarantee that, for null filtered fields in a table, if your system sets a value for one of
	// them, it will always set a value for all of them. Otherwise you may get results with missing
	// rows for some queries.
	NullFiltered bool
	// DisjunctionOptimizable indicates that if a filter contains multiple disjunctions on this field,
	// it will be translated to an IN UNNEST clause by the query generator. The values will be bound
	// to the query as an array. For example, "field1 = value1 OR field2 = value2" will be translated
	// to Spanner SQL as "field1 IN UNNEST (@array)" where @array is [value1, value2]. This currently
	// only works with string type Spanner columns. I.e., in the example above, field1 in the Spanner
	// query must be a string type.
	DisjunctionOptimizable bool
}

// MemberMap takes a query and a map of name=>Member and produces a MemberFunc
// for use by the queryfilter expression parser that looks up names, appends
// the corresponding QueryLiteral to the query and returns the member's
// primitive type. If a given name in the expression is unrecognized, it
// returns an error.
func MemberMap(q *spanner.Statement, members map[string]Member) queryfilter.MemberFunc {
	return func(name []string) (safesql.TrustedSQLString, filtervisitor.PrimType, error) {
		m, err := fetchMember(name, members)
		if err != nil {
			return safesql.New(""), filtervisitor.Invalid, err
		}
		return m.QueryLiteral, m.Type, nil
	}
}

// PhraseMap takes a map of type kinds to a map of original string and new strings. It is used to rewrite restriction expressions where the literal passed by the user needs to be rewritten.
func PhraseMap(rewrites map[string]map[string]string) queryfilter.RestrictionTranslate {
	return func(r *fpb.Restriction) *fpb.Restriction {
		field, phrase, ok := extractFieldAndPhrase(r)
		if !ok {
			return r
		}

		m, ok := rewrites[field]
		if !ok {
			return r
		}

		newphrase, ok := m[phrase]
		if !ok {
			return r
		}
		out := &fpb.Restriction{}
		proto.Merge(out, r)
		if r.GetLhs().GetPhrase() != nil {
			out.GetLhs().GetPhrase().GetValues()[0] = newphrase
		} else {
			out.GetRhs().GetPhrase().GetValues()[0] = newphrase
		}
		return out
	}
}

func extractFieldAndPhrase(r *fpb.Restriction) (string, string, bool) {
	if r.GetLhs().GetValue() != "" && r.GetRhs().GetPhrase() != nil {
		field := r.GetLhs().GetValue()
		phrase := ""
		if values := r.GetRhs().GetPhrase().GetValues(); len(values) > 0 {
			phrase = values[0]
		}
		return field, phrase, true
	}
	if r.GetRhs().GetValue() != "" && r.GetLhs().GetPhrase() != nil {
		field := r.GetRhs().GetValue()
		phrase := ""
		if values := r.GetLhs().GetPhrase().GetValues(); len(values) > 0 {
			phrase = values[0]
		}
		return field, phrase, true
	}
	return "", "", false
}

func fetchMember(name []string, members map[string]Member) (Member, error) {
	n := strings.Join(name, ".")
	m, ok := members[n]
	if !ok {
		return m, status.Errorf(status.InvalidArgument, "unsupported querytools.Member name in query: %q", n)
	}

	return m, nil
}

// DisjunctionMemberMap takes an expression and a map of name=>Member and produces a MemberFunc
// for use by the queryfilter expression parser that looks up names, and
// returns the found member. If a given name in the expression is unrecognized, it
// returns an error.
func DisjunctionMemberMap(members map[string]Member) queryfilter.DisjunctionFunc {
	return func(name []string) (bool, safesql.TrustedSQLString, error) {
		m, err := fetchMember(name, members)
		if err != nil {
			return false, safesql.New(""), err
		}
		return m.DisjunctionOptimizable, m.QueryLiteral, nil
	}
}

// Function represents a named function that can be evaluated against
// expression arguments.
type Function func(*queryfilter.Spanner, []*fpb.Expression) (filtervisitor.PrimType, error)

// FunctionMap takes a query and a map of name=>Function and produces a
// FunctionFunc for use by the queryfilter expression parser that looks up
// function names encountered in the expression, calls the corresponding
// function with the parsed arguments, and returns the result.
// If a given function name is unrecognized, it returns an error.
func FunctionMap(functions map[string]Function) queryfilter.FunctionFunc {
	return func(fn string, s *queryfilter.Spanner, args []*fpb.Expression) (filtervisitor.PrimType, error) {
		f, ok := functions[fn]
		if !ok {
			return filtervisitor.Invalid, status.Errorf(status.InvalidArgument, "unsupported function name in query: %q", fn)
		}
		return f(s, args)
	}
}

// UserOrderedIndex represents the fields that will be used in the ORDER BY clause
// of the query.
type UserOrderedIndex struct {
	// TableField is the name of the field that the table should be ordered by.
	TableField TableField

	// SortOrder is the order that the fields should be sorted in (ASC or DESC).
	SortOrder SortOrder
}

// TableView represents a view on a Spanner table. This may be the base table
// itself or a view of an index on the table.
//
// Given the following spanner schema:
// CREATE TABLE Albums (
//
//	SingerId     INT64 NOT NULL,
//	AlbumId      INT64 NOT NULL,
//	AlbumTitle   STRING(MAX),
//	ReleaseDate  DATE
//
// ) PRIMARY KEY (SingerId, AlbumId),
//
// CREATE TABLE Singers (
//
//	 SingerId INT64 NOT NULL,
//	 SingerName STRING(MAX)
//	) PRIMARY KEY (SingerId)
//
// CREATE INDEX AlbumsByAlbumTitle ON Albums(SingerId, AlbumTitle)
//
// You would want to define three TableViews. The first would be defined as:
// albums = TableView{
//
//	  Name:       TableName("Albums"),
//	  NameClause: safesql.New("Albums a"),
//	  Cost:       30,
//	  Fields:     map[querytools.TableField]Member{
//		SingerIDTableField:    Member{
//			Name:         SingerIDTableField,
//			QueryLiteral: safesql.New("a.SingerId"),
//			Type:         filtervisitor.Integer,
//			Selectable:   true,
//			Indexed:      true,
//		},
//		AlbumIDTableField:     Member{
//			Name:         AlbumIDTableField,
//			QueryLiteral: safesql.New("a.AlbumId"),
//			Type:         filtervisitor.Integer,
//			Selectable:   true,
//			Indexed:      true,
//		},
//		AlbumTitleTableField:  Member{
//			Name:         AlbumTitleTableField,
//			QueryLiteral: safesql.New("a.AlbumTitle"),
//			Type:         filtervisitor.String,
//			Selectable:   true,
//		},
//		ReleaseDateTableField: Member{
//			Name:         ReleaseDateTableField,
//			QueryLiteral: safesql.New("a.ReleaseDate"),
//			Type:         filtervisitor.Timestamp,
//			Selectable:   true,
//		},
//	  },
//	  Relationships: []querytools.Join{
//		querytools.Join{
//			singers,
//			safesql.New(" JOIN Singers s ON (a.SingerId = s.SingerId)"),
//		},
//	  },
//	  OrderedIndexTableFields: []TableField{
//		SingerIDTableField,
//		AlbumIDTableField,
//	  }
//
// }
//
// Following the same example, you would want a TableView for Singers and
// AlbumsByAlbumTitle that include relevant indexed fields and fields that can
// be selected from those tables.
type TableView struct {
	// Name is a constant name of this Table; it doesn't have to match the
	// actual spanner table name, rather it must be used to consistently
	// reference this Table across all views.
	Name TableName
	// NameClause is the SQL clause used following the FROM statement for
	// this table.
	NameClause safesql.TrustedSQLString
	// Cost is the expense of querying this Table, larger Tables are
	// expected to have higher cost.
	Cost int32
	// Fields is a map of Members by TableField name that can be selected
	// and/or queried on from this TableView with the QueryLiteral that
	// should be used to reference them in this view's spanner table,
	// and any other metadata about how they participate in the query.
	Fields map[TableField]Member
	// Relationships are an array of joins that can be made to other Tables
	// from this Table.
	Relationships []Join
	// OrderedIndicesWithSortDirection contains the list of all Fields having
	// Indexed set, in the order they must appear in the ORDER BY clause.
	// In general, this must match the ordering in the spanner primary key
	// or index.
	OrderedIndicesWithSortDirection []UserOrderedIndex
	// OrderedIndexTableFields is deprecated. Use OrderedIndicesWithSortDirection.
	// An error will be generated if both OrderedIndicesWithSortDirection and OrderedIndexTableFields
	// are not empty. Otherwise, if OrderedIndexTableFields is not empty,
	// the default ASC sort direction will be applied to fields.
	OrderedIndexTableFields []TableField
	// OrderByFieldsWithSortDirection specifies the default set of fields to order by for queries made
	// against this table. This is optional. If not set, queries will order by all fields in
	// OrderedIndexTableFields. This override is useful for DISTINCT_ARRAY_ELEMENT tables where
	// aggregation is needed to control the level at which aggregation happens. The downside is that
	// page tokens are no longer necessarily efficient if they're not made from all the fields in
	// OrderedIndexTableFields.
	OrderByFieldsWithSortDirection []UserOrderedIndex
	// OrderByFields is deprecated. Use OrderByFieldsWithSortDirection.
	// An error will be generated if both OrderByFieldsWithSortDirection and OrderByFields
	// are not empty. Otherwise, if OrderByFields is not empty, the default ASC sort direction
	// will be applied to fields.
	OrderByFields []TableField
}

// Join represents how Tables can be joined together.
type Join struct {
	// T is the Table that can be joined from the TableView referencing
	// this join.
	T TableView
	// Clause is the SQL clause that is used for this join.
	Clause safesql.TrustedSQLString
}

// queryGen is an implementation of QueryGenerator.
type queryGen struct {
	// tables are the tables available to this QueryGenerator, containing the schema definitions in
	// terms of fields, relationships, etc.
	tables []TableView

	// members is a map from string literals allowed to appear in user filters as field names, to the
	// Members that they correspond to.
	members map[string]Member

	// baseFilters is a list of fields needed in filters that aren't specifically requested by users,
	// but required to handle user requests. An example of this might be a projectID for Cloud
	// products - so that we only return results for the user's project; another example would be
	// filters needed to implement pagination for a query.
	baseFilters []TableField

	// functions are custom functions allowed in the filters.
	functions map[string]Function

	// optimizeFilters toggles whether disjunctions on the same field in filters should be optimized
	// to use an IN clause in Spanner SQL.
	optimizeFilters bool

	// optimizeOrderBy toggles whether the user order by:
	// - Disallows derived fields.
	// - Disallows fields with the incorrect sort order (e.g., if the user order by is "FOO, BAR",
	//   then it will only match tables where orderedIndexTableFields starts with "FOO, BAR".
	optimizeOrderBy bool

	options options

	lastFromTable TableName
}

// Tables returns the tables available to this QueryGenerator.
func (q *queryGen) Tables() []TableView {
	return q.tables
}

type options struct {
	rewrites           map[string]map[string]string
	deduplicateSelects bool
}

// Option is an optional argument to generating queries.
type Option interface {
	set(*options)
}

type optionwrap func(*options)

func (o optionwrap) set(out *options) {
	o(out)
}

// EnumRewrites indicates the enum rewrites for each type.
func EnumRewrites(rewrites map[string]map[string]string) Option {
	return optionwrap(func(out *options) {
		out.rewrites = rewrites
	})
}

// DeduplicateSelects will prevent any SELECT's from containing duplicate fields
func DeduplicateSelects(deduplicate bool) Option {
	return optionwrap(func(out *options) {
		out.deduplicateSelects = deduplicate
	})
}

// NewQueryGenerator takes a list of TableViews defining the schema of
// available tables, a map (possibly empty) defining what members can be
// specified in user-defined filters and what name(s) they can be referenced
// by, a list of filters that aren't specifically requested by users but
// required to handle user requests, and a map from function names allowed in
// queries to the function implementations.
// For convenience, if members is nil it will be populated with all fields from
// all tables, referenced by the names defined in the field metadata.
// To disallow all fields from user queries, pass the empty map instead.
// The baseFilters must be Indexed and Selectable.
func NewQueryGenerator(tables []TableView, members map[string]Member, baseFilters []TableField, functions map[string]Function, opts ...Option) (QueryGenerator, error) {
	return NewOptimizableGenerator(tables, members, baseFilters, functions, false, false, opts...)
}

// NewOptimizableGenerator is the same as NewQueryGenerator, but offers the option to optimize your filters by
// allowing disjuctions on the same column to be converted to IN predicates rather than a series of OR predicates.
func NewOptimizableGenerator(tables []TableView, members map[string]Member, baseFilters []TableField, functions map[string]Function, optimizeFilter, optimizeOrderBy bool, opts ...Option) (QueryGenerator, error) {
	updatedTables := make([]TableView, len(tables))
	for i, table := range tables {
		ut, err := addSortDirectionToOrderedFieldsAndIndices(table)
		if err != nil {
			return nil, err
		}
		updatedTables[i] = ut
	}
	if members == nil {
		members = make(map[string]Member)
		for _, t := range updatedTables {
			for k, v := range t.Fields {
				members[string(k)] = v
			}
		}
	}

	o := options{}
	for _, opt := range opts {
		opt.set(&o)
	}
	return &queryGen{
		tables:          updatedTables,
		members:         members,
		baseFilters:     baseFilters,
		functions:       functions,
		optimizeFilters: optimizeFilter,
		optimizeOrderBy: optimizeOrderBy,
		options:         o,
	}, nil
}

// addSortDirectionToOrderedFieldsAndIndices creates a copy of the input table and then
// moves the fields of OrderedIndexTableFields and OrderByFields
// to OrderedIndicesWithSortDirection and OrderByFieldsWithSortDirection respectively
// by adding ASC sort direction. Only one of OrderedIndexTableFields and
// OrderedIndicesWithSortDirection may be present, same for OrderByFields and
// OrderByFieldsWithSortDirection. Otherwise, an error is generated.
func addSortDirectionToOrderedFieldsAndIndices(t TableView) (TableView, error) {
	table := t
	if len(table.OrderedIndexTableFields) != 0 {
		if len(table.OrderedIndicesWithSortDirection) != 0 {
			return TableView{}, status.Errorf(status.InvalidArgument, "only one of OrderedIndexTableFields and OrderedIndicesWithSortDirection may be provided for table %v", table.Name)
		}
		table.OrderedIndicesWithSortDirection = make([]UserOrderedIndex, len(table.OrderedIndexTableFields))
		for i, f := range table.OrderedIndexTableFields {
			table.OrderedIndicesWithSortDirection[i] = UserOrderedIndex{TableField: f, SortOrder: Asc}
		}
		table.OrderedIndexTableFields = []TableField{}
	}
	if len(table.OrderByFields) != 0 {
		if len(table.OrderByFieldsWithSortDirection) != 0 {
			return TableView{}, status.Errorf(status.InvalidArgument, "only one of OrderByFields and OrderByFieldsWithSortDirection may be provided for table %v", table.Name)
		}
		table.OrderByFieldsWithSortDirection = make([]UserOrderedIndex, len(table.OrderByFields))
		for i, f := range table.OrderByFields {
			table.OrderByFieldsWithSortDirection[i] = UserOrderedIndex{TableField: f, SortOrder: Asc}
		}
		table.OrderByFields = []TableField{}
	}
	return table, nil
}

// GenerateQuery generates a spanner SQL query with the following contents:
// SELECT: returns the selectFields, plus any fields needed for a page token
// Note that it's possible for a query to SELECT a field more than once, because of a lack of
// deduping between explicitly selected fields and fields needed for pagination.
// FROM: a base table selected as the one that can satisfy the query with the
// lowest cost, and possibly some joins to fulfill additional select fields
// WHERE: applies the baseFilterExp condition, the userFilterExp condition,
// and if ptFields is non-empty the conditions to continue from those page token
// values
// GROUP BY, ORDER BY: as implied by the metadata of the fields used
// LIMIT, OFFSET: according to pageSize and offset, respectively
//
// It returns the query, a map of TableField to index so that the caller can
// build the page token, an Unnester that can patch up any repeated fields that
// were selected, and any error that occurred.
//
//nolint:revive // Why: Spanner AST query generator returning (*spanner.Statement, map[TableField]int, Unnester, error).
func (q *queryGen) GenerateQuery(selectFields []TableField, fullTextFields []safesql.TrustedSQLString, baseFilterExp *fpb.Expression, userFilterExp *fpb.Expression, userOrderBy []UserOrderedIndex, pageSize, offset int, ptFields map[TableField]any, preamble *safesql.TrustedSQLString) (*spanner.Statement, map[TableField]int, Unnester, error) {
	// Captures the names referenced in userFilterExp and the fields that
	// we're mapping them to.
	userFilters := map[string]TableField{}

	// equalityFilters will contain the set of fields used in equality
	// statements in userFilterExp, plus all base filters which are always
	// equality comparisons. Having this list allows us to determine which
	// fields should be included in the page token for paginated queries.
	// For example, if the table has primary key A,B,C but we have a base
	// filter A=2 and a user filter C=3, the page token only needs to
	// contain B.
	equalityFilters := map[TableField]bool{}

	// Do a walk over the userFilterExp expression so that we can scout
	// what Members it accesses. We use a "dummy query" here to recycle the
	// logic in qfcs.Spanner that walks the expression and calls us
	// back on Members.
	if err := q.scoutUserFilterMembers(userFilterExp, fullTextFields, userFilters, equalityFilters); err != nil {
		return nil, nil, nil, err
	}

	// Add all of the base filter fields as equality filters. The base filters are assumed to be fixed
	// in the client code, so we do not validate that they are actually equality conditions.
	for _, bf := range q.baseFilters {
		equalityFilters[bf] = true
	}

	candidateTables := map[TableName]*candidateTable{}

	// Find candidate tables for the user order by and base filters.
	for _, t := range q.tables {
		ct := &candidateTable{t: t}

		valid, err := checkCandidateTableOrderBy(t, userOrderBy, q.optimizeOrderBy)
		if err != nil {
			return nil, nil, nil, err
		}
		if !valid {
			continue
		}

		// If there are no base filters, this table is a candidate.
		if len(q.baseFilters) == 0 {
			candidateTables[t.Name] = ct
			continue
		}

		// Otherwise we need to ensure this table satisfies base filters.
		allFiltersFound := true
		for _, f := range q.baseFilters {
			if _, ok := t.Fields[f]; !ok {
				allFiltersFound = false
				break
			}
		}
		if allFiltersFound {
			candidateTables[t.Name] = ct
		}
	}

	if len(candidateTables) == 0 {
		return nil, nil, nil, status.Errorf(status.FailedPrecondition, "no tables can satisfy base filters %v", q.baseFilters)
	}

	// Iterate over user filters removing tables that don't have the fields to satisfy filters.
	log.V(1).Infof("We have user filters %v and available tables %v. Removing tables that don't have the fields to satisfy filters...", filterNames(userFilters), tableNames(candidateTables))
	for _, filter := range userFilters {
		removeTableIfMissingField(filter, candidateTables)
	}
	log.V(1).Infof("We now have available tables %v", tableNames(candidateTables))

	if len(candidateTables) == 0 {
		return nil, nil, nil, status.Errorf(status.FailedPrecondition, "no tables can satisfy all user filters %v and base filters %v", userFilters, q.baseFilters)
	}

	// Iterate over fields in the requested page token to ensure that we select the same table as
	// previously selected.
	log.V(1).Infof("We have page token fields %v and available tables %v. Removing tables that don't satisfy the page token...", pageTokenNames(ptFields), tableNames(candidateTables))
	for k := range ptFields {
		removeTableIfMissingField(k, candidateTables)
	}
	log.V(1).Infof("We now have available tables %v", tableNames(candidateTables))

	if len(candidateTables) == 0 {
		return nil, nil, nil, status.Errorf(status.FailedPrecondition, "no tables can satisfy all user filters %v, base filters %v, and page token filters %v", userFilters, q.baseFilters, ptFields)
	}

	// Remove tables that have null filtered fields in them that aren't satisfied by the base or
	// user filters.
	log.V(1).Info("Removing tables with null filtered fields that aren't satisfied by the base or user filters...")
	removeIfNullFilterUnsatisfied(candidateTables, q.baseFilters, userFilters)
	log.V(1).Infof("We now have available tables %v", tableNames(candidateTables))

	if len(candidateTables) == 0 {
		return nil, nil, nil, status.Errorf(status.FailedPrecondition, "no tables can satisfy all user filters %v, base filters %v, and page token filters %v", userFilters, q.baseFilters, ptFields)
	}

	// For the remaining candidate tables, update the heuristic for number of filter fields that make
	// up the prefix of each candidate table's index.
	log.V(1).Infof("Updating the heuristic for number of filter fields that make up the prefix of each candidate table's index...")
	updateNumFiltersIndexed(candidateTables, q.baseFilters, userFilters)

	// Determine the best table to select from.
	log.V(1).Infof("Finding the best table to select from based on available tables %v", tableNames(candidateTables))
	var bestTable *candidateTable
	for _, ct := range candidateTables {
		if bestTable == nil {
			log.V(1).Infof("No current best table, setting %q as the best table", ct.t.Name)
			bestTable = ct
			continue
		}

		if ct.numFiltersIndexed > bestTable.numFiltersIndexed {
			log.V(1).Infof("Table %q has ordered indexes on %d filter fields whereas current best table %q has %d, setting %q as the best table", ct.t.Name, ct.numFiltersIndexed, bestTable.t.Name, bestTable.numFiltersIndexed, ct.t.Name)
			bestTable = ct
			continue
		}

		if ct.numFiltersIndexed == bestTable.numFiltersIndexed && ct.t.Cost < bestTable.t.Cost {
			log.V(1).Infof("Table %q and current best table %q have ordered indexes on the same number of filter fields, but table %q costs less (%d versus %d), setting table %q as best table", ct.t.Name, bestTable.t.Name, ct.t.Name, ct.t.Cost, bestTable.t.Cost, ct.t.Name)
			bestTable = ct
		}

		log.V(1).Infof("Table %q isn't better than current best table %q, skipping it", ct.t.Name, bestTable.t.Name)
	}
	fromTable := bestTable.t
	q.lastFromTable = fromTable.Name

	// Create a set to lookup whether a field is a selectField.
	sfs := make(map[TableField]bool, len(selectFields))
	for _, sf := range selectFields {
		sfs[sf] = true
	}
	var selectMembers []Member
	// Create a list of fields to use for join selection. We need to find
	// all of the selectFields and all of the baseFilters.
	joinFields := make([]TableField, len(selectFields))
	copy(joinFields, selectFields)
	for _, bf := range q.baseFilters {
		if !sfs[bf] {
			joinFields = append(joinFields, bf)
		}
	}

	// Iterate over joinFields to figure out what joins we need to perform.
	log.V(1).Infof("We have the following join fields: %v", joinFields)
	var joins []Join
	for _, sf := range joinFields {
		log.V(1).Infof("Looking for join field %q", sf)
		m, ok := fromTable.Fields[sf]
		if ok && m.Selectable {
			// This field is selectable in fromTable.
			log.V(1).Infof("Join field %q is selectable from table %q", sf, fromTable.Name)
			if sfs[sf] {
				log.V(1).Infof("Join field %q is in select fields, adding it to select members", sf)
				selectMembers = append(selectMembers, m)
			}
			continue
		}

		found := false
		log.V(1).Infof("Join field %q is not selectable from table %q, seeing if it's available in any of the existing joins %v", sf, fromTable.Name, relationshipTableNames(joins))
		for _, jt := range joins {
			// Is it available in any existing joins?
			m, ok = jt.T.Fields[sf]
			if ok && m.Selectable {
				log.V(1).Infof("Join field %q is selectable from join table %q", sf, jt.T.Name)
				if sfs[sf] {
					log.V(1).Infof("Join field %q is in select fields, adding it to select members", sf)
					selectMembers = append(selectMembers, m)
				}
				found = true
				break
			}
		}
		if !found {
			var err error
			found, err = resolveJoinFieldFromRelationships(sf, sfs, fromTable, &joins, &selectMembers)
			if err != nil {
				return nil, nil, nil, err
			}
		}
	}

	// Iterate over base and user filter fields to get their members to create an unnester with any
	// unnest fields while we're iterating.
	unr := unnester{
		seen: make(map[*Unnest]bool),
	}
	filters := make(map[string]Member)
	for _, f := range q.baseFilters {
		m, ok := fromTable.Fields[f]
		if !ok {
			// At this point we should have already thrown an error
			// if we have no single Table that has all filters.
			return nil, nil, nil, status.Errorf(status.FailedPrecondition, "selected from table %q cannot satisfy filter %+v", fromTable.Name, f)
		}
		filters[string(f)] = m
		unr.seen[m.UnnestClause] = true
	}
	for n, f := range userFilters {
		m, ok := fromTable.Fields[f]
		if !ok {
			// At this point we should have already thrown an error
			// if we have no single Table that has all filters.
			return nil, nil, nil, status.Errorf(status.FailedPrecondition, "selected from table %q cannot satisfy user filter %+v", fromTable.Name, f)
		}
		filters[n] = m
		unr.seen[m.UnnestClause] = true
	}

	// Proceed to building the query statement.

	// Start with the preamble and the SELECT clause.
	var query *spanner.Statement
	selectedMembers := make(map[safesql.TrustedSQLString]int)
	if preamble != nil {
		newStmt := spanner.NewStatement(preamble.String() + " SELECT ")
		query = &newStmt
	} else {
		newStmt := spanner.NewStatement("SELECT ")
		query = &newStmt
	}

	selectIdx := 0
	needGroupBy := false
	for _, sf := range selectMembers {
		if selectIdx > 0 {
			query.SQL = query.SQL + ", "
		}
		if sf.NeedGroupBy {
			needGroupBy = true
		}

		var selectSQL safesql.TrustedSQLString
		if sf.SelectSQLString != safesql.EmptyString() {
			selectSQL = sf.SelectSQLString
		} else {
			selectSQL = sf.QueryLiteral
		}

		query.SQL = query.SQL + selectSQL.String()

		selectedMembers[selectSQL] = selectIdx
		selectIdx++
	}

	obs := make(map[TableField]bool, len(userOrderBy))
	var orderByFields []UserOrderedIndex
	for _, ob := range userOrderBy {
		orderByFields = append(orderByFields, ob)
		obs[ob.TableField] = true
	}

	// If this table has specified an order by override, use that instead of the default that orders
	// by the fields that are indexed.
	if len(fromTable.OrderByFieldsWithSortDirection) > 0 {
		for _, oi := range fromTable.OrderByFieldsWithSortDirection {
			// Add table order by fields iff the user did not specify them.
			if !obs[oi.TableField] {
				orderByFields = append(orderByFields, oi)
			}
		}
	} else {
		for _, oi := range fromTable.OrderedIndicesWithSortDirection {
			// Add table order by fields iff the user did not specify them.
			if !obs[oi.TableField] {
				orderByFields = append(orderByFields, oi)
			}
		}
	}

	// Add query literal for each field that needs to be included for the
	// page token to the query. Store the index of these in the select
	// statement in a map to be returned so the fields can be retrieved.
	var pageTokenFields []UserOrderedIndex
	for _, f := range orderByFields {
		if !equalityFilters[f.TableField] {
			pageTokenFields = append(pageTokenFields, f)
		}
	}
	tfToIdx := make(map[TableField]int)
	for _, t := range pageTokenFields {
		f, ok := fromTable.Fields[t.TableField]
		if !ok {
			return nil, nil, nil, status.Errorf(status.Internal, "%s", fmt.Sprintf("unable to find field %v in table %v", t, fromTable))
		}

		if index, ok := selectedMembers[f.QueryLiteral]; q.options.deduplicateSelects && ok {
			tfToIdx[t.TableField] = index
			continue
		}
		if selectIdx > 0 {
			query.SQL = query.SQL + ", "
		}
		query.SQL = query.SQL + f.QueryLiteral.String()
		tfToIdx[t.TableField] = selectIdx
		if q.options.deduplicateSelects {
			selectedMembers[f.QueryLiteral] = selectIdx
		}
		selectIdx++
	}

	// Add FROM.
	froms := []safesql.TrustedSQLString{safesql.New(" FROM "), fromTable.NameClause}
	// Add JOIN clauses.
	for _, j := range joins {
		froms = append(froms, j.Clause)
	}
	// Apply the UNNEST.
	if err := unr.Apply(query, froms); err != nil {
		return nil, nil, nil, status.Errorf(status.InvalidArgument, "error processing filter expression: %v", err)
	}

	// Get all the WHERE clauses in UNNEST conditions.
	var unnestWheres []safesql.TrustedSQLString
	for unnest := range unr.seen {
		if unnest != nil {
			unnestWheres = append(unnestWheres, unnest.Where)
		}
	}

	// Add the WHERE clause, if there are any to add in the base or user filter, page token fields,
	// or unnest clauses.
	if !emptyExpression(baseFilterExp) || !emptyExpression(userFilterExp) || len(ptFields) > 0 || len(unnestWheres) > 0 {
		query.SQL = query.SQL + " WHERE "
	}

	qf := queryfilter.Spanner{
		MemberFunc:           MemberMap(query, filters),
		FunctionFunc:         FunctionMap(q.functions),
		DisjunctionFunc:      DisjunctionMemberMap(filters),
		OptimizeDisjunction:  q.optimizeFilters,
		Q:                    query,
		RestrictionTranslate: PhraseMap(q.options.rewrites),
		TextSearchColumns:    fullTextFields,
		FilterBaseOptions:    []queryfilter.FilterBaseOption{queryfilter.WithFullTextSearchEnabled},
	}

	// Add base filters.
	if !emptyExpression(baseFilterExp) {
		err := qf.AppendFilters(baseFilterExp)
		if err != nil {
			return nil, nil, nil, status.Errorf(status.InvalidArgument, "error processing base filter expression: %v", err)
		}
	}
	// Add user filters.
	if !emptyExpression(userFilterExp) {
		if baseFilterExp != nil {
			query.SQL = query.SQL + " AND "
		}
		err := qf.AppendFilters(userFilterExp)
		if err != nil {
			return nil, nil, nil, status.Errorf(status.InvalidArgument, "error processing filter expression: %v", err)
		}
	}

	// Add page token filters.
	var orderedPtFields []UserOrderedIndex
	for _, ob := range orderByFields {
		if _, ok := ptFields[ob.TableField]; ok {
			orderedPtFields = append(orderedPtFields, ob)
		}
	}
	if len(orderedPtFields) != 0 {
		// In the unlikely case that we have no filters at all, this
		// could be the beginning of the WHERE clause.
		if !emptyExpression(baseFilterExp) || !emptyExpression(userFilterExp) {
			query.SQL = query.SQL + " AND "
		}
		query.SQL = query.SQL + "("
		if err := q.appendPageTokenFields(fromTable, ptFields, orderedPtFields, query); err != nil {
			return nil, nil, nil, status.Errorf(status.Internal, "error adding page token fields %v", err)
		}
		query.SQL = query.SQL + ")"
	}

	// Add UNNEST conditions to WHERE.
	for _, unnestWhere := range unnestWheres {
		query.SQL = query.SQL + unnestWhere.String()
	}

	if needGroupBy {
		appendGroupByClause(query, selectMembers, orderByFields, fromTable)
	}

	if len(orderByFields) > 0 {
		query.SQL = query.SQL + " ORDER BY "
		for i, ob := range orderByFields {
			if i > 0 {
				query.SQL = query.SQL + ", "
			}
			query.SQL = query.SQL + fromTable.Fields[ob.TableField].QueryLiteral.String()
			if ob.SortOrder == Desc {
				query.SQL = query.SQL + " DESC"
			}
		}
	}

	// Add pageSize limit if needed.
	if pageSize > 0 {
		query.SQL = query.SQL + " LIMIT " + strconv.Itoa(pageSize)
	}
	if offset > 0 {
		query.SQL = query.SQL + " OFFSET " + strconv.Itoa(offset)
	}

	return query, tfToIdx, &unr, nil
}

// LastFromTable returns the table that was last selected from. This is filled in after calling
// GenerateQuery(). This is generally to be used for logging or metric purposes by the client.
func (q *queryGen) LastFromTable() TableName {
	return q.lastFromTable
}

// appendPageTokenFields adds WHERE conditions to query to enforce pagination conditions for
// orderedFields, having the values found in fieldToValue. It recurses to handle multiple page token
// fields; for example if we have indices A,B and the last page token was A=5,B=2 then we continue
// with (A=5 AND B>2) OR (A>5).
func (q *queryGen) appendPageTokenFields(fromTable TableView, fieldToValue map[TableField]any, orderedFields []UserOrderedIndex, query *spanner.Statement) error {
	if len(orderedFields) == 0 {
		return nil
	}
	query.SQL = query.SQL + "("
	for i, orderedField := range orderedFields {
		if i > 0 {
			query.SQL = query.SQL + " AND "
		}
		v, ok := fieldToValue[orderedField.TableField]
		if !ok {
			return status.Errorf(status.FailedPrecondition, "unable to find value for field %q in map %+v", orderedField.TableField, fieldToValue)
		}
		f, ok := fromTable.Fields[orderedField.TableField]
		if !ok {
			return status.Errorf(status.FailedPrecondition, "unable to find field %q in map %+v", orderedField.TableField, fromTable.Fields)
		}
		query.SQL = query.SQL + f.QueryLiteral.String()
		if i == len(orderedFields)-1 {
			if orderedField.SortOrder == Desc {
				query.SQL = query.SQL + " < "
			} else {
				query.SQL = query.SQL + " > "
			}
		} else {
			query.SQL = query.SQL + " = "
		}

		query.SQL = query.SQL + "@val"
		query.SQL = query.SQL + strconv.Itoa(len(orderedFields))
		query.SQL = query.SQL + strconv.Itoa(i)

		if query.Params == nil {
			query.Params = make(map[string]any)
		}
		query.Params[fmt.Sprintf("val%v%v", len(orderedFields), i)] = v
	}
	query.SQL = query.SQL + ")"
	if len(orderedFields) > 1 {
		query.SQL = query.SQL + " OR "
	}
	return q.appendPageTokenFields(fromTable, fieldToValue, orderedFields[:len(orderedFields)-1], query)
}

// candidateTable contains heuristics on a table that is used to help determine whether it is the
// best table to select from. The heuristics depend on the filters being used.
type candidateTable struct {
	t TableView
	// numFiltersIndexed is how many filter fields make up the prefix of this table's index.
	numFiltersIndexed int
}

// removeIfNullFilterUnsatisfied removes a candidate table if it contains fields that are null
// filtered but aren't being filtered on in the base or user filters.
func removeIfNullFilterUnsatisfied(candidateTables map[TableName]*candidateTable, baseFilters []TableField, userFilters map[string]TableField) {
	// Turn base filters into a set for fast lookup.
	baseFiltersSet := map[TableField]bool{}
	for _, bf := range baseFilters {
		baseFiltersSet[bf] = true
	}

	// Turn the table fields of the user filters into a set. This is because the candidate tables use
	// table field names and not the external One Platform filter field name that are currently the
	// keys of userFilters.
	userFiltersSet := map[TableField]bool{}
	for _, uf := range userFilters {
		userFiltersSet[uf] = true
	}

	for i, ct := range candidateTables {
		// Due to limitations right now we only check that there is a filter on at least one of the null
		// filtered fields to allow a null filtered table to be a candidate table. This is because with
		// the current framework, we can't check that a user is filtering on all null filtered fields
		// using conjunctions.
		hasNullFiltered := false
		isFilteringOnNF := false
		for field, member := range ct.t.Fields {
			if member.NullFiltered {
				hasNullFiltered = true
				_, okBF := baseFiltersSet[field]
				_, okUF := userFiltersSet[field]
				if okBF || okUF {
					isFilteringOnNF = true
					break
				}
			}
		}
		if hasNullFiltered && !isFilteringOnNF {
			log.V(1).Infof("There are no filters on any of table %q's null filtered fields, removing it from candidate tables", ct.t.Name)
			delete(candidateTables, i)
		}
	}
}

// updateNumFiltersIndexed updates the heuristic for number of filter fields that make up the prefix
// of each candidate table's index.
func updateNumFiltersIndexed(candidateTables map[TableName]*candidateTable, baseFilters []TableField, userFilters map[string]TableField) {
	// Turn base filters into a set for fast lookup.
	baseFiltersSet := map[TableField]bool{}
	for _, bf := range baseFilters {
		baseFiltersSet[bf] = true
	}

	// Turn the table fields of the user filters into a set. This is because the candidate tables use
	// table field names and not the external One Platform filter field name that are currently the
	// keys of userFilters.
	userFiltersSet := map[TableField]bool{}
	for _, uf := range userFilters {
		userFiltersSet[uf] = true
	}

	for _, ct := range candidateTables {
		for _, index := range ct.t.OrderedIndicesWithSortDirection {
			_, okBF := baseFiltersSet[index.TableField]
			_, okUF := userFiltersSet[index.TableField]
			if okBF || okUF {
				ct.numFiltersIndexed++
				continue
			}
			// The filter fields must make up a prefix of this table's index, so if we can't match in
			// order, we stop counting.
			break
		}
		log.V(1).Infof("Candidate table %q has %d filter field(s) that make up the prefix of its index", ct.t.Name, ct.numFiltersIndexed)
	}
}

// removeTableIfMissingField removes a candidate table if it does not contain the specified field.
func removeTableIfMissingField(filter TableField, candidateTables map[TableName]*candidateTable) {
	for i, ct := range candidateTables {
		if _, ok := ct.t.Fields[filter]; !ok {
			log.V(1).Infof("Table %q does not contain field %q, removing it", ct.t.Name, filter)
			delete(candidateTables, i)
			continue
		}
	}
}

// filterNames extracts the keys from a filters map for logging purposes.
func filterNames(filters map[string]TableField) []string {
	var ns []string
	for n := range filters {
		ns = append(ns, n)
	}
	return ns
}

// tableNames extracts the keys from a tables map for logging purposes.
func tableNames(tables map[TableName]*candidateTable) []TableName {
	var ts []TableName
	for t := range tables {
		ts = append(ts, t)
	}
	return ts
}

// relationshipTableNames extracts the table names from a list of join relationshps for logging
// purposes.
func relationshipTableNames(joins []Join) []TableName {
	var ts []TableName
	for _, j := range joins {
		ts = append(ts, j.T.Name)
	}
	return ts
}

// pageTokenNames extracts the keys from a page token fields map for logging purposes.
func pageTokenNames(ptFields map[TableField]any) []TableField {
	var ps []TableField
	for p := range ptFields {
		ps = append(ps, p)
	}
	return ps
}

// emptyExpression returns a bool indicating if the Expression is nil or completely empty.
func emptyExpression(exp *fpb.Expression) bool {
	return exp.GetExpression() == nil
}

func checkCandidateTableOrderBy(t TableView, userOrderBy []UserOrderedIndex, optimizeOrderBy bool) (bool, error) {
	if len(userOrderBy) == 0 {
		return true, nil
	}
	for _, uob := range userOrderBy {
		m, ok := t.Fields[uob.TableField]
		if !ok {
			return false, nil
		}
		if m.Derived && optimizeOrderBy {
			return false, status.Errorf(status.InvalidArgument, "derived field %q cannot be used for ordering", uob.TableField)
		}
	}
	if !optimizeOrderBy {
		return true, nil
	}
	if len(userOrderBy) > len(t.OrderedIndicesWithSortDirection) {
		return false, nil
	}
	for i, oi := range userOrderBy {
		if oi.TableField != t.OrderedIndicesWithSortDirection[i].TableField || oi.SortOrder != t.OrderedIndicesWithSortDirection[i].SortOrder {
			return false, nil
		}
	}
	return true, nil
}

func resolveJoinFieldFromRelationships(sf TableField, sfs map[TableField]bool, fromTable TableView, joins *[]Join, selectMembers *[]Member) (bool, error) {
	log.V(1).Infof("Join field %q is not available in any existing joins, see if it's available in any of the prospective joins %v", sf, relationshipTableNames(fromTable.Relationships))
	for _, jt := range fromTable.Relationships {
		m, ok := jt.T.Fields[sf]
		if ok && m.Selectable {
			*joins = append(*joins, jt)
			log.V(1).Infof("Join field %q is selectable from join table %q", sf, jt.T.Name)
			if sfs[sf] {
				log.V(1).Infof("Join field %q is in select fields, adding it to select members", sf)
				*selectMembers = append(*selectMembers, m)
			}
			return true, nil
		}
	}
	return false, status.Errorf(status.FailedPrecondition, "unable to find a table to select %q from", sf)
}

func appendGroupByClause(query *spanner.Statement, selectMembers []Member, orderByFields []UserOrderedIndex, fromTable TableView) {
	groupFields := map[safesql.TrustedSQLString]bool{}
	query.SQL = query.SQL + " GROUP BY "
	needsComma := false
	for _, m := range selectMembers {
		if !m.OmitFromGroupBy {
			if needsComma {
				query.SQL = query.SQL + ", "
			}
			query.SQL = query.SQL + m.QueryLiteral.String()
			groupFields[m.QueryLiteral] = true
			needsComma = true
		}
	}

	for _, ob := range orderByFields {
		ql := fromTable.Fields[ob.TableField].QueryLiteral
		if !groupFields[ql] {
			if needsComma {
				query.SQL = query.SQL + ", "
			}
			query.SQL = query.SQL + ql.String()
			groupFields[ql] = true
			needsComma = true
		}
	}
}

func (q *queryGen) scoutUserFilterMembers(userFilterExp *fpb.Expression, fullTextFields []safesql.TrustedSQLString, userFilters map[string]TableField, equalityFilters map[TableField]bool) error {
	if emptyExpression(userFilterExp) {
		return nil
	}
	dummyQuery := spanner.NewStatement("")

	qf := queryfilter.Spanner{
		MemberFunc: func(name []string) (safesql.TrustedSQLString, filtervisitor.PrimType, error) {
			n := strings.Join(name, ".")
			m, ok := q.members[n]
			if !ok || m.Name == "" {
				return safesql.New(""), filtervisitor.Invalid, status.Errorf(status.InvalidArgument, "unsupported field name in query: %q", n)
			}
			userFilters[n] = m.Name
			return safesql.New(""), m.Type, nil
		},
		FunctionFunc:      FunctionMap(q.functions),
		Q:                 &dummyQuery,
		TextSearchColumns: fullTextFields,
		FilterBaseOptions: []queryfilter.FilterBaseOption{queryfilter.WithFullTextSearchEnabled},
	}

	err := qf.AppendFilters(userFilterExp)
	if err != nil {
		return status.Errorf(status.InvalidArgument, "error processing filters: %v", err)
	}

	ev := equalityvisitor.EqualityVisitor{
		MemberFunc: func(name []string, followsEq bool) (filtervisitor.PrimType, error) {
			if !followsEq {
				return filtervisitor.Invalid, nil
			}
			n := strings.Join(name, ".")
			a, ok := q.members[n]
			if !ok {
				return filtervisitor.Invalid, status.Errorf(status.InvalidArgument, "name %q not found", n)
			}
			equalityFilters[a.Name] = true
			return a.Type, nil
		},
	}
	filtervisitor.Visit(&ev, userFilterExp)
	return nil
}
