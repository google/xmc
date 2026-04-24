// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// Package workunitexecutionschema provides the Spanner schema for the WorkUnitExecutions table.
package workunitexecutionschema

// WorkUnitExecutionsTable is the name of the WorkUnitExecutions table.
var WorkUnitExecutionsTable = "WorkUnitExecutions"

// WorkUnitExecutionsColumns is the full list of columns in the WorkUnitExecutions table. It is used
// when fetching rows from the table. Note that we return the XID column, even though the primary
// key is actually the BitReversedXID column (which should be exposed in the WHERE clause).
var WorkUnitExecutionsColumns = []string{
	"Xid",
	"WorkUnitId",
	"ExecutionId",
	"ExecutionMetadata",
	"WorkUnitStatus",
	"CreateTime",
	"UpdateTime",
	"CompleteTime",
}
