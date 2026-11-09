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

// Package metricsquery reads training metrics from the XMC metrics store (ClickHouse or BigQuery)
// and returns them as store-agnostic, proto-free domain types.
package metricsquery

import (
	"context"
	"fmt"

	xmcbigquery "xmanager_cloud/pkg/bigquery"
)

// defaultTable is the metrics table queried when no table name is configured.
const defaultTable = "xmc_metrics"

// Querier runs metric queries against a single metrics table in a ClickHouse or BigQuery store.
//
// A Querier is immutable after construction and safe for concurrent use. Use NewClickHouse or
// NewBigQuery; the zero value is not usable.
type Querier struct {
	backend backend
	table   string
}

// Option configures a Querier.
type Option func(*Querier)

// WithTableName overrides the metrics table name. An empty name keeps the current table.
func WithTableName(name string) Option {
	return func(q *Querier) {
		if name != "" {
			q.table = name
		}
	}
}

// NewClickHouse returns a Querier that reads the default metrics table through conn.
func NewClickHouse(conn ClickHouseConn, opts ...Option) (*Querier, error) {
	if conn == nil {
		return nil, fmt.Errorf("clickhouse connection must not be nil")
	}
	return newQuerier(clickHouseBackend{conn: conn}, defaultTable, opts), nil
}

// NewBigQuery returns a Querier that reads the table referenced by conn.TableRef(), falling back to
// the default metrics table when the reference is empty.
func NewBigQuery(conn xmcbigquery.Conn, opts ...Option) (*Querier, error) {
	if conn == nil {
		return nil, fmt.Errorf("bigquery connection must not be nil")
	}
	table := conn.TableRef()
	if table == "" {
		table = defaultTable
	}
	return newQuerier(bigQueryBackend{conn: conn}, table, opts), nil
}

func newQuerier(b backend, table string, opts []Option) *Querier {
	q := &Querier{backend: b, table: table}
	for _, opt := range opts {
		opt(q)
	}
	return q
}

// Ping checks that the underlying metrics store connection is still usable.
func (q *Querier) Ping(ctx context.Context) error {
	return q.backend.Ping(ctx)
}

// Close releases the underlying metrics store connection.
func (q *Querier) Close() error {
	return q.backend.Close()
}

// queryAll runs query and scans every returned row with scan. kind names the query in errors.
func queryAll[T any](ctx context.Context, b backend, kind, query string, args []any, scan func(rows) (T, error)) ([]T, error) {
	r, err := b.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query %s: %w", kind, err)
	}
	defer r.Close()

	var out []T
	for r.Next() {
		v, err := scan(r)
		if err != nil {
			return nil, fmt.Errorf("failed to scan %s row: %w", kind, err)
		}
		out = append(out, v)
	}
	if err := r.Err(); err != nil {
		return nil, fmt.Errorf("%s rows iteration error: %w", kind, err)
	}
	return out, nil
}
