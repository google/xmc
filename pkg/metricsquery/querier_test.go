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

package metricsquery

import (
	"context"
	"errors"
	"testing"
)

func TestNewClickHouse(t *testing.T) {
	tests := []struct {
		name      string
		conn      ClickHouseConn
		opts      []Option
		wantErr   bool
		wantTable string
	}{
		{name: "default table", conn: &fakeClickHouseConn{}, wantTable: "xmc_metrics"},
		{name: "table name option", conn: &fakeClickHouseConn{}, opts: []Option{WithTableName("custom")}, wantTable: "custom"},
		{name: "empty table name keeps default", conn: &fakeClickHouseConn{}, opts: []Option{WithTableName("")}, wantTable: "xmc_metrics"},
		{name: "nil option is ignored", conn: &fakeClickHouseConn{}, opts: []Option{nil, WithTableName("custom")}, wantTable: "custom"},
		{name: "nil connection", conn: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := NewClickHouse(tt.conn, tt.opts...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewClickHouse() error = %v, wantErr = %t", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if q.table != tt.wantTable || q.backend.dialect() != dialectClickHouse {
				t.Errorf("NewClickHouse() table = %q, dialect = %v; want %q, clickhouse", q.table, q.backend.dialect(), tt.wantTable)
			}
		})
	}
}

func TestNewBigQuery(t *testing.T) {
	tests := []struct {
		name      string
		conn      *fakeBigQueryConn
		opts      []Option
		wantErr   bool
		wantTable string
	}{
		{name: "table from TableRef", conn: &fakeBigQueryConn{tableRef: "`p.d.t`"}, wantTable: "`p.d.t`"},
		{name: "empty TableRef falls back to default", conn: &fakeBigQueryConn{}, wantTable: "xmc_metrics"},
		{name: "table name option overrides TableRef", conn: &fakeBigQueryConn{tableRef: "`p.d.t`"}, opts: []Option{WithTableName("custom")}, wantTable: "custom"},
		{name: "nil connection", conn: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var q *Querier
			var err error
			if tt.conn == nil {
				q, err = NewBigQuery(nil, tt.opts...)
			} else {
				q, err = NewBigQuery(tt.conn, tt.opts...)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewBigQuery() error = %v, wantErr = %t", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if q.table != tt.wantTable || q.backend.dialect() != dialectBigQuery {
				t.Errorf("NewBigQuery() table = %q, dialect = %v; want %q, bigquery", q.table, q.backend.dialect(), tt.wantTable)
			}
		})
	}
}

func TestQuerierPingAndClose(t *testing.T) {
	offline := errors.New("offline")
	conn := &fakeBigQueryConn{pingErr: offline}
	q, err := NewBigQuery(conn)
	if err != nil {
		t.Fatalf("NewBigQuery() error: %v", err)
	}
	if err := q.Ping(context.Background()); !errors.Is(err, offline) {
		t.Errorf("Ping() = %v, want wrapped %v", err, offline)
	}
	if err := q.Close(); err != nil || !conn.closed {
		t.Errorf("Close() = %v, closed = %t; want nil, true", err, conn.closed)
	}
}
