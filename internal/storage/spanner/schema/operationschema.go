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
// Package operationschema contains the schema for the Operations table.
package operationschema

// OperationsTable is the name of the Operations table.
var OperationsTable = "Operations"

// OperationColumns is the full list of columns in the Operations table. It is used when fetching
// or inserting into the table. Note that we return the XID column, even though the primary key is
// actually the BitReversedXID column (which should be exposed in the WHERE clause).
var OperationColumns = []string{
	"Xid",
	"WorkUnitId",
	"OperationId",
	"Done",
	"Status",
	"Response",
	"OperationMetadata",
	"CreateTime",
	"UpdateTime",
}

// OperationUpdateColumns is the list of columns that can be mutated in the Operations table.
// BitReversedXid, WorkUnitId and OperationId are the primary key and cannot be mutated.
var OperationUpdateColumns = []string{
	"BitReversedXid",
	"WorkUnitId",
	"OperationId",
	"Done",
	"Status",
	"Response",
	"OperationMetadata",
	"UpdateTime",
}
