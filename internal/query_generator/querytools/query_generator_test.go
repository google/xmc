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
package querytools

import (
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"xmanager_cloud/api/filter/go/filter"
	"xmanager_cloud/api/filter/go/filtervisitor"
	postgresdialect "xmanager_cloud/internal/query_generator/dialect/postgres"
	spannerdialect "xmanager_cloud/internal/query_generator/dialect/spanner"
	"xmanager_cloud/internal/query_generator/predicate"
	"xmanager_cloud/internal/safesql"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	fpb "xmanager_cloud/api/filter"
)

func TestGenerateQuery(t *testing.T) {
	preamble := safesql.New("@{memory_limit=268435456}")
	tests := []struct {
		desc                    string
		tables                  []TableView
		members                 map[string]Member
		selectFields            []TableField
		baseFilters             []TableField
		baseFilterExp           *fpb.Expression
		userFilter              string
		orderByFields           []UserOrderedIndex
		pageSize                int
		offset                  int
		preamble                *safesql.TrustedSQLString
		allowUnoptimizedOrderBy bool
		wantQuery               spanner.Statement
		options                 []Option
	}{
		// Note that it's possible for a query to SELECT a field more than once, because of a lack of
		// deduping between explicitly selected fields and fields needed for pagination.
		{
			desc:         "no base filter, no user filter",
			tables:       SingersTables,
			selectFields: []TableField{SingerIDTableField, SingerNameTableField},
			wantQuery:    spanner.NewStatement(`SELECT s.SingerId, s.SingerName, s.SingerId FROM Singers s ORDER BY s.SingerId`),
			options:      []Option{DeduplicateSelects(false)},
		},
		{
			desc:         "no base filter, no user filter, deduplicating",
			tables:       SingersTables,
			selectFields: []TableField{SingerIDTableField, SingerNameTableField},
			wantQuery:    spanner.NewStatement(`SELECT s.SingerId, s.SingerName FROM Singers s ORDER BY s.SingerId`),
			options:      []Option{DeduplicateSelects(true)},
		},
		{
			desc:          "base filter, no user filter",
			tables:        SingersTables,
			selectFields:  []TableField{SingerIDTableField, SingerNameTableField},
			baseFilters:   []TableField{SingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(SingerIDTableField), Value: "1"}),
			wantQuery:     spanner.NewStatement(`SELECT s.SingerId, s.SingerName FROM Singers s WHERE (s.SingerId = @fb0) ORDER BY s.SingerId`),
		},
		{
			desc:          "base filter, no user filter, deduplicate has no affect",
			tables:        SingersTables,
			selectFields:  []TableField{SingerIDTableField, SingerNameTableField},
			baseFilters:   []TableField{SingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(SingerIDTableField), Value: "1"}),
			wantQuery:     spanner.NewStatement(`SELECT s.SingerId, s.SingerName FROM Singers s WHERE (s.SingerId = @fb0) ORDER BY s.SingerId`),
			options:       []Option{DeduplicateSelects(true)},
		},
		{
			desc:         "user filter, no base filter",
			tables:       SingersTables,
			selectFields: []TableField{SingerNameTableField},
			userFilter:   `SingerName = "Marley"`,
			wantQuery:    spanner.NewStatement(`SELECT s.SingerName, s.SingerId FROM Singers s WHERE (s.SingerName = @fb0) ORDER BY s.SingerId`),
		},
		{
			desc:       "user filter, no base filter, no select fields",
			tables:     SingersTables,
			userFilter: `SingerName = "Marley"`,
			wantQuery:  spanner.NewStatement(`SELECT s.SingerId FROM Singers s WHERE (s.SingerName = @fb0) ORDER BY s.SingerId`),
		},
		{
			desc:       "user filter only, multiple ORs on optimizable disjunction field",
			tables:     SingersTables,
			userFilter: `SingerId = "123" OR SingerName = "Marley" OR SingerName = "Bob" OR SingerName = "Matt"`,
			wantQuery:  spanner.NewStatement(`SELECT s.SingerId FROM Singers s WHERE ((s.SingerId = @fb0) OR (s.SingerName IN UNNEST (@fb1))) ORDER BY s.SingerId`),
		},
		{
			desc:       "user filter only, multiple ORs on non-optimizable disjunction field",
			tables:     SingersTables,
			userFilter: `SingerId = "123" OR SingerId = "2"`,
			wantQuery:  spanner.NewStatement(`SELECT s.SingerId FROM Singers s WHERE ((s.SingerId = @fb0) OR (s.SingerId = @fb1)) ORDER BY s.SingerId`),
		},
		{
			desc:         "page size specified",
			tables:       SingersTables,
			selectFields: []TableField{SingerNameTableField},
			userFilter:   `SingerName = "Marley"`,
			pageSize:     200,
			wantQuery:    spanner.NewStatement(`SELECT s.SingerName, s.SingerId FROM Singers s WHERE (s.SingerName = @fb0) ORDER BY s.SingerId LIMIT 200`),
		},
		{
			desc:         "offset specified",
			tables:       SingersTables,
			selectFields: []TableField{SingerNameTableField},
			userFilter:   `SingerName = "Marley"`,
			offset:       10,
			wantQuery:    spanner.NewStatement(`SELECT s.SingerName, s.SingerId FROM Singers s WHERE (s.SingerName = @fb0) ORDER BY s.SingerId OFFSET 10`),
		},
		{
			desc:         "page size and offset specified",
			tables:       SingersTables,
			selectFields: []TableField{SingerNameTableField},
			userFilter:   `SingerName = "Marley"`,
			pageSize:     200,
			offset:       10,
			wantQuery:    spanner.NewStatement(`SELECT s.SingerName, s.SingerId FROM Singers s WHERE (s.SingerName = @fb0) ORDER BY s.SingerId LIMIT 200 OFFSET 10`),
		},
		{
			desc:         "preamble specified",
			tables:       SingersTables,
			selectFields: []TableField{SingerNameTableField},
			userFilter:   `SingerName = "Marley"`,
			preamble:     &preamble,
			wantQuery:    spanner.NewStatement(`@{memory_limit=268435456} SELECT s.SingerName, s.SingerId FROM Singers s WHERE (s.SingerName = @fb0) ORDER BY s.SingerId`),
		},
		{
			desc:          "album table joins with singers table when selecting from albums table (with album ID) and singer name",
			tables:        AllTables,
			selectFields:  []TableField{AlbumSingerIDTableField, SingerNameTableField},
			userFilter:    `AlbumId = "1"`,
			baseFilters:   []TableField{AlbumSingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumSingerIDTableField), Value: "1"}),
			wantQuery:     spanner.NewStatement(`SELECT a.SingerId, s.SingerName FROM Albums a JOIN Singers s ON (a.SingerId = s.SingerId) WHERE (a.SingerId = @fb0) AND (a.AlbumId = @fb1) ORDER BY a.SingerId, a.AlbumId`),
		},
		{
			desc:   "members can be manually specified for user filters",
			tables: AlbumsTables,
			members: map[string]Member{"TheGreatestAlbumId": {
				Name:         AlbumIDTableField,
				QueryLiteral: safesql.New("a.AlbumId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			}},
			selectFields:  []TableField{AlbumSingerIDTableField},
			userFilter:    `TheGreatestAlbumId = "1"`,
			baseFilters:   []TableField{AlbumSingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumSingerIDTableField), Value: "1"}),
			wantQuery:     spanner.NewStatement(`SELECT a.SingerId FROM Albums a WHERE (a.SingerId = @fb0) AND (a.AlbumId = @fb1) ORDER BY a.SingerId, a.AlbumId`),
		},
		{
			desc:         "selecting singer name count requires group by, no base filter, no user filter",
			tables:       SingersTables,
			selectFields: []TableField{SingerIDTableField, SingerNameCountTableField},
			wantQuery:    spanner.NewStatement(`SELECT s.SingerId, COUNT(s.SingerName) AS SingerNameCount FROM Singers s GROUP BY s.SingerId ORDER BY s.SingerId`),
		},
		{
			desc:          "selecting singer name count requires group by, base filter, no user filter",
			tables:        SingersTables,
			selectFields:  []TableField{SingerIDTableField, SingerNameCountTableField},
			baseFilters:   []TableField{SingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(SingerIDTableField), Value: "1"}),
			wantQuery:     spanner.NewStatement(`SELECT s.SingerId, COUNT(s.SingerName) AS SingerNameCount FROM Singers s WHERE (s.SingerId = @fb0) GROUP BY s.SingerId ORDER BY s.SingerId`),
		},
		{
			desc:          "selecting singer name count requires group by, base filter, user filter",
			tables:        SingersTables,
			selectFields:  []TableField{SingerIDTableField, SingerNameCountTableField},
			baseFilters:   []TableField{SingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(SingerIDTableField), Value: "1"}),
			userFilter:    `SingerName = "Marley"`,
			wantQuery:     spanner.NewStatement(`SELECT s.SingerId, COUNT(s.SingerName) AS SingerNameCount FROM Singers s WHERE (s.SingerId = @fb0) AND (s.SingerName = @fb1) GROUP BY s.SingerId ORDER BY s.SingerId`),
		},
		{
			desc:                    "user specified order by allowed on an unindexed column, base filter, user filter, unoptimized order by allowed",
			tables:                  AlbumsTables,
			selectFields:            []TableField{AlbumSingerIDTableField},
			baseFilters:             []TableField{AlbumSingerIDTableField},
			baseFilterExp:           predicate.Conjunction(predicate.String{Field: string(AlbumSingerIDTableField), Value: "1"}),
			userFilter:              `ReleaseDate = "2016-12-02"`,
			allowUnoptimizedOrderBy: true,
			orderByFields:           []UserOrderedIndex{{TableField: ReleaseDateTableField, SortOrder: Desc}},
			wantQuery:               spanner.NewStatement(`SELECT a.SingerId, a.AlbumId FROM Albums a WHERE (a.SingerId = @fb0) AND (a.ReleaseDate = @fb1) ORDER BY a.ReleaseDate DESC, a.SingerId, a.AlbumId`),
		},
		{
			desc:                    "user specified order on indexed column, base filter, no user filter",
			tables:                  AlbumsTables,
			selectFields:            []TableField{AlbumSingerIDTableField},
			baseFilters:             []TableField{AlbumSingerIDTableField},
			baseFilterExp:           predicate.Conjunction(predicate.String{Field: string(AlbumSingerIDTableField), Value: "1"}),
			allowUnoptimizedOrderBy: true,
			orderByFields:           []UserOrderedIndex{{TableField: AlbumSingerIDTableField, SortOrder: Desc}},
			wantQuery:               spanner.NewStatement(`SELECT a.SingerId, a.AlbumId FROM Albums a WHERE (a.SingerId = @fb0) ORDER BY a.SingerId DESC, a.AlbumId`),
		},
		{
			desc:                    "multiple user specified order on indexed columns, base filter, no user filter",
			tables:                  AlbumsTables,
			selectFields:            []TableField{AlbumSingerIDTableField},
			baseFilters:             []TableField{AlbumSingerIDTableField},
			baseFilterExp:           predicate.Conjunction(predicate.String{Field: string(AlbumSingerIDTableField), Value: "1"}),
			allowUnoptimizedOrderBy: true,
			orderByFields: []UserOrderedIndex{
				{TableField: AlbumSingerIDTableField, SortOrder: Desc},
				{TableField: AlbumIDTableField, SortOrder: Desc},
			},
			wantQuery: spanner.NewStatement(`SELECT a.SingerId, a.AlbumId FROM Albums a WHERE (a.SingerId = @fb0) ORDER BY a.SingerId DESC, a.AlbumId DESC`),
		},
		{
			desc:          "selecting from a table that has an order by override, base filter, no user filter",
			tables:        []TableView{AlbumsByTitleAlbumIDReleaseDateOrderByOverride},
			selectFields:  []TableField{AlbumIDTableField},
			baseFilters:   []TableField{AlbumTitleTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumTitleTableField), Value: "Killer Queen"}),
			wantQuery:     spanner.NewStatement(`SELECT a.AlbumId FROM Albums@{FORCE_INDEX=AlbumsByTitleAlbumIdReleaseDate} a WHERE (a.AlbumTitle = @fb0) ORDER BY a.AlbumId`),
		},
		{
			desc:          "selecting multiple fields from a table that has an order by override, base filter, no user filter",
			tables:        []TableView{AlbumsByTitleAlbumIDReleaseDateOrderByOverride},
			selectFields:  []TableField{AlbumIDTableField, RatingTableField},
			baseFilters:   []TableField{AlbumTitleTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumTitleTableField), Value: "Killer Queen"}),
			wantQuery:     spanner.NewStatement(`SELECT a.AlbumId, a.Rating FROM Albums@{FORCE_INDEX=AlbumsByTitleAlbumIdReleaseDate} a WHERE (a.AlbumTitle = @fb0) ORDER BY a.AlbumId`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			qg, err := NewOptimizableGenerator(tt.tables, tt.members, tt.baseFilters, nil, true, !tt.allowUnoptimizedOrderBy, tt.options...)
			if err != nil {
				t.Fatalf("Error creating query generator: %v", err)
			}

			exp, err := filter.ParseExpressionWithOptions(fpb.ExpressionParserOptions_builder{RejectLegacyOperatorPrecedence: false}.Build(), tt.userFilter)
			if err != nil {
				t.Fatalf("Error parsing user filter expression %q: %v", tt.userFilter, err)
			}

			q := spannerdialect.NewStatement(nil)
			_, _, err = qg.GenerateQuery(q, tt.selectFields, nil, tt.baseFilterExp, exp, tt.orderByFields, tt.pageSize, tt.offset, nil, tt.preamble)
			if err != nil {
				t.Fatalf("Error generating query: %v", err)
			}
			if q.Spanner().SQL != tt.wantQuery.SQL {
				t.Errorf("Got query %q, want %q", q.Spanner().SQL, tt.wantQuery.SQL)
			}
		})
	}
}

