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
	"fmt"
	"io"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	xmcbigquery "xmanager_cloud/pkg/bigquery"
)

// ClickHouseConn abstracts the ClickHouse connection methods required by a Querier.
type ClickHouseConn interface {
	Query(ctx context.Context, query string, args ...any) (driver.Rows, error)
	Ping(ctx context.Context) error
}

// Compile-time interface check.
var _ ClickHouseConn = (driver.Conn)(nil)

// dialect identifies the SQL dialect spoken by a metrics store.
type dialect int

const (
	dialectClickHouse dialect = iota
	dialectBigQuery
)

// rows is the cursor surface shared by the ClickHouse and BigQuery drivers.
type rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// backend executes parameterized SQL against a metrics store and reports its dialect.
type backend interface {
	Query(ctx context.Context, query string, args ...any) (rows, error)
	Ping(ctx context.Context) error
	Close() error
	dialect() dialect
}

type clickHouseBackend struct{ conn ClickHouseConn }

var _ backend = clickHouseBackend{}

func (b clickHouseBackend) Query(ctx context.Context, query string, args ...any) (rows, error) {
	return b.conn.Query(ctx, query, args...)
}

func (b clickHouseBackend) Ping(ctx context.Context) error {
	if err := b.conn.Ping(ctx); err != nil {
		return fmt.Errorf("clickhouse ping failed: %w", err)
	}
	return nil
}

func (b clickHouseBackend) Close() error {
	if closer, ok := b.conn.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (clickHouseBackend) dialect() dialect { return dialectClickHouse }

type bigQueryBackend struct{ conn xmcbigquery.Conn }

var _ backend = bigQueryBackend{}

func (b bigQueryBackend) Query(ctx context.Context, query string, args ...any) (rows, error) {
	return b.conn.Query(ctx, query, args...)
}

func (b bigQueryBackend) Ping(ctx context.Context) error {
	if err := b.conn.Ping(ctx); err != nil {
		return fmt.Errorf("bigquery ping failed: %w", err)
	}
	return nil
}

func (b bigQueryBackend) Close() error { return b.conn.Close() }

func (bigQueryBackend) dialect() dialect { return dialectBigQuery }
