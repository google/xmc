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

package dataengine_test

import (
	"context"
	"testing"
	"time"

	xmcbigquery "xmanager_cloud/pkg/bigquery"
	"xmanager_cloud/pkg/clickhouse"
	dataenginetest "xmanager_cloud/test/dataengine"
)

func TestDataEngineLive(t *testing.T) {
	_ = dataenginetest.LoadEnvFile(".env")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := clickhouse.Connect(ctx)
	if err != nil {
		t.Skipf("Skipping live ClickHouse test (ClickHouse not reachable): %v", err)
		return
	}
	defer conn.Close()

	opts := dataenginetest.DefaultHarnessOptions()
	opts.CleanData = true

	results, err := dataenginetest.Run(ctx, opts)
	if err != nil {
		t.Fatalf("DataEngine test harness execution failed: %v", err)
	}

	for _, res := range results {
		t.Run(res.TestCaseID, func(t *testing.T) {
			if !res.Passed {
				if res.Error != nil {
					t.Fatalf("Test case failed with error: %v", res.Error)
				}
				if res.Diff != "" {
					t.Fatalf("Test case failed golden diff:\n%s", res.Diff)
				}
				t.Fatalf("Test case failed")
			}
		})
	}
}

func TestDataEngineBigQueryLive(t *testing.T) {
	_ = dataenginetest.LoadEnvFile(".env")
	if !xmcbigquery.Configured() {
		t.Skip("Skipping live BigQuery test (BIGQUERY_DATASET is not set)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	bqOpts, err := xmcbigquery.GetOptions()
	if err != nil {
		t.Skipf("Skipping live BigQuery test (options incomplete): %v", err)
	}
	conn, err := xmcbigquery.ConnectWithOptions(ctx, bqOpts)
	if err != nil {
		t.Skipf("Skipping live BigQuery test (BigQuery not reachable): %v", err)
	}
	_ = conn.Close()

	opts := dataenginetest.DefaultHarnessOptions()
	opts.Store = dataenginetest.StoreBigQuery
	opts.CleanData = true
	results, err := dataenginetest.Run(ctx, opts)
	if err != nil {
		t.Fatalf("BigQuery DataEngine test harness execution failed: %v", err)
	}
	for _, res := range results {
		t.Run(res.TestCaseID, func(t *testing.T) {
			if !res.Passed {
				t.Fatalf("Test case failed (err=%v):\n%s", res.Error, res.Diff)
			}
		})
	}
}
