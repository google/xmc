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

package dataengine

import (
	"context"
	"fmt"
	"reflect"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type mockClickHouseConn struct {
	queryFn    func(ctx context.Context, query string, args ...any) (driver.Rows, error)
	queryRowFn func(ctx context.Context, query string, args ...any) driver.Row
	pingFn     func(ctx context.Context) error
}

func (m *mockClickHouseConn) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	if m.queryFn != nil {
		return m.queryFn(ctx, query, args...)
	}
	return nil, fmt.Errorf("queryFn not implemented")
}

func (m *mockClickHouseConn) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	if m.queryRowFn != nil {
		return m.queryRowFn(ctx, query, args...)
	}
	return &mockRow{err: fmt.Errorf("queryRowFn not implemented")}
}

func (m *mockClickHouseConn) Ping(ctx context.Context) error {
	if m.pingFn != nil {
		return m.pingFn(ctx)
	}
	return nil
}

type mockRow struct {
	vals []any
	err  error
}

func (r *mockRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return assignValues(dest, r.vals)
}

func (r *mockRow) ScanStruct(dest any) error {
	return fmt.Errorf("ScanStruct not implemented in mock")
}

func (r *mockRow) Err() error {
	return r.err
}

type mockRows struct {
	data   [][]any
	idx    int
	closed bool
	err    error
}

func (r *mockRows) Next() bool {
	if r.idx < len(r.data) {
		r.idx++
		return true
	}
	return false
}

func (r *mockRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.data) {
		return fmt.Errorf("scan called out of range")
	}
	row := r.data[r.idx-1]
	return assignValues(dest, row)
}

func (r *mockRows) ScanStruct(dest any) error {
	return fmt.Errorf("ScanStruct not implemented in mock")
}

func (r *mockRows) Close() error {
	r.closed = true
	return nil
}

func (r *mockRows) Err() error {
	return r.err
}

func (r *mockRows) Columns() []string {
	return nil
}

func (r *mockRows) Totals(dest ...any) error {
	return nil
}

func (r *mockRows) ColumnTypes() []driver.ColumnType {
	return nil
}

func assignValues(dest []any, src []any) error {
	if len(dest) != len(src) {
		return fmt.Errorf("column count mismatch: got %d dest, %d src", len(dest), len(src))
	}
	for i, d := range dest {
		s := src[i]
		dv := reflect.ValueOf(d)
		if dv.Kind() != reflect.Ptr || dv.IsNil() {
			return fmt.Errorf("destination %d is not a non-nil pointer", i)
		}
		target := dv.Elem()
		sv := reflect.ValueOf(s)
		if sv.IsValid() && sv.Type().AssignableTo(target.Type()) {
			target.Set(sv)
		} else if sv.IsValid() && sv.Type().ConvertibleTo(target.Type()) {
			target.Set(sv.Convert(target.Type()))
		} else {
			return fmt.Errorf("cannot assign %T to %T", s, target.Interface())
		}
	}
	return nil
}
