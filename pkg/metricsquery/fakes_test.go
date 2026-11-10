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
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	xmcbigquery "xmanager_cloud/pkg/bigquery"
	"xmanager_cloud/pkg/metricsquery/metricsquerytest"
)

// queryRecorder records the last query and answers it with rows or err.
type queryRecorder struct {
	rows  *metricsquerytest.Rows
	err   error
	query string
	args  []any
}

func (r *queryRecorder) answer(query string, args []any) (*metricsquerytest.Rows, error) {
	r.query, r.args = query, args
	if r.err != nil {
		return nil, r.err
	}
	return r.rows, nil
}

// fakeClickHouseConn is a ClickHouseConn that does not implement io.Closer.
type fakeClickHouseConn struct {
	queryRecorder
	pingErr error
}

func (c *fakeClickHouseConn) Query(_ context.Context, query string, args ...any) (driver.Rows, error) {
	rows, err := c.answer(query, args)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (c *fakeClickHouseConn) Ping(context.Context) error { return c.pingErr }

// closableClickHouseConn is a ClickHouseConn that implements io.Closer.
type closableClickHouseConn struct {
	fakeClickHouseConn
	closed bool
}

func (c *closableClickHouseConn) Close() error {
	c.closed = true
	return nil
}

type fakeBigQueryConn struct {
	queryRecorder
	tableRef string
	pingErr  error
	closed   bool
}

func (c *fakeBigQueryConn) Query(_ context.Context, query string, args ...any) (xmcbigquery.Rows, error) {
	rows, err := c.answer(query, args)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (c *fakeBigQueryConn) Ping(context.Context) error { return c.pingErr }
func (c *fakeBigQueryConn) TableRef() string           { return c.tableRef }

func (c *fakeBigQueryConn) Close() error {
	c.closed = true
	return nil
}

// newRecordingQuerier returns a Querier for d over table whose queries are answered by rec.
func newRecordingQuerier(t *testing.T, d dialect, table string, rec queryRecorder) (*Querier, *queryRecorder) {
	t.Helper()
	if d == dialectBigQuery {
		conn := &fakeBigQueryConn{queryRecorder: rec, tableRef: table}
		q, err := NewBigQuery(conn)
		if err != nil {
			t.Fatalf("NewBigQuery() error: %v", err)
		}
		return q, &conn.queryRecorder
	}
	conn := &fakeClickHouseConn{queryRecorder: rec}
	q, err := NewClickHouse(conn, WithTableName(table))
	if err != nil {
		t.Fatalf("NewClickHouse() error: %v", err)
	}
	return q, &conn.queryRecorder
}
