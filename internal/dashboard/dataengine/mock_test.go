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

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"xmanager_cloud/pkg/metricsquery/metricsquerytest"
)

type mockClickHouseConn struct {
	queryFn func(ctx context.Context, query string, args ...any) (driver.Rows, error)
	pingFn  func(ctx context.Context) error
}

func (m *mockClickHouseConn) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	if m.queryFn != nil {
		return m.queryFn(ctx, query, args...)
	}
	return nil, fmt.Errorf("queryFn not implemented")
}

func (m *mockClickHouseConn) Ping(ctx context.Context) error {
	if m.pingFn != nil {
		return m.pingFn(ctx)
	}
	return nil
}

type mockRows = metricsquerytest.Rows
