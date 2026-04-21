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