func TestGenerateQueryErrors(t *testing.T) {
	tests := []struct {
		desc                    string
		tables                  []TableView
		members                 map[string]Member
		selectFields            []TableField
		baseFilters             []TableField
		baseFilterExp           *fpb.Expression
		userFilter              string
		orderByFields           []UserOrderedIndex
		allowUnoptimizedOrderBy bool
		wantErrCode             codes.Code
	}{
		{
			desc:         "no single table can satisfy all filters",
			tables:       AllTables,
			selectFields: []TableField{SingerIDTableField},
			userFilter:   `SingerName = "Ringo" AND AlbumTitle = "Killer Queen"`,
			wantErrCode:  codes.FailedPrecondition,
		},
		{
			desc:          "user filter on a field that doesn't exist (made up field)",
			tables:        SingersTables,
			selectFields:  []TableField{SingerIDTableField},
			baseFilters:   []TableField{SingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(SingerIDTableField), Value: "1"}),
			userFilter:    `MadeUpField = "rawr"`,
			wantErrCode:   codes.InvalidArgument,
		},
		{
			desc:   "user filter cannot use a field (singer ID) not specified in members even if it's in the base filters",
			tables: SingersTables,
			members: map[string]Member{"SingerName": {
				Name:         SingerNameTableField,
				QueryLiteral: safesql.New("s.SingerName"),
				Type:         filtervisitor.String,
				Selectable:   true,
			}},
			selectFields:  []TableField{SingerIDTableField},
			baseFilters:   []TableField{SingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(SingerIDTableField), Value: "1"}),
			userFilter:    `SingerId = "3"`,
			wantErrCode:   codes.InvalidArgument,
		},
		{
			desc:          "selecting a field (album ID) that doesn't exist in any singer tables",
			tables:        SingersTables,
			selectFields:  []TableField{SingerIDTableField, AlbumIDTableField},
			baseFilters:   []TableField{SingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(SingerIDTableField), Value: "1"}),
			wantErrCode:   codes.FailedPrecondition,
		},
		{
			desc:          "specifying a base filter (album ID) that doesn't exist in any singer tables when creating a query generator",
			tables:        SingersTables,
			selectFields:  []TableField{SingerIDTableField},
			baseFilters:   []TableField{SingerIDTableField, AlbumIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(SingerIDTableField), Value: "1"}),
			wantErrCode:   codes.FailedPrecondition,
		},
		{
			desc:          "filtering on a base filter (album ID) that wasn't specified when creating the query generator",
			tables:        SingersTables,
			selectFields:  []TableField{SingerIDTableField},
			baseFilters:   []TableField{SingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumIDTableField), Value: "1"}),
			wantErrCode:   codes.InvalidArgument,
		},
		{
			desc:                    "user specified order by not allowed on an unindexed column, base filter, user filter, optimized order by",
			tables:                  AlbumsTables,
			selectFields:            []TableField{AlbumSingerIDTableField},
			baseFilters:             []TableField{AlbumSingerIDTableField},
			baseFilterExp:           predicate.Conjunction(predicate.String{Field: string(AlbumSingerIDTableField), Value: "1"}),
			userFilter:              `ReleaseDate = "2016-12-02"`,
			allowUnoptimizedOrderBy: false,
			orderByFields:           []UserOrderedIndex{{TableField: ReleaseDateTableField, SortOrder: Desc}},
			wantErrCode:             codes.FailedPrecondition,
		},
		{
			desc:                    "user specified order by not allowed on a derived field, base filter, no user filter, optimized order by",
			tables:                  SingersTables,
			selectFields:            []TableField{SingerIDTableField},
			baseFilters:             []TableField{SingerIDTableField},
			baseFilterExp:           predicate.Conjunction(predicate.String{Field: string(SingerIDTableField), Value: "1"}),
			allowUnoptimizedOrderBy: false,
			orderByFields:           []UserOrderedIndex{{TableField: SingerNameCountTableField, SortOrder: Desc}},
			wantErrCode:             codes.InvalidArgument,
		},
		{
			desc:                    "user specified order by does not match order of indexed fields, base filter, no user filter, optimized order by",
			tables:                  AlbumsTables,
			selectFields:            []TableField{AlbumSingerIDTableField},
			baseFilters:             []TableField{AlbumSingerIDTableField},
			baseFilterExp:           predicate.Conjunction(predicate.String{Field: string(AlbumSingerIDTableField), Value: "1"}),
			allowUnoptimizedOrderBy: false,
			orderByFields: []UserOrderedIndex{
				{TableField: AlbumIDTableField, SortOrder: Desc},
				{TableField: AlbumSingerIDTableField, SortOrder: Desc},
			},
			wantErrCode: codes.FailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			qg, err := NewOptimizableGenerator(tt.tables, tt.members, tt.baseFilters, nil, true, !tt.allowUnoptimizedOrderBy)
			if err != nil {
				t.Fatalf("Error creating query generator: %v", err)
			}

			exp, err := filter.ParseExpressionWithOptions(fpb.ExpressionParserOptions_builder{RejectLegacyOperatorPrecedence: false}.Build(), tt.userFilter)
			if err != nil {
				t.Fatalf("Error parsing user filter expression %q: %v", tt.userFilter, err)
			}

			_, _, err = qg.GenerateQuery(spannerdialect.NewStatement(nil), tt.selectFields, nil, tt.baseFilterExp, exp, tt.orderByFields, 0, 0, nil, nil)
			t.Logf("Got error: %v", err)
			if c := status.Code(err); c != tt.wantErrCode {
				t.Errorf("Generating query got err code %v, want %v", c, tt.wantErrCode)
			}
		})
	}
}

