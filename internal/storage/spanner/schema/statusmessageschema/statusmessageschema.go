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
// Package statusmessageschema contains the Spanner schema for the StatusMessages table.
package statusmessageschema

import (
	"api/filter/go/filtervisitor"
	"googlesql/safesql/go/safesql"
	"xmanager_cloud/query_generator/querytools"
)

const (
	statusMessagesTableName = querytools.TableName("StatusMessages")

	bitReversedXidTableField = querytools.TableField("bit_reversed_xid")
	xidTableField            = querytools.TableField("xid")
	workUnitIDTableField     = querytools.TableField("work_unit_id")
	messageIDTableField      = querytools.TableField("message_id")

	severityTableField       = querytools.TableField("severity")
	sourceTableField         = querytools.TableField("source")
	userFacingTextTableField = querytools.TableField("user_facing_text")
	userFacingURLTableField  = querytools.TableField("user_facing_url")
	executionIDTableField    = querytools.TableField("execution_id")
	createTimeTableField     = querytools.TableField("create_time")
	auxiliaryInfoTableField  = querytools.TableField("auxiliary_info")
)

var (
	// StatusMessageTableName is the name of the StatusMessages table in Spanner.
	StatusMessageTableName = "StatusMessages"

	statusMessagesTableView = querytools.TableView{
		Name:       statusMessagesTableName,
		NameClause: safesql.New("StatusMessages s"),
		Cost:       30,
		Fields: map[querytools.TableField]querytools.Member{
			bitReversedXidTableField: {
				Name:         bitReversedXidTableField,
				QueryLiteral: safesql.New("s.BitReversedXid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			xidTableField: {
				Name:         xidTableField,
				QueryLiteral: safesql.New("s.Xid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			workUnitIDTableField: {
				Name:         workUnitIDTableField,
				QueryLiteral: safesql.New("s.WorkUnitId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			messageIDTableField: {
				Name:         messageIDTableField,
				QueryLiteral: safesql.New("s.MessageId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			severityTableField: {
				Name:         severityTableField,
				QueryLiteral: safesql.New("s.Severity"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			sourceTableField: {
				Name:         sourceTableField,
				QueryLiteral: safesql.New("s.Source"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			userFacingTextTableField: {
				Name:         userFacingTextTableField,
				QueryLiteral: safesql.New("s.UserFacingText"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			userFacingURLTableField: {
				Name:         userFacingURLTableField,
				QueryLiteral: safesql.New("s.UserFacingUrl"),
				Selectable:   true,
			},
			executionIDTableField: {
				Name:         executionIDTableField,
				QueryLiteral: safesql.New("s.ExecutionId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
			},
			createTimeTableField: {
				Name:         createTimeTableField,
				QueryLiteral: safesql.New("s.CreateTime"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
			},
			auxiliaryInfoTableField: {
				Name:         auxiliaryInfoTableField,
				QueryLiteral: safesql.New("s.AuxiliaryInfo"),
				Selectable:   true,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: bitReversedXidTableField, SortOrder: querytools.Asc},
			{TableField: createTimeTableField, SortOrder: querytools.Desc},
			{TableField: workUnitIDTableField, SortOrder: querytools.Asc},
			{TableField: messageIDTableField, SortOrder: querytools.Asc},
		},
	}

	// StatusMessageTables is the list of tables that can be used for StatusMessage queries.
	StatusMessageTables = []querytools.TableView{statusMessagesTableView}

	// StatusMessagesSelectFields is the list of fields that can be selected for StatusMessages queries.
	StatusMessagesSelectFields = []querytools.TableField{
		bitReversedXidTableField,
		xidTableField,
		workUnitIDTableField,
		messageIDTableField,
		severityTableField,
		sourceTableField,
		userFacingTextTableField,
		userFacingURLTableField,
		executionIDTableField,
		createTimeTableField,
		auxiliaryInfoTableField,
	}

	// StatusMessagesOrderByFields is the list of fields that can be used for ordering StatusMessages queries.
	StatusMessagesOrderByFields = []querytools.UserOrderedIndex{
		{TableField: createTimeTableField},
	}
)
