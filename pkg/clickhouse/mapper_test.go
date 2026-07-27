// Copyright 2026 Google LLC
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

package clickhouse

import (
	"errors"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	dashboardpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

type mockRows struct {
	cols []string
	data [][]any
	idx  int
	err  error
}

func (m *mockRows) Next() bool {
	if m.idx >= len(m.data) {
		return false
	}
	m.idx++
	return true
}

func (m *mockRows) Scan(dest ...any) error {
	row := m.data[m.idx-1]
	for i, val := range row {
		switch d := dest[i].(type) {
		case *string:
			*d = val.(string)
		case *float64:
			*d = val.(float64)
		case *int64:
			*d = val.(int64)
		case *uint64:
			*d = val.(uint64)
		case *MicroOrTime:
			_ = d.Scan(val)
		case *[][2]float64:
			*d = val.([][2]float64)
		}
	}
	return nil
}

func (m *mockRows) ScanStruct(dest any) error        { return nil }
func (m *mockRows) ColumnTypes() []driver.ColumnType { return nil }
func (m *mockRows) Totals(dest ...any) error         { return nil }
func (m *mockRows) Columns() []string                { return m.cols }
func (m *mockRows) Close() error                     { return nil }
func (m *mockRows) Err() error                       { return m.err }
func (m *mockRows) HasData() bool                    { return len(m.data) > 0 }

func TestMicroOrTime_Scan(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  time.Time
	}{
		{
			name:  "Time",
			input: time.Unix(1700000000, 0),
			want:  time.Unix(1700000000, 0),
		},
		{
			name:  "Int64Micro",
			input: int64(1700000000000000),
			want:  time.Unix(1700000000, 0),
		},
		{
			name:  "Uint64Micro",
			input: uint64(1700000000000000),
			want:  time.Unix(1700000000, 0),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var m MicroOrTime
			if err := m.Scan(tc.input); err != nil {
				t.Fatalf("unexpected Scan error: %v", err)
			}
			if !m.Time.Equal(tc.want) {
				t.Errorf("got %v, want %v", m.Time, tc.want)
			}
			if protoTS := m.ToProtoTimestamp(); protoTS == nil || protoTS.Seconds != 1700000000 {
				t.Errorf("ToProtoTimestamp() invalid: %v", protoTS)
			}
		})
	}
}

func TestMicroOrTime_ScanError(t *testing.T) {
	var m MicroOrTime
	if err := m.Scan("invalid-type"); err == nil {
		t.Errorf("expected error when scanning string into MicroOrTime")
	}
}

func TestMapSummaryRows(t *testing.T) {
	rows := &mockRows{
		data: [][]any{
			{"loss", 0.5, 0.1, 0.9, 0.45, int64(100), int64(1700000000000000)},
		},
	}
	res, err := MapSummaryRows(rows)
	if err != nil {
		t.Fatalf("MapSummaryRows error: %v", err)
	}
	if res == nil || res.MetricName != "loss" || res.LatestValue != 0.5 {
		t.Errorf("unexpected summary result: %v", res)
	}
}

func TestMapSummaryRows_Error(t *testing.T) {
	rows := &mockRows{
		err: errors.New("db error"),
	}
	_, err := MapSummaryRows(rows)
	if err == nil {
		t.Errorf("expected error when rows.Err() returns an error")
	}
}

func TestMapScatterRows(t *testing.T) {
	rows := &mockRows{
		cols: make([]string, 8),
		data: [][]any{
			{"loss", int64(1), int64(10), int64(5), int64(1700000000000000), 0.5, 0.1, 0.9},
		},
	}
	res, err := MapScatterRows(rows)
	if err != nil {
		t.Fatalf("MapScatterRows error: %v", err)
	}
	if res == nil || len(res.Points) != 1 || res.MetricName != "loss" {
		t.Errorf("unexpected scatter result: %v", res)
	}
}

func TestMapScatterRows_Error(t *testing.T) {
	rows := &mockRows{
		cols: make([]string, 8),
		err:  errors.New("db error"),
	}
	_, err := MapScatterRows(rows)
	if err == nil {
		t.Errorf("expected error when rows.Err() returns an error after scatter loop")
	}
}

func TestMapScatterLTTBRows(t *testing.T) {
	rows := &mockRows{
		cols: make([]string, 4),
		data: [][]any{
			{"loss", int64(1), int64(10), [][2]float64{{1.0, 0.5}, {2.0, 0.4}}},
		},
	}
	res, err := MapScatterLTTBRows(rows)
	if err != nil {
		t.Fatalf("MapScatterLTTBRows error: %v", err)
	}
	if res == nil || len(res.Points) != 2 || res.MetricName != "loss" {
		t.Errorf("unexpected lttb scatter result: %v", res)
	}
}

func TestMapScatterLTTBRows_Error(t *testing.T) {
	rows := &mockRows{
		cols: make([]string, 4),
		err:  errors.New("db error"),
	}
	_, err := MapScatterLTTBRows(rows)
	if err == nil {
		t.Errorf("expected error when rows.Err() returns an error in lttb scatter")
	}
}

func TestMapBarRows(t *testing.T) {
	rows := &mockRows{
		data: [][]any{
			{"cat_metric", "cat1", uint64(42)},
		},
	}
	res, err := MapBarRows(rows)
	if err != nil {
		t.Fatalf("MapBarRows error: %v", err)
	}
	if res == nil || len(res.Buckets) != 1 || res.Buckets[0].ValueOrCount != 42 {
		t.Errorf("unexpected bar result: %v", res)
	}
}

func TestMapBarRows_Error(t *testing.T) {
	rows := &mockRows{
		err: errors.New("db error"),
	}
	_, err := MapBarRows(rows)
	if err == nil {
		t.Errorf("expected error when rows.Err() returns an error after bar loop")
	}
}

func TestMapPlotRows(t *testing.T) {
	rows := &mockRows{
		data: [][]any{
			{"loss", 0.5, 0.1, 0.9, 0.45, int64(100), int64(1700000000000000)},
		},
	}
	plot := &dashboardpb.PlotSpec{
		PlotId: "p1",
		Type:   dashboardpb.PlotType_SUMMARY_BOX,
	}
	res, err := MapPlotRows(plot, rows, nil)
	if err != nil {
		t.Fatalf("MapPlotRows error: %v", err)
	}
	if res == nil || res.PlotId != "p1" {
		t.Errorf("unexpected result: %v", res)
	}

	_, err = MapPlotRows(nil, rows, nil)
	if err == nil {
		t.Errorf("expected error on nil plot")
	}
	_, err = MapPlotRows(plot, nil, nil)
	if err == nil {
		t.Errorf("expected error on nil rows")
	}
}
