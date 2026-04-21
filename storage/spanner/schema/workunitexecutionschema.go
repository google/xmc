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