func TestGenerateQueryPageTokens(t *testing.T) {
	tests := []struct {
		desc          string
		tables        []TableView
		selectFields  []TableField
		baseFilters   []TableField
		baseFilterExp *fpb.Expression
		userFilter    string
		userOrderBy   []UserOrderedIndex
		wantPTFields  map[TableField]int
		// This is the query we want when using the page token fields from an initial query, that has no
		// page token fields specified, in subsequent queries to page.
		wantQuery spanner.Statement
	}{
		{
			desc:         "no filter in singers, page token fields needed",
			tables:       SingersTables,
			selectFields: []TableField{SingerNameTableField},
			wantPTFields: map[TableField]int{
				SingerIDTableField: 1,
			},
			wantQuery: spanner.NewStatement(`SELECT s.SingerName, s.SingerId FROM Singers s WHERE ((s.SingerId > @fb0)) ORDER BY s.SingerId`),
		},
		{
			desc:          "equality filter on primary key in singers, no page token fields needed",
			tables:        SingersTables,
			selectFields:  []TableField{SingerNameTableField},
			baseFilters:   []TableField{SingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(SingerIDTableField), Value: "1"}),
			wantPTFields:  map[TableField]int{},
			wantQuery:     spanner.NewStatement(`SELECT s.SingerName FROM Singers s WHERE (s.SingerId = @fb0) ORDER BY s.SingerId`),
		},
		{
			desc:         "greater than filter on primary key in singers, page token fields needed",
			tables:       SingersTables,
			selectFields: []TableField{SingerNameTableField},
			userFilter:   `SingerId > "101"`,
			wantPTFields: map[TableField]int{
				SingerIDTableField: 1,
			},
			wantQuery: spanner.NewStatement(`SELECT s.SingerName, s.SingerId FROM Singers s WHERE (s.SingerId > @fb0) AND ((s.SingerId > @fb1)) ORDER BY s.SingerId`),
		},
		{
			desc:         "no filter in albums, page token fields needed",
			tables:       AlbumsTables,
			selectFields: []TableField{AlbumTitleTableField},
			wantPTFields: map[TableField]int{
				AlbumSingerIDTableField: 1,
				AlbumIDTableField:       2,
			},
			wantQuery: spanner.NewStatement(`SELECT a.AlbumTitle, a.SingerId, a.AlbumId FROM Albums a WHERE ((a.SingerId = @fb0 AND a.AlbumId > @fb1) OR (a.SingerId > @fb2)) ORDER BY a.SingerId, a.AlbumId`),
		},
		{
			desc:          "equality filter on singer ID primary key in albums, page token fields needed since there is another primary key album ID",
			tables:        AlbumsTables,
			selectFields:  []TableField{AlbumTitleTableField},
			baseFilters:   []TableField{AlbumSingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumSingerIDTableField), Value: "1"}),
			wantPTFields: map[TableField]int{
				AlbumIDTableField: 1,
			},
			wantQuery: spanner.NewStatement(`SELECT a.AlbumTitle, a.AlbumId FROM Albums a WHERE (a.SingerId = @fb0) AND ((a.AlbumId > @fb1)) ORDER BY a.SingerId, a.AlbumId`),
		},
		{
			desc:          "equality filter on album ID primary key in albums, page token fields needed since there is another primary key singer ID",
			tables:        AlbumsTables,
			selectFields:  []TableField{AlbumTitleTableField},
			baseFilters:   []TableField{AlbumIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumIDTableField), Value: "1"}),
			wantPTFields: map[TableField]int{
				AlbumSingerIDTableField: 1,
			},
			wantQuery: spanner.NewStatement(`SELECT a.AlbumTitle, a.SingerId FROM Albums a WHERE (a.AlbumId = @fb0) AND ((a.SingerId > @fb1)) ORDER BY a.SingerId, a.AlbumId`),
		},
		{
			desc:          "equality filter on both primary keys (conjunction) in albums, no page token fields needed",
			tables:        AlbumsTables,
			selectFields:  []TableField{AlbumTitleTableField},
			baseFilters:   []TableField{AlbumSingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumSingerIDTableField), Value: "1"}),
			userFilter:    `AlbumId = "123"`,
			wantPTFields:  map[TableField]int{},
			wantQuery:     spanner.NewStatement(`SELECT a.AlbumTitle FROM Albums a WHERE (a.SingerId = @fb0) AND (a.AlbumId = @fb1) ORDER BY a.SingerId, a.AlbumId`),
		},
		{
			desc:         "equality filter on both primary keys (disjunction) in albums, page token fields needed",
			tables:       AlbumsTables,
			selectFields: []TableField{AlbumTitleTableField},
			userFilter:   `SingerId = "90" OR AlbumId = "123"`,
			wantPTFields: map[TableField]int{
				AlbumSingerIDTableField: 1,
				AlbumIDTableField:       2,
			},
			wantQuery: spanner.NewStatement(`SELECT a.AlbumTitle, a.SingerId, a.AlbumId FROM Albums a WHERE ((a.SingerId = @fb0) OR (a.AlbumId = @fb1)) AND ((a.SingerId = @fb2 AND a.AlbumId > @fb3) OR (a.SingerId > @fb4)) ORDER BY a.SingerId, a.AlbumId`),
		},
		{
			desc:         "greater than filter on singer ID primary key in albums, page token fields needed",
			tables:       AlbumsTables,
			selectFields: []TableField{AlbumTitleTableField},
			userFilter:   `SingerId > "23"`,
			wantPTFields: map[TableField]int{
				AlbumSingerIDTableField: 1,
				AlbumIDTableField:       2,
			},
			wantQuery: spanner.NewStatement(`SELECT a.AlbumTitle, a.SingerId, a.AlbumId FROM Albums a WHERE (a.SingerId > @fb0) AND ((a.SingerId = @fb1 AND a.AlbumId > @fb2) OR (a.SingerId > @fb3)) ORDER BY a.SingerId, a.AlbumId`),
		},
		{
			desc:         "greater than filter with user order on album id without sort direction, page token fields needed",
			tables:       AlbumsTables,
			selectFields: []TableField{AlbumTitleTableField},
			userFilter:   `SingerId > "23"`,
			userOrderBy:  []UserOrderedIndex{{TableField: AlbumIDTableField}},
			wantPTFields: map[TableField]int{
				AlbumIDTableField:       1,
				AlbumSingerIDTableField: 2,
			},
			wantQuery: spanner.NewStatement(`SELECT a.AlbumTitle, a.AlbumId, a.SingerId FROM Albums a WHERE (a.SingerId > @fb0) AND ((a.AlbumId = @fb1 AND a.SingerId > @fb2) OR (a.AlbumId > @fb3)) ORDER BY a.AlbumId, a.SingerId`),
		},
		{
			desc:         "greater than filter with user order on album id with desc sort direction, page token fields needed",
			tables:       AlbumsTables,
			selectFields: []TableField{AlbumTitleTableField},
			userFilter:   `SingerId > "23"`,
			userOrderBy:  []UserOrderedIndex{{TableField: AlbumIDTableField, SortOrder: Desc}},
			wantPTFields: map[TableField]int{
				AlbumIDTableField:       1,
				AlbumSingerIDTableField: 2,
			},
			wantQuery: spanner.NewStatement(`SELECT a.AlbumTitle, a.AlbumId, a.SingerId FROM Albums a WHERE (a.SingerId > @fb0) AND ((a.AlbumId = @fb1 AND a.SingerId > @fb2) OR (a.AlbumId < @fb3)) ORDER BY a.AlbumId DESC, a.SingerId`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			qg, err := NewOptimizableGenerator(tt.tables, nil, tt.baseFilters, nil, true, false)
			if err != nil {
				t.Fatalf("Error creating query generator: %v", err)
			}

			exp, err := filter.ParseExpressionWithOptions(fpb.ExpressionParserOptions_builder{RejectLegacyOperatorPrecedence: false}.Build(), tt.userFilter)
			if err != nil {
				t.Fatalf("Error parsing user filter expression %q: %v", tt.userFilter, err)
			}

			// Initial query without specifying page token fields, ensure we get the correct page token
			// fields back for the next query (if we need to page.)
			ptFields, _, err := qg.GenerateQuery(spannerdialect.NewStatement(nil), tt.selectFields, nil, tt.baseFilterExp, exp, tt.userOrderBy, 0, 0, nil, nil)
			if err != nil {
				t.Errorf("Generating query got err %v, want success", err)
			}
			if !cmp.Equal(ptFields, tt.wantPTFields, cmpopts.EquateEmpty()) {
				t.Errorf("Generating query got page token fields %v, want %v", ptFields, tt.wantPTFields)
			}

			// Use the page token fields we got back in a subsequent query as the page token to test
			// paging constructs the correct SQL statement.
			pageToken := map[TableField]any{}
			for ptField := range ptFields {
				// Just use dummy value of "0" here, since it's not relevant to this test. Also assume
				// string page token field types for this test.
				pageToken[ptField] = "0"
			}
			q := spannerdialect.NewStatement(nil)
			ptFields2, _, err := qg.GenerateQuery(q, tt.selectFields, nil, tt.baseFilterExp, exp, tt.userOrderBy, 0, 0, pageToken, nil)
			if err != nil {
				t.Errorf("Generating query with page token %v got err %v, want success", pageToken, err)
			}
			// The page token fields should have not changed.
			if !cmp.Equal(ptFields2, tt.wantPTFields, cmpopts.EquateEmpty()) {
				t.Errorf("Generating query with page token %v got page token fields %v, want %v", pageToken, ptFields2, tt.wantPTFields)
			}
			if q.Spanner().SQL != tt.wantQuery.SQL {
				t.Errorf("Got query (using page token %v) %q, want %q", pageToken, q.Spanner().SQL, tt.wantQuery.SQL)
			}
		})
	}
}

