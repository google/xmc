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

package metricsquerytest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

type scanned struct {
	Step  int64
	Value float64
	Name  string
	TS    time.Time
}

func TestRowsIteratesAndScans(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	iterErr := errors.New("stream reset")
	r := &Rows{
		Data:    [][]any{{int32(1), float32(0.5), "loss", ts}, {int64(2), 0.25, "loss", ts}},
		IterErr: iterErr,
	}

	var got []scanned
	for r.Next() {
		var s scanned
		if err := r.Scan(&s.Step, &s.Value, &s.Name, &s.TS); err != nil {
			t.Fatalf("Scan() error: %v", err)
		}
		got = append(got, s)
	}

	want := []scanned{{1, 0.5, "loss", ts}, {2, 0.25, "loss", ts}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("scanned rows mismatch (-want +got):\n%s", diff)
	}
	if !errors.Is(r.Err(), iterErr) {
		t.Errorf("Err() = %v, want %v", r.Err(), iterErr)
	}
	if err := r.Close(); err != nil || !r.Closed() {
		t.Errorf("Close() = %v, Closed() = %t; want nil, true", err, r.Closed())
	}
}

func TestRowsScanErrors(t *testing.T) {
	var (
		n int64
		s string
	)
	tests := []struct {
		name string
		rows *Rows
		next bool
		dest []any
	}{
		{name: "before Next", rows: &Rows{Data: [][]any{{int64(1)}}}, dest: []any{&n}},
		{name: "column count mismatch", rows: &Rows{Data: [][]any{{int64(1), "x"}}}, next: true, dest: []any{&n}},
		{name: "non-pointer destination", rows: &Rows{Data: [][]any{{int64(1)}}}, next: true, dest: []any{n}},
		{name: "nil source", rows: &Rows{Data: [][]any{{nil}}}, next: true, dest: []any{&n}},
		{name: "inconvertible type", rows: &Rows{Data: [][]any{{[]int{1}}}}, next: true, dest: []any{&s}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.next {
				tt.rows.Next()
			}
			if err := tt.rows.Scan(tt.dest...); err == nil {
				t.Error("Scan() succeeded, want error")
			}
		})
	}
}

func TestRowsUnsupportedMethods(t *testing.T) {
	r := &Rows{}
	if err := r.ScanStruct(&struct{}{}); err == nil {
		t.Error("ScanStruct() succeeded, want error")
	}
	if r.Columns() != nil || r.ColumnTypes() != nil || r.Totals() != nil {
		t.Error("Columns/ColumnTypes/Totals returned non-nil, want nil")
	}
}
