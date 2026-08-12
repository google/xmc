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

// Package dataengine compiles and executes analytics queries against ClickHouse for XMC dashboard charts.
package dataengine

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ClickHouseConn abstracts ClickHouse database connection methods required by DataEngine.
type ClickHouseConn interface {
	Query(ctx context.Context, query string, args ...any) (driver.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) driver.Row
	Ping(ctx context.Context) error
}

// Compile-time interface check.
var _ ClickHouseConn = (driver.Conn)(nil)