func TestGenerateQueryChoosesCorrectIndex(t *testing.T) {
	tests := []struct {
		desc          string
		baseFilters   []TableField
		baseFilterExp *fpb.Expression
		userFilter    string
		wantIndexes   map[TableName]bool
	}{
		{
			desc: "no base or user filters, should select from the albums base table because it is the cheapest",
			wantIndexes: map[TableName]bool{
				AlbumsTableName: true,
			},
		},
		{
			desc:       "user filter on singer ID, should select from the albums base table because its index prefixes on singer ID",
			userFilter: `SingerId = "123"`,
			wantIndexes: map[TableName]bool{
				AlbumsTableName: true,
			},
		},
		{
			desc:       "user filter on rating, no table indexes on this in its prefix, so it should select the null filtered table because it is the cheapest",
			userFilter: `Rating = "5"`,
			wantIndexes: map[TableName]bool{
				AlbumsByTitleReleaseDateRatingTableName: true,
			},
		},
		{
			desc:          "base filter on album title, user filter on singer ID, albums base table prefixes on singer ID, others prefix on album title, but it should select from the albums base table because it is the cheapest",
			baseFilters:   []TableField{AlbumTitleTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumTitleTableField), Value: "Killer Queen"}),
			userFilter:    `SingerId = "123"`,
			wantIndexes: map[TableName]bool{
				AlbumsTableName: true,
			},
		},
		{
			desc:          "base filter on album title, should select from either of two optimal indexes that both prefix on album title",
			baseFilters:   []TableField{AlbumTitleTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumTitleTableField), Value: "Killer Queen"}),
			wantIndexes: map[TableName]bool{
				AlbumsByTitleReleaseDateTableName:        true,
				AlbumsByTitleAlbumIDReleaseDateTableName: true,
			},
		},
		{
			desc:          "base filter on album title, user filter on rating, should select from null filtered table",
			baseFilters:   []TableField{AlbumTitleTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumTitleTableField), Value: "Killer Queen"}),
			userFilter:    `Rating = 4`,
			wantIndexes: map[TableName]bool{
				AlbumsByTitleReleaseDateRatingTableName: true,
			},
		},
		{
			desc:          "base filter on album title, user filter on release date, should select from null filtered table",
			baseFilters:   []TableField{AlbumTitleTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumTitleTableField), Value: "Killer Queen"}),
			userFilter:    `ReleaseDate = "2018-01-23"`,
			wantIndexes: map[TableName]bool{
				AlbumsByTitleReleaseDateRatingTableName: true,
			},
		},
		{
			desc:          "base filter on album title, user filter on album ID, should select from one optimal index that prefixes on album title and album ID",
			baseFilters:   []TableField{AlbumTitleTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumTitleTableField), Value: "Killer Queen"}),
			userFilter:    `AlbumId = "123"`,
			wantIndexes: map[TableName]bool{
				AlbumsByTitleAlbumIDReleaseDateTableName: true,
			},
		},
		{
			desc:          "base filter on album title, user filter on release date and rating, should select from then null filtered index",
			baseFilters:   []TableField{AlbumTitleTableField},
			baseFilterExp: predicate.Conjunction(predicate.String{Field: string(AlbumTitleTableField), Value: "Killer Queen"}),
			userFilter:    `ReleaseDate = "2018-01-23" AND Rating = 5`,
			wantIndexes: map[TableName]bool{
				AlbumsByTitleReleaseDateRatingTableName: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			qg, err := NewOptimizableGenerator(AlbumsTables, nil, tt.baseFilters, nil, true, false)
			if err != nil {
				t.Fatalf("Error creating query generator: %v", err)
			}

			exp, err := filter.ParseExpressionWithOptions(fpb.ExpressionParserOptions_builder{RejectLegacyOperatorPrecedence: false}.Build(), tt.userFilter)
			if err != nil {
				t.Fatalf("Error parsing user filter expression %q: %v", tt.userFilter, err)
			}

			if _, _, err = qg.GenerateQuery(spannerdialect.NewStatement(nil), []TableField{AlbumSingerIDTableField}, nil, tt.baseFilterExp, exp, nil, 10, 0, nil, nil); err != nil {
				t.Fatalf("Error generating query: %v", err)
			}

			if _, ok := tt.wantIndexes[qg.LastFromTable()]; !ok {
				t.Errorf("Query generator with base filter %q and user filter %q chose index %q, want one of %v", tt.baseFilterExp, tt.userFilter, qg.LastFromTable(), tt.wantIndexes)
			}
		})
	}
}

func TestRewritePhrases(t *testing.T) {
	usrFilters := `SingerName = "Ringo" OR SingerName = "FOO_OLD"`
	exp, err := filter.ParseExpressionWithOptions(fpb.ExpressionParserOptions_builder{RejectLegacyOperatorPrecedence: true}.Build(), usrFilters)
	if err != nil {
		t.Fatalf("Unable to parse expressions %v, got %v, want success", usrFilters, err)
	}

	qg, err := NewQueryGenerator(SingersTables, nil, []TableField{SingerIDTableField}, nil, EnumRewrites(map[string]map[string]string{
		"SingerName": {
			"FOO_OLD": "FOO_NEW",
		},
	}))
	if err != nil {
		t.Fatalf("NewQueryGenerator() got error %v, expected success", err)
	}

	q := spannerdialect.NewStatement(nil)
	_, _, err = qg.GenerateQuery(q, []TableField{SingerIDTableField}, nil, nil, exp, nil, 0, 0, nil, nil)
	if err != nil {
		t.Errorf("GenerateQuery() got error %v expected success", err)
	}

	wantSQL := "SELECT s.SingerId FROM Singers s WHERE ((s.SingerName = @fb0) OR (s.SingerName = @fb1)) ORDER BY s.SingerId"
	wantParams := map[string]any{
		"fb0": "Ringo",
		"fb1": "FOO_NEW",
	}
	if q.Spanner().SQL != wantSQL {
		t.Errorf("Incorrect query output: got %q, wanted %q", q.Spanner().SQL, wantSQL)
	}
	if !cmp.Equal(q.Spanner().Params, wantParams) {
		t.Errorf("Incorrect query params: got %v, wanted %v", q.Spanner().Params, wantParams)
	}
}

