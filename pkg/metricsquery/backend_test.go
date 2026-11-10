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

	"github.com/google/go-cmp/cmp"

	"xmanager_cloud/pkg/metricsquery/metricsquerytest"
)

func TestBackendQueryPassesThrough(t *testing.T) {
	ctx := context.Background()
	want := &metricsquerytest.Rows{Data: [][]any{{int64(1)}}}
	backends := map[string]backend{
		"clickhouse": clickHouseBackend{conn: &fakeClickHouseConn{queryRecorder: queryRecorder{rows: want}}},
		"bigquery":   bigQueryBackend{conn: &fakeBigQueryConn{queryRecorder: queryRecorder{rows: want}}},
	}
	for name, b := range backends {
		t.Run(name, func(t *testing.T) {
			got, err := b.Query(ctx, "SELECT ?", int64(1))
			if err != nil {
				t.Fatalf("Query() error: %v", err)
			}
			if got != want {
				t.Errorf("Query() = %v, want the connection's rows %v", got, want)
			}
		})
	}
}

func TestBackendPing(t *testing.T) {
	offline := errors.New("offline")
	tests := []struct {
		name    string
		backend backend
		wantErr error
	}{
		{name: "clickhouse reachable", backend: clickHouseBackend{conn: &fakeClickHouseConn{}}},
		{name: "clickhouse unreachable", backend: clickHouseBackend{conn: &fakeClickHouseConn{pingErr: offline}}, wantErr: offline},
		{name: "bigquery reachable", backend: bigQueryBackend{conn: &fakeBigQueryConn{}}},
		{name: "bigquery unreachable", backend: bigQueryBackend{conn: &fakeBigQueryConn{pingErr: offline}}, wantErr: offline},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.backend.Ping(context.Background())
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Ping() = %v, want wrapped %v", err, tt.wantErr)
			}
		})
	}
}

func TestBackendClose(t *testing.T) {
	if err := (clickHouseBackend{conn: &fakeClickHouseConn{}}).Close(); err != nil {
		t.Errorf("Close() on non-closable clickhouse conn = %v, want nil", err)
	}
	closable := &closableClickHouseConn{}
	if err := (clickHouseBackend{conn: closable}).Close(); err != nil || !closable.closed {
		t.Errorf("Close() on closable clickhouse conn = %v, closed = %t; want nil, true", err, closable.closed)
	}
	bq := &fakeBigQueryConn{}
	if err := (bigQueryBackend{conn: bq}).Close(); err != nil || !bq.closed {
		t.Errorf("Close() on bigquery conn = %v, closed = %t; want nil, true", err, bq.closed)
	}
}

func TestBackendDialect(t *testing.T) {
	got := []dialect{clickHouseBackend{}.dialect(), bigQueryBackend{}.dialect()}
	want := []dialect{dialectClickHouse, dialectBigQuery}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("dialect() mismatch (-want +got):\n%s", diff)
	}
}
