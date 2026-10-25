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
	"errors"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

func TestClickHouseBackend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	nonClosable := clickHouseBackend{conn: &mockClickHouseConn{
		queryFn: func(context.Context, string, ...any) (driver.Rows, error) {
			return &mockRows{data: [][]any{{int64(1)}}}, nil
		},
	}}
	rows, err := nonClosable.Query(ctx, "SELECT 1")
	if err != nil {
		t.Fatalf("Query() error: %v", err)
	}
	_ = rows.Close()
	if err := nonClosable.Ping(ctx); err != nil {
		t.Errorf("Ping() error: %v", err)
	}
	if err := nonClosable.Close(); err != nil {
		t.Errorf("Close() on non-closable conn error: %v", err)
	}

	closable := &closableMockConn{}
	closable.pingFn = func(context.Context) error { return errors.New("offline") }
	backend := clickHouseBackend{conn: closable}
	if err := backend.Ping(ctx); err == nil {
		t.Error("Ping() succeeded on failing conn, want error")
	}
	if err := backend.Close(); err != nil || !closable.closed.Load() {
		t.Errorf("Close() err=%v, closed=%v; want nil, true", err, closable.closed.Load())
	}
}