func TestUpdateNumFiltersIndexed(t *testing.T) {
	tests := []struct {
		desc                  string
		candidateTables       map[TableName]*candidateTable
		baseFilters           []TableField
		userFilters           map[string]TableField
		wantNumFiltersIndexed map[TableName]int
	}{
		{
			desc:        "no base or user filters, no index prefix matches since there are no filters",
			baseFilters: []TableField{},
			userFilters: map[string]TableField{},
			wantNumFiltersIndexed: map[TableName]int{
				AlbumsTableName:                          0,
				AlbumsByTitleReleaseDateTableName:        0,
				AlbumsByTitleAlbumIDReleaseDateTableName: 0,
			},
		},
		{
			desc: "base filter on singer ID",
			baseFilters: []TableField{
				AlbumSingerIDTableField,
			},
			userFilters: map[string]TableField{},
			wantNumFiltersIndexed: map[TableName]int{
				AlbumsTableName:                          1,
				AlbumsByTitleReleaseDateTableName:        0,
				AlbumsByTitleAlbumIDReleaseDateTableName: 0,
			},
		},
		{
			desc:        "user filter on album ID, no index prefix matches",
			baseFilters: []TableField{},
			userFilters: map[string]TableField{
				"AlbumId": AlbumIDTableField,
			},
			wantNumFiltersIndexed: map[TableName]int{
				AlbumsTableName:                          0,
				AlbumsByTitleReleaseDateTableName:        0,
				AlbumsByTitleAlbumIDReleaseDateTableName: 0,
			},
		},
		{
			desc: "one base filter on singer ID, one user filter on album ID",
			baseFilters: []TableField{
				AlbumSingerIDTableField,
			},
			userFilters: map[string]TableField{
				"AlbumId": AlbumIDTableField,
			},
			wantNumFiltersIndexed: map[TableName]int{
				AlbumsTableName:                          2,
				AlbumsByTitleReleaseDateTableName:        0,
				AlbumsByTitleAlbumIDReleaseDateTableName: 0,
			},
		},
		{
			desc: "one base filter on album title, one user filter on release date",
			baseFilters: []TableField{
				AlbumTitleTableField,
			},
			userFilters: map[string]TableField{
				"ReleaseDate": ReleaseDateTableField,
			},
			wantNumFiltersIndexed: map[TableName]int{
				AlbumsTableName:                          0,
				AlbumsByTitleReleaseDateTableName:        2,
				AlbumsByTitleAlbumIDReleaseDateTableName: 1,
			},
		},
		{
			desc: "one base filter on album title, two user filters on release date and album ID",
			baseFilters: []TableField{
				AlbumTitleTableField,
			},
			userFilters: map[string]TableField{
				"ReleaseDate": ReleaseDateTableField,
				"AlbumId":     AlbumIDTableField,
			},
			wantNumFiltersIndexed: map[TableName]int{
				AlbumsTableName:                          0,
				AlbumsByTitleReleaseDateTableName:        2,
				AlbumsByTitleAlbumIDReleaseDateTableName: 3,
			},
		},
		{
			desc: "two user filters on release date (using an example of external One Platform filter field) and album title",
			userFilters: map[string]TableField{
				"greatest_release_date": ReleaseDateTableField,
				"AlbumTitle":            AlbumTitleTableField,
			},
			wantNumFiltersIndexed: map[TableName]int{
				AlbumsTableName:                          0,
				AlbumsByTitleReleaseDateTableName:        2,
				AlbumsByTitleAlbumIDReleaseDateTableName: 1,
			},
		},
	}
	updatedAlbums := mustAddSortDirectionToOrderedFieldsAndIndices(t, Albums)
	updatedAlbumsByTitleReleaseDate := mustAddSortDirectionToOrderedFieldsAndIndices(t, AlbumsByTitleReleaseDate)
	updatdAlbumsByTitleAlbumIDReleaseDate := mustAddSortDirectionToOrderedFieldsAndIndices(t, AlbumsByTitleAlbumIDReleaseDate)

	for _, tt := range tests {
		// candidateTables must be redeclared in each test since it holds metadata that gets updated by
		// updateNumFiltersIndexed().

		candidateTables := map[TableName]*candidateTable{
			AlbumsTableName: {
				t: updatedAlbums,
			},
			AlbumsByTitleReleaseDateTableName: {
				t: updatedAlbumsByTitleReleaseDate,
			},
			AlbumsByTitleAlbumIDReleaseDateTableName: {
				t: updatdAlbumsByTitleAlbumIDReleaseDate,
			},
		}

		updateNumFiltersIndexed(candidateTables, tt.baseFilters, tt.userFilters)
		for _, ct := range candidateTables {
			if ct.numFiltersIndexed != tt.wantNumFiltersIndexed[ct.t.Name] {
				t.Errorf("%q: table %q: got %d num filters indexed, want %d", tt.desc, ct.t.Name, ct.numFiltersIndexed, tt.wantNumFiltersIndexed[ct.t.Name])
			}
		}
	}
}

func TestRemoveIfNullFilterUnsatisfied(t *testing.T) {
	tests := []struct {
		desc        string
		baseFilters []TableField
		userFilters map[string]TableField
		wantCTNames []TableName
	}{
		{
			desc:        "no base or user filters, can't use null filtered candidate table",
			wantCTNames: []TableName{AlbumsTableName},
		},
		{
			desc: "base filter only, only filtering on the non-null filtered field (AlbumTitle), can't use null filtered candidate table",
			baseFilters: []TableField{
				AlbumTitleTableField,
			},
			wantCTNames: []TableName{AlbumsTableName},
		},
		{
			desc: "base filter and user filter, filtering on one of the null filtered fields (ReleaseDate), can use null filtered candidate table",
			baseFilters: []TableField{
				AlbumTitleTableField,
			},
			userFilters: map[string]TableField{
				"ReleaseDate": ReleaseDateTableField,
			},
			wantCTNames: []TableName{AlbumsTableName, AlbumsByTitleReleaseDateRatingTableName},
		},
		{
			desc: "user filter only, filtering on one of the null filtered fields (Rating), can use null filtered candidate table",
			userFilters: map[string]TableField{
				"Rating": RatingTableField,
			},
			wantCTNames: []TableName{AlbumsTableName, AlbumsByTitleReleaseDateRatingTableName},
		},
		{
			desc: "user filter only, filtering on all of the null filtered fields, can use null filtered candidate table",
			userFilters: map[string]TableField{
				"ReleaseDate": ReleaseDateTableField,
				"Rating":      RatingTableField,
			},
			wantCTNames: []TableName{AlbumsTableName, AlbumsByTitleReleaseDateRatingTableName},
		},
		{
			desc: "base filter and user filter, filtering on all fields, can use null filtered candidate table",
			baseFilters: []TableField{
				AlbumTitleTableField,
			},
			userFilters: map[string]TableField{
				"ReleaseDate": ReleaseDateTableField,
				"Rating":      RatingTableField,
			},
			wantCTNames: []TableName{AlbumsTableName, AlbumsByTitleReleaseDateRatingTableName},
		},
		{
			desc: "user filter only, filtering on all the null filtered fields, but user filter field on release date uses an example of external One Platform filter field, can use null filtered candidate table",
			userFilters: map[string]TableField{
				"greatest_release_date": ReleaseDateTableField,
				"Rating":                RatingTableField,
			},
			wantCTNames: []TableName{AlbumsTableName, AlbumsByTitleReleaseDateRatingTableName},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			candidateTables := map[TableName]*candidateTable{
				AlbumsTableName: {
					t: Albums,
				},
				AlbumsByTitleReleaseDateRatingTableName: {
					t: AlbumsByTitleReleaseDateRating,
				},
			}

			removeIfNullFilterUnsatisfied(candidateTables, tt.baseFilters, tt.userFilters)

			var ctNames []TableName
			for ctName := range candidateTables {
				ctNames = append(ctNames, ctName)
			}

			if diff := cmp.Diff(tt.wantCTNames, ctNames, cmpopts.EquateEmpty(), cmpopts.SortSlices(func(a, b TableName) bool { return a < b })); diff != "" {
				t.Errorf("removeIfNullFilterUnsatisfied() returned diff (want -> got):\n%s", diff)
			}
		})
	}
}

