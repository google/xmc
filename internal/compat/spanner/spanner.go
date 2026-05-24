package spanner

import "cloud.google.com/go/spanner"

type Statement = spanner.Statement
type NullString = spanner.NullString
type NullInt64 = spanner.NullInt64
type NullTime = spanner.NullTime
type Client = spanner.Client
type Row = spanner.Row
type GenericColumnValue = spanner.GenericColumnValue

func NewStatement(sql string) Statement {
	return spanner.NewStatement(sql)
}
