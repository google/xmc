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

// Package dataengine compiles and executes analytics queries for XMC dashboard charts.
package dataengine

import (
	"context"
	"fmt"
	"io"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	xmcbigquery "xmanager_cloud/pkg/bigquery"
)

// ClickHouseConn abstracts ClickHouse database connection methods required by DataEngine.
type ClickHouseConn interface {
	Query(ctx context.Context, query string, args ...any) (driver.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) driver.Row
	Ping(ctx context.Context) error
}

// Compile-time interface check.
var _ ClickHouseConn = (driver.Conn)(nil)

type rowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

type queryBackend interface {
	Query(ctx context.Context, query string, args ...any) (rowScanner, error)
	Ping(ctx context.Context) error
	Close() error
}

type clickHouseBackend struct{ conn ClickHouseConn }

var _ queryBackend = clickHouseBackend{}

func (b clickHouseBackend) Query(ctx context.Context, query string, args ...any) (rowScanner, error) {
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

type bigQueryBackend struct{ conn xmcbigquery.Conn }

var _ queryBackend = bigQueryBackend{}

func (b bigQueryBackend) Query(ctx context.Context, query string, args ...any) (rowScanner, error) {
	return b.conn.Query(ctx, query, args...)
}

func (b bigQueryBackend) Ping(ctx context.Context) error {
	if err := b.conn.Ping(ctx); err != nil {
		return fmt.Errorf("bigquery ping failed: %w", err)
	}
	return nil
}

func (b bigQueryBackend) Close() error { return b.conn.Close() }