func TestGenerateQueryPageTokensWithSortDirectionReturnsExpectedConditionsAndOrdering(t *testing.T) {
	defaultTable := Singers
	defaultTable.OrderByFields = []TableField{}
	defaultTable.OrderedIndexTableFields = []TableField{}
	selectFields := []TableField{SingerIDTableField, SingerNameTableField}
	tests := []struct {
		desc                        string
		orderedIndicesWithSortOrder []UserOrderedIndex
		orderByFieldsWithSortOrder  []UserOrderedIndex
		userOrderBy                 []UserOrderedIndex
		wantPageTokens              map[TableField]int
		// This is the query we want when using the page token fields from an initial query, that has no
		// page token fields specified, in subsequent queries to page.
		wantQuery spanner.Statement
	}{
		{
			desc: "one asc orderedIndicesWithSortorder",
			orderedIndicesWithSortOrder: []UserOrderedIndex{
				{
					TableField: SingerIDTableField,
					SortOrder:  Asc,
				},
			},
			wantPageTokens: map[TableField]int{SingerIDTableField: 0},
			wantQuery:      spanner.NewStatement(`SELECT s.SingerId, s.SingerName FROM Singers s WHERE ((s.SingerId > @fb0)) ORDER BY s.SingerId`),
		},
		{
			desc: "one desc orderedIndicesWithSortorder",
			orderedIndicesWithSortOrder: []UserOrderedIndex{
				{
					TableField: SingerIDTableField,
					SortOrder:  Desc,
				},
			},
			wantPageTokens: map[TableField]int{SingerIDTableField: 0},
			wantQuery:      spanner.NewStatement(`SELECT s.SingerId, s.SingerName FROM Singers s WHERE ((s.SingerId < @fb0)) ORDER BY s.SingerId DESC`),
		},
		{
			desc: "multiple orderedIndicesWithSortorder",
			orderedIndicesWithSortOrder: []UserOrderedIndex{
				{
					TableField: SingerIDTableField,
					SortOrder:  Asc,
				},
				{
					TableField: SingerNameTableField,
					SortOrder:  Desc,
				},
			},
			wantPageTokens: map[TableField]int{SingerIDTableField: 0, SingerNameTableField: 1},
			wantQuery:      spanner.NewStatement(`SELECT s.SingerId, s.SingerName FROM Singers s WHERE ((s.SingerId = @fb0 AND s.SingerName < @fb1) OR (s.SingerId > @fb2)) ORDER BY s.SingerId, s.SingerName DESC`),
		},
		{
			desc: "orderedIndicesWithSortorder is ignored when orderByFieldsWithSortOrder is present",
			orderedIndicesWithSortOrder: []UserOrderedIndex{
				{
					TableField: SingerIDTableField,
					SortOrder:  Desc,
				},
			},
			orderByFieldsWithSortOrder: []UserOrderedIndex{
				{
					TableField: SingerNameTableField,
					SortOrder:  Desc,
				},
			},
			wantPageTokens: map[TableField]int{SingerNameTableField: 1},
			wantQuery:      spanner.NewStatement(`SELECT s.SingerId, s.SingerName FROM Singers s WHERE ((s.SingerName < @fb0)) ORDER BY s.SingerName DESC`),
		},
		{
			desc: "userOrder comes before orderedIndicesWithSortorder",
			orderedIndicesWithSortOrder: []UserOrderedIndex{
				{
					TableField: SingerIDTableField,
					SortOrder:  Desc,
				},
			},
			userOrderBy: []UserOrderedIndex{
				{
					TableField: SingerNameTableField,
					SortOrder:  Desc,
				},
			},
			wantPageTokens: map[TableField]int{SingerIDTableField: 0, SingerNameTableField: 1},
			wantQuery:      spanner.NewStatement(`SELECT s.SingerId, s.SingerName FROM Singers s WHERE ((s.SingerName = @fb0 AND s.SingerId < @fb1) OR (s.SingerName < @fb2)) ORDER BY s.SingerName DESC, s.SingerId DESC`),
		},
		{
			desc: "userOrder comes before orderByFieldsWithSortOrder",
			orderByFieldsWithSortOrder: []UserOrderedIndex{
				{
					TableField: SingerIDTableField,
					SortOrder:  Asc,
				},
			},
			userOrderBy: []UserOrderedIndex{
				{
					TableField: SingerNameTableField,
					SortOrder:  Asc,
				},
			},
			wantPageTokens: map[TableField]int{SingerIDTableField: 0, SingerNameTableField: 1},
			wantQuery:      spanner.NewStatement(`SELECT s.SingerId, s.SingerName FROM Singers s WHERE ((s.SingerName = @fb0 AND s.SingerId > @fb1) OR (s.SingerName > @fb2)) ORDER BY s.SingerName, s.SingerId`),
		},
		{
			desc: "user order differ from orderedIndices in sort direction",
			orderedIndicesWithSortOrder: []UserOrderedIndex{
				{
					TableField: SingerIDTableField,
					SortOrder:  Asc,
				},
			},
			userOrderBy: []UserOrderedIndex{
				{
					TableField: SingerIDTableField,
					SortOrder:  Desc,
				},
			},
			wantPageTokens: map[TableField]int{SingerIDTableField: 0},
			wantQuery:      spanner.NewStatement(`SELECT s.SingerId, s.SingerName FROM Singers s WHERE ((s.SingerId < @fb0)) ORDER BY s.SingerId DESC`),
		},
		{
			desc: "user order without sort direction",
			userOrderBy: []UserOrderedIndex{
				{
					TableField: SingerIDTableField,
				},
			},
			wantPageTokens: map[TableField]int{SingerIDTableField: 0},
			wantQuery:      spanner.NewStatement(`SELECT s.SingerId, s.SingerName FROM Singers s WHERE ((s.SingerId > @fb0)) ORDER BY s.SingerId`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			table := defaultTable
			table.OrderedIndicesWithSortDirection = tt.orderedIndicesWithSortOrder
			table.OrderByFieldsWithSortDirection = tt.orderByFieldsWithSortOrder

			qg, err := NewOptimizableGenerator([]TableView{table}, nil, nil, nil, false, false, DeduplicateSelects(true))
			if err != nil {
				t.Fatalf("NewOptimizableGenerator() returned error %v", err)
			}

			// Initial query without specifying page token fields
			pageTokensMap, _, err := qg.GenerateQuery(spannerdialect.NewStatement(nil), selectFields, nil, nil, nil, tt.userOrderBy, 0, 0, nil, nil)
			if err != nil {
				t.Fatalf("Initial GenerateQuery() returned error %v", err)
			}
			if diff := cmp.Diff(tt.wantPageTokens, pageTokensMap, protocmp.Transform()); diff != "" {
				t.Fatalf("Initial GenerateQuery() returned unexpected page tokens (-want +got):\n%s", diff)
			}

			// Use the page token fields we got back in a subsequent query as the page token to test
			// paging constructs the correct SQL statement.
			pageTokens := map[TableField]any{}
			for ptField := range pageTokensMap {
				// Just use dummy value of "0" here, since it's not relevant to this test. Also assume
				// string page token field types for this test.
				pageTokens[ptField] = "0"
			}
			q := spannerdialect.NewStatement(nil)
			pageTokensMap, _, err = qg.GenerateQuery(q, selectFields, nil, nil, nil, tt.userOrderBy, 0, 0, pageTokens, nil)
			if err != nil {
				t.Fatalf("Subequent GenerateQuery() returned error %v", err)
			}
			// The page token fields should have not changed.
			if diff := cmp.Diff(tt.wantPageTokens, pageTokensMap, protocmp.Transform()); diff != "" {
				t.Errorf("Subequent GenerateQuery() returned unexpected page tokens (-want +got):\n%s", diff)
			}
			if q.Spanner().SQL != tt.wantQuery.SQL {
				t.Errorf("Subequent GenerateQuery() returned query %q, want %q", q.Spanner().SQL, tt.wantQuery.SQL)
			}
		})
	}
}

func TestWhenBothOrderedIndicesPresentFails(t *testing.T) {
	table := Singers
	table.OrderedIndexTableFields = []TableField{SingerIDTableField}
	table.OrderedIndicesWithSortDirection = []UserOrderedIndex{{TableField: SingerIDTableField, SortOrder: Asc}}
	_, err := NewQueryGenerator([]TableView{table}, nil, nil, nil)
	if err == nil { // if NO error
		t.Errorf("NewQueryGenerator() succeeded unexpectedly, want error")
	}
}

func TestWhenBothOrderedFieldsPresentFails(t *testing.T) {
	table := Singers
	table.OrderByFields = []TableField{SingerIDTableField}
	table.OrderByFieldsWithSortDirection = []UserOrderedIndex{{TableField: SingerIDTableField, SortOrder: Asc}}
	_, err := NewQueryGenerator([]TableView{table}, nil, nil, nil)
	if err == nil { // if NO error
		t.Errorf("NewQueryGenerator() succeeded unexpectedly, want error")
	}
}

func TestUserOrderDifferentFromOrderedIndicesWithOrderOptimizierFails(t *testing.T) {
	singersTable := Singers
	singersTable.OrderedIndexTableFields = []TableField{SingerIDTableField} // Ascending by default
	userOrderByFields := []UserOrderedIndex{{TableField: SingerIDTableField, SortOrder: Desc}}
	updatedSingersTable := mustAddSortDirectionToOrderedFieldsAndIndices(t, singersTable)

	tests := []struct {
		name  string
		table TableView
	}{
		{
			name:  "ordered indices without sort direction",
			table: singersTable,
		},
		{
			name:  "ordered indices with sort direction",
			table: updatedSingersTable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optimizeOrder := true
			qg, err := NewOptimizableGenerator([]TableView{tt.table}, nil, nil, nil, false, optimizeOrder)
			if err != nil {
				t.Fatalf("NewQueryGenerator() returned error %v", err)
			}
			_, _, err = qg.GenerateQuery(spannerdialect.NewStatement(nil), []TableField{SingerIDTableField}, nil, nil, nil, userOrderByFields, 0, 0, nil, nil)
			if err == nil { // if NO error
				t.Errorf("GenerateQuery() with orderByFields %v succeeded unexpectedly, want error", userOrderByFields)
			}
		})
	}
}

