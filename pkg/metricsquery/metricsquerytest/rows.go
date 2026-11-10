// Copyright 2026 Google LLC
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

// Package metricsquerytest provides test doubles for code that reads metrics through
// ClickHouse or BigQuery cursors.
package metricsquerytest

import (
	"fmt"
	"reflect"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	xmcbigquery "xmanager_cloud/pkg/bigquery"
)

// Rows is an in-memory cursor over Data. It satisfies both the ClickHouse driver.Rows and the
// BigQuery xmcbigquery.Rows interfaces.
type Rows struct {
	// Data holds one slice of column values per row, in SELECT order.
	Data [][]any
	// IterErr is returned by Err after iteration.
	IterErr error

	idx    int
	closed bool
}

var (
	_ driver.Rows      = (*Rows)(nil)
	_ xmcbigquery.Rows = (*Rows)(nil)
)

// Next advances to the next row and reports whether one exists.
func (r *Rows) Next() bool {
	if r.idx < len(r.Data) {
		r.idx++
		return true
	}
	return false
}

// Scan copies the current row into dest, converting values where Go allows it.
func (r *Rows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.Data) {
		return fmt.Errorf("scan called out of range")
	}
	return assignValues(dest, r.Data[r.idx-1])
}

// ScanStruct is not supported.
func (r *Rows) ScanStruct(any) error {
	return fmt.Errorf("ScanStruct not implemented in fake")
}

// Close marks the cursor closed.
func (r *Rows) Close() error {
	r.closed = true
	return nil
}

// Closed reports whether Close was called.
func (r *Rows) Closed() bool { return r.closed }

// Err returns IterErr.
func (r *Rows) Err() error { return r.IterErr }

// Columns returns nil.
func (r *Rows) Columns() []string { return nil }

// Totals is a no-op.
func (r *Rows) Totals(...any) error { return nil }

// ColumnTypes returns nil.
func (r *Rows) ColumnTypes() []driver.ColumnType { return nil }

func assignValues(dest []any, src []any) error {
	if len(dest) != len(src) {
		return fmt.Errorf("column count mismatch: got %d dest, %d src", len(dest), len(src))
	}
	for i, d := range dest {
		if err := assignValue(d, src[i]); err != nil {
			return fmt.Errorf("column %d: %w", i, err)
		}
	}
	return nil
}

func assignValue(dest, src any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Ptr || dv.IsNil() {
		return fmt.Errorf("destination is not a non-nil pointer")
	}
	target := dv.Elem()
	sv := reflect.ValueOf(src)
	switch {
	case sv.IsValid() && sv.Type().AssignableTo(target.Type()):
		target.Set(sv)
	case sv.IsValid() && sv.Type().ConvertibleTo(target.Type()):
		target.Set(sv.Convert(target.Type()))
	default:
		return fmt.Errorf("cannot assign %T to %T", src, target.Interface())
	}
	return nil
}