func TestUserOrderDifferentFromOrderedIndicesWithoutOrderOptimizierSucceeds(t *testing.T) {
	singersTable := Singers
	singersTable.OrderedIndexTableFields = []TableField{SingerIDTableField} // Ascending by default
	userOrderByFields := []UserOrderedIndex{{TableField: SingerIDTableField, SortOrder: Desc}}
	updatedSingersTable := mustAddSortDirectionToOrderedFieldsAndIndices(t, singersTable)

	tests := []struct {
		name  string
		table TableView
	}{
		{
			name:  "ordered indices without sort direction",
			table: singersTable,
		},
		{
			name:  "ordered indices with sort direction",
			table: updatedSingersTable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optimizeOrder := false
			qg, err := NewOptimizableGenerator([]TableView{tt.table}, nil, nil, nil, false, optimizeOrder)
			if err != nil {
				t.Fatalf("NewQueryGenerator() returned error %v", err)
			}
			_, _, err = qg.GenerateQuery(spannerdialect.NewStatement(nil), []TableField{SingerIDTableField}, nil, nil, nil, userOrderByFields, 0, 0, nil, nil)
			if err != nil {
				t.Errorf("GenerateQuery() with orderByFields %v returned error %v", userOrderByFields, err)
			}
		})
	}
}

func TestUserOrderNotInSelectStatementReturnsExcpectedColumnsAndPageTokens(t *testing.T) {
	table := Singers
	table.OrderedIndexTableFields = []TableField{SingerIDTableField}
	selectFields := []TableField{SingerIDTableField}
	userOrder := []UserOrderedIndex{{TableField: SingerNameTableField, SortOrder: Desc}}
	wantQuery := spanner.NewStatement(`SELECT s.SingerId, s.SingerName FROM Singers s ORDER BY s.SingerName DESC, s.SingerId`)
	wantPageTokens := map[TableField]int{SingerIDTableField: 0, SingerNameTableField: 1}

	qg, err := NewOptimizableGenerator([]TableView{table}, nil, nil, nil, false, false, DeduplicateSelects(true))
	if err != nil {
		t.Fatalf("NewOptimizableGenerator() returned error %v", err)
	}

	q := spannerdialect.NewStatement(nil)
	pageTokensMap, _, err := qg.GenerateQuery(q, selectFields, nil, nil, nil, userOrder, 0, 0, nil, nil)
	if err != nil {
		t.Fatalf("GenerateQuery() returned error %v", err)
	}
	if diff := cmp.Diff(wantPageTokens, pageTokensMap, protocmp.Transform()); diff != "" {
		t.Errorf("GenerateQuery() returned unexpected page tokens (-want +got):\n%s", diff)
	}
	if q.Spanner().SQL != wantQuery.SQL {
		t.Errorf("GenerateQuery() returned query %q, want %q", q.Spanner().SQL, wantQuery.SQL)
	}
}

func TestAddSortDirectionToOrderedFieldsAndIndicesCopiesInputTable(t *testing.T) {
	table := Singers
	table.OrderedIndexTableFields = []TableField{SingerIDTableField}
	table.OrderedIndicesWithSortDirection = []UserOrderedIndex{}
	table.OrderByFields = []TableField{SingerNameTableField}
	table.OrderByFieldsWithSortDirection = []UserOrderedIndex{}

	updatedTable, err := addSortDirectionToOrderedFieldsAndIndices(table)
	if err != nil {
		t.Fatalf("addSortDirectionToOrderedFieldsAndIndices() returned error %v", err)
	}
	// Verifying the correctness of the initial table.
	if diff := cmp.Diff(table.OrderedIndexTableFields, []TableField{SingerIDTableField}, protocmp.Transform()); diff != "" {
		t.Errorf("addSortDirectionToOrderedFieldsAndIndices() changed OrderedIndexTableFields of the input table (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(table.OrderedIndicesWithSortDirection, []UserOrderedIndex{}, protocmp.Transform()); diff != "" {
		t.Errorf("addSortDirectionToOrderedFieldsAndIndices() changed OrderedIndicesWithSortDirection of the input table (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(table.OrderByFields, []TableField{SingerNameTableField}, protocmp.Transform()); diff != "" {
		t.Errorf("addSortDirectionToOrderedFieldsAndIndices() changed OrderByFields of the input table (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(table.OrderByFieldsWithSortDirection, []UserOrderedIndex{}, protocmp.Transform()); diff != "" {
		t.Errorf("addSortDirectionToOrderedFieldsAndIndices() changed OrderByFieldsWithSortDirection of the input table (-want +got):\n%s", diff)
	}
	// Verifying the correctness of the returned table.
	if diff := cmp.Diff(updatedTable.OrderedIndexTableFields, []TableField{}, protocmp.Transform()); diff != "" {
		t.Errorf("addSortDirectionToOrderedFieldsAndIndices() returned unexpected OrderedIndexTableFields (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(updatedTable.OrderedIndicesWithSortDirection, []UserOrderedIndex{{TableField: SingerIDTableField, SortOrder: Asc}}, protocmp.Transform()); diff != "" {
		t.Errorf("addSortDirectionToOrderedFieldsAndIndices() returned unexpected OrderedIndicesWithSortDirection (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(updatedTable.OrderByFields, []TableField{}, protocmp.Transform()); diff != "" {
		t.Errorf("addSortDirectionToOrderedFieldsAndIndices() returned unexpected OrderByFields (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(updatedTable.OrderByFieldsWithSortDirection, []UserOrderedIndex{{TableField: SingerNameTableField, SortOrder: Asc}}, protocmp.Transform()); diff != "" {
		t.Errorf("addSortDirectionToOrderedFieldsAndIndices() returned unexpected OrderByFieldsWithSortDirection (-want +got):\n%s", diff)
	}
}

func mustAddSortDirectionToOrderedFieldsAndIndices(t *testing.T, tableView TableView) TableView {
	t.Helper()
	tv, err := addSortDirectionToOrderedFieldsAndIndices(tableView)
	if err != nil {
		t.Fatalf("mustAddSortDirectionToOrderedFieldsAndIndices(%v) failed: %v.", tableView, err)
	}
	return tv
}

func TestGenerateQuery_PageTokenValidationAndNullableCursors(t *testing.T) {
	nullableReleaseDateTable := TableView{
		Name:       "NullableAlbums",
		NameClause: safesql.New("NullableAlbums a"),
		Fields: map[TableField]Member{
			SingerIDTableField: {
				QueryLiteral: safesql.New("a.SingerId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
			},
			ReleaseDateTableField: {
				QueryLiteral: safesql.New("a.ReleaseDate"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Nullable:     true,
			},
		},
		OrderedIndexTableFields: []TableField{SingerIDTableField},
	}

	qg, err := NewQueryGenerator([]TableView{nullableReleaseDateTable}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewQueryGenerator() failed: %v", err)
	}

	t.Run("missing required sort field in page token returns InvalidArgument", func(t *testing.T) {
		_, _, err := qg.GenerateQuery(
			spannerdialect.NewStatement(nil),
			[]TableField{SingerIDTableField},
			nil,
			nil,
			nil,
			[]UserOrderedIndex{{TableField: ReleaseDateTableField, SortOrder: Asc}},
			10,
			0,
			map[TableField]any{SingerIDTableField: int64(1)},
			nil,
		)
		statusErr, ok := status.FromError(err)
		if !ok || statusErr.Code() != codes.InvalidArgument {
			t.Errorf("GenerateQuery() got error %v, want InvalidArgument", err)
		}
	})

	t.Run("missing required index tie-breaker field in page token returns InvalidArgument", func(t *testing.T) {
		_, _, err := qg.GenerateQuery(
			spannerdialect.NewStatement(nil),
			[]TableField{SingerIDTableField},
			nil,
			nil,
			nil,
			[]UserOrderedIndex{{TableField: ReleaseDateTableField, SortOrder: Asc}},
			10,
			0,
			map[TableField]any{ReleaseDateTableField: "2026-01-01"},
			nil,
		)
		statusErr, ok := status.FromError(err)
		if !ok || statusErr.Code() != codes.InvalidArgument {
			t.Errorf("GenerateQuery() got error %v, want InvalidArgument", err)
		}
	})

	t.Run("nullable sort field ASC with nil cursor value generates IS NULL / IS NOT NULL", func(t *testing.T) {
		stmt := spannerdialect.NewStatement(nil)
		_, _, err := qg.GenerateQuery(
			stmt,
			[]TableField{SingerIDTableField},
			nil,
			&fpb.Expression{},
			nil,
			[]UserOrderedIndex{{TableField: ReleaseDateTableField, SortOrder: Asc}},
			10,
			0,
			map[TableField]any{ReleaseDateTableField: nil, SingerIDTableField: int64(42)},
			nil,
		)
		if err != nil {
			t.Fatalf("GenerateQuery() failed: %v", err)
		}
		wantSQL := "SELECT a.SingerId, a.ReleaseDate FROM NullableAlbums a WHERE ((a.ReleaseDate IS NULL AND a.SingerId > @fb0) OR (a.ReleaseDate IS NOT NULL)) ORDER BY a.ReleaseDate, a.SingerId LIMIT 10"
		if stmt.Spanner().SQL != wantSQL {
			t.Errorf("GenerateQuery() SQL = %q, want %q", stmt.Spanner().SQL, wantSQL)
		}
	})

	t.Run("nullable sort field DESC with non-nil cursor value includes OR IS NULL", func(t *testing.T) {
		stmt := spannerdialect.NewStatement(nil)
		_, _, err := qg.GenerateQuery(
			stmt,
			[]TableField{SingerIDTableField},
			nil,
			nil,
			nil,
			[]UserOrderedIndex{{TableField: ReleaseDateTableField, SortOrder: Desc}},
			10,
			0,
			map[TableField]any{ReleaseDateTableField: "2026-01-01", SingerIDTableField: int64(42)},
			nil,
		)
		if err != nil {
			t.Fatalf("GenerateQuery() failed: %v", err)
		}
		wantSQL := "SELECT a.SingerId, a.ReleaseDate FROM NullableAlbums a WHERE ((a.ReleaseDate = @fb0 AND a.SingerId > @fb1) OR ((a.ReleaseDate < @fb2 OR a.ReleaseDate IS NULL))) ORDER BY a.ReleaseDate DESC, a.SingerId LIMIT 10"
		if stmt.Spanner().SQL != wantSQL {
			t.Errorf("GenerateQuery() SQL = %q, want %q", stmt.Spanner().SQL, wantSQL)
		}
	})

	t.Run("nullable sort field DESC with nil cursor value generates IS NULL and FALSE", func(t *testing.T) {
		stmt := spannerdialect.NewStatement(nil)
		_, _, err := qg.GenerateQuery(
			stmt,
			[]TableField{SingerIDTableField},
			nil,
			nil,
			nil,
			[]UserOrderedIndex{{TableField: ReleaseDateTableField, SortOrder: Desc}},
			10,
			0,
			map[TableField]any{ReleaseDateTableField: nil, SingerIDTableField: int64(42)},
			nil,
		)
		if err != nil {
			t.Fatalf("GenerateQuery() failed: %v", err)
		}
		wantSQL := "SELECT a.SingerId, a.ReleaseDate FROM NullableAlbums a WHERE ((a.ReleaseDate IS NULL AND a.SingerId > @fb0) OR (FALSE)) ORDER BY a.ReleaseDate DESC, a.SingerId LIMIT 10"
		if stmt.Spanner().SQL != wantSQL {
			t.Errorf("GenerateQuery() SQL = %q, want %q", stmt.Spanner().SQL, wantSQL)
		}
	})
}

func TestGeneratePostgresQuery(t *testing.T) {
	pgAlbumsTable := Albums
	pgAlbumsTable.Fields = map[TableField]Member{
		SingerIDTableField:    {Name: SingerIDTableField, QueryLiteral: safesql.New("a.SingerId"), Type: filtervisitor.Integer, Selectable: true, Indexed: true},
		AlbumIDTableField:     {Name: AlbumIDTableField, QueryLiteral: safesql.New("a.AlbumId"), Type: filtervisitor.Integer, Selectable: true, Indexed: true},
		AlbumTitleTableField:  {Name: AlbumTitleTableField, QueryLiteral: safesql.New("a.AlbumTitle"), Type: filtervisitor.String, Selectable: true, DisjunctionOptimizable: true},
		ReleaseDateTableField: {Name: ReleaseDateTableField, QueryLiteral: safesql.New("a.ReleaseDate"), Type: filtervisitor.String, Selectable: true, Nullable: true},
	}

	tests := []struct {
		name              string
		baseFilters       []TableField
		baseFilterExp     *fpb.Expression
		userFilter        string
		fullTextFields    []safesql.TrustedSQLString
		orderByFields     []UserOrderedIndex
		pageSize          int
		offset            int
		ptFields          map[TableField]any
		wantStmt          *postgresdialect.Statement
		wantFieldToIdxMap map[TableField]int
	}{
		{
			name:          "base filter, user disjunction filter, and cursor pagination bind positional args in order",
			baseFilters:   []TableField{SingerIDTableField},
			baseFilterExp: predicate.Conjunction(predicate.Int64{Field: string(SingerIDTableField), Value: 7}),
			userFilter:    `AlbumTitle = "foo" OR AlbumTitle = "bar"`,
			pageSize:      10,
			offset:        5,
			ptFields:      map[TableField]any{AlbumIDTableField: int64(42)},
			wantStmt: &postgresdialect.Statement{
				SQL:  "SELECT a.AlbumTitle, a.AlbumId FROM Albums a WHERE (a.SingerId = $1) AND ((a.AlbumTitle = ANY($2))) AND ((a.AlbumId > $3)) ORDER BY a.SingerId, a.AlbumId LIMIT 10 OFFSET 5",
				Args: []any{int64(7), []string{"foo", "bar"}, int64(42)},
			},
			wantFieldToIdxMap: map[TableField]int{AlbumIDTableField: 1},
		},
		{
			name:           "full text search and nullable ASC order by with nil cursor value",
			userFilter:     `" unplugged "`,
			fullTextFields: []safesql.TrustedSQLString{safesql.New("a.SearchTokens")},
			orderByFields:  []UserOrderedIndex{{TableField: ReleaseDateTableField, SortOrder: Asc}},
			pageSize:       5,
			ptFields:       map[TableField]any{ReleaseDateTableField: nil, SingerIDTableField: int64(10), AlbumIDTableField: int64(20)},
			wantStmt: &postgresdialect.Statement{
				SQL:  "SELECT a.AlbumTitle, a.ReleaseDate, a.SingerId, a.AlbumId FROM Albums a WHERE (a.SearchTokens @@ plainto_tsquery('simple', $1)) AND ((a.ReleaseDate IS NULL AND a.SingerId = $2 AND a.AlbumId > $3) OR (a.ReleaseDate IS NULL AND a.SingerId > $4) OR (a.ReleaseDate IS NOT NULL)) ORDER BY a.ReleaseDate ASC NULLS FIRST, a.SingerId, a.AlbumId LIMIT 5",
				Args: []any{" unplugged ", int64(10), int64(20), int64(10)},
			},
			wantFieldToIdxMap: map[TableField]int{
				ReleaseDateTableField: 1,
				SingerIDTableField:    2,
				AlbumIDTableField:     3,
			},
		},
		{
			name:          "nullable DESC order by emits DESC NULLS LAST and cursor (< $N OR IS NULL)",
			orderByFields: []UserOrderedIndex{{TableField: ReleaseDateTableField, SortOrder: Desc}},
			pageSize:      5,
			ptFields:      map[TableField]any{ReleaseDateTableField: "2026-01-01", SingerIDTableField: int64(10), AlbumIDTableField: int64(20)},
			wantStmt: &postgresdialect.Statement{
				SQL:  "SELECT a.AlbumTitle, a.ReleaseDate, a.SingerId, a.AlbumId FROM Albums a WHERE ((a.ReleaseDate = $1 AND a.SingerId = $2 AND a.AlbumId > $3) OR (a.ReleaseDate = $4 AND a.SingerId > $5) OR ((a.ReleaseDate < $6 OR a.ReleaseDate IS NULL))) ORDER BY a.ReleaseDate DESC NULLS LAST, a.SingerId, a.AlbumId LIMIT 5",
				Args: []any{"2026-01-01", int64(10), int64(20), "2026-01-01", int64(10), "2026-01-01"},
			},
			wantFieldToIdxMap: map[TableField]int{
				ReleaseDateTableField: 1,
				SingerIDTableField:    2,
				AlbumIDTableField:     3,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			qg, err := NewOptimizableGenerator([]TableView{pgAlbumsTable}, nil, tc.baseFilters, nil, true, false)
			if err != nil {
				t.Fatalf("NewOptimizableGenerator() failed: %v", err)
			}
			var userFilterExp *fpb.Expression
			if tc.userFilter != "" {
				userFilterExp, err = filter.ParseExpressionWithOptions(fpb.ExpressionParserOptions_builder{
					EnableSimpleQuotedStrings: true,
				}.Build(), tc.userFilter)
				if err != nil {
					t.Fatalf("filter.ParseExpressionWithOptions(%q) failed: %v", tc.userFilter, err)
				}
			}
			gotStmt := postgresdialect.NewStatement("")
			gotIdxMap, _, err := qg.GenerateQuery(
				gotStmt,
				[]TableField{AlbumTitleTableField},
				tc.fullTextFields,
				tc.baseFilterExp,
				userFilterExp,
				tc.orderByFields,
				tc.pageSize,
				tc.offset,
				tc.ptFields,
				nil,
			)
			if err != nil {
				t.Fatalf("GenerateQuery() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.wantStmt, gotStmt, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("GenerateQuery() statement diff (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantFieldToIdxMap, gotIdxMap, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("GenerateQuery() field index map diff (-want +got):\n%s", diff)
			}
		})
	}
}
