// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bigquery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/google/go-cmp/cmp"
	bqapi "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/option"
)

func TestConfigured(t *testing.T) {
	for _, tt := range []struct {
		name, dataset string
		want          bool
	}{
		{"unset dataset", "", false},
		{"whitespace dataset", "   ", false},
		{"non-empty dataset", "xmc_metrics", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BIGQUERY_DATASET", tt.dataset)
			if got := Configured(); got != tt.want {
				t.Errorf("Configured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetOptions(t *testing.T) {
	for _, tt := range []struct {
		name     string
		env      map[string]string
		fallback []string
		want     Options
		wantErr  bool
	}{
		{"GOOGLE_CLOUD_PROJECT with defaults", map[string]string{"GOOGLE_CLOUD_PROJECT": "gcp-proj"}, nil, Options{ProjectID: "gcp-proj", Dataset: DefaultDataset, Table: DefaultTable}, false},
		{"BIGQUERY_PROJECT_ID overrides all", map[string]string{"BIGQUERY_PROJECT_ID": "bq-proj", "GOOGLE_CLOUD_PROJECT": "gcp-proj", "BIGQUERY_DATASET": "custom_dataset", "BIGQUERY_TABLE": "custom_table"}, []string{"fallback-proj"}, Options{ProjectID: "bq-proj", Dataset: "custom_dataset", Table: "custom_table"}, false},
		{"fallbackProjectID when env unset", nil, []string{"", "fallback-proj"}, Options{ProjectID: "fallback-proj", Dataset: DefaultDataset, Table: DefaultTable}, false},
		{"error when no project ID", nil, nil, Options{}, true},
		{"error on invalid dataset identifier", map[string]string{"GOOGLE_CLOUD_PROJECT": "gcp-proj", "BIGQUERY_DATASET": "bad`dataset"}, nil, Options{}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"BIGQUERY_PROJECT_ID", "GOOGLE_CLOUD_PROJECT", "BIGQUERY_DATASET", "BIGQUERY_TABLE"} {
				t.Setenv(key, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got, err := GetOptions(tt.fallback...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetOptions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if diff := cmp.Diff(tt.want, *got); diff != "" {
					t.Errorf("GetOptions() mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestOptionsValidateAndTableRef(t *testing.T) {
	t.Parallel()
	if _, err := (*Options)(nil).TableRef(); err == nil {
		t.Fatal("nil Options.TableRef() succeeded, want error")
	}

	valid := &Options{ProjectID: "domain.com:my-proj_1", Dataset: "xmc_metrics_2026", Table: "xmc_metrics"}
	gotRef, err := valid.TableRef()
	if err != nil {
		t.Fatalf("TableRef() unexpected error: %v", err)
	}
	if wantRef := "`domain.com:my-proj_1.xmc_metrics_2026.xmc_metrics`"; gotRef != wantRef {
		t.Errorf("TableRef() = %q, want %q", gotRef, wantRef)
	}

	oversized := strings.Repeat("a", maxIdentifierLen+1)
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"empty project", Options{Dataset: "ds", Table: "tbl"}},
		{"project with slash", Options{ProjectID: "proj/evil", Dataset: "ds", Table: "tbl"}},
		{"project with backtick", Options{ProjectID: "proj`evil", Dataset: "ds", Table: "tbl"}},
		{"oversized project", Options{ProjectID: oversized, Dataset: "ds", Table: "tbl"}},
		{"dataset with hyphen", Options{ProjectID: "proj", Dataset: "bad-ds", Table: "tbl"}},
		{"dataset with SQL injection", Options{ProjectID: "proj", Dataset: "ds; DROP TABLE", Table: "tbl"}},
		{"oversized dataset", Options{ProjectID: "proj", Dataset: oversized, Table: "tbl"}},
		{"table with dot", Options{ProjectID: "proj", Dataset: "ds", Table: "ds.tbl"}},
		{"oversized table", Options{ProjectID: "proj", Dataset: "ds", Table: oversized}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.opts.TableRef(); err == nil {
				t.Errorf("TableRef(%+v) succeeded, want validation error", tc.opts)
			}
		})
	}
}

type fakeBigQueryAPI struct {
	mu          sync.Mutex
	tableStatus int
	queryStatus int
	badNextPage bool
	queryResp   *bqapi.QueryResponse
	lastQuery   *bqapi.QueryRequest
}

func (f *fakeBigQueryAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/tables/"):
		if f.tableStatus != 0 && f.tableStatus != http.StatusOK {
			http.Error(w, `{"error":{"code":404,"message":"table not found","errors":[{"reason":"notFound"}]}}`, f.tableStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(&bqapi.Table{
			TableReference: &bqapi.TableReference{ProjectId: "test-proj", DatasetId: "xmc_metrics", TableId: "xmc_metrics"},
		})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/queries"):
		var req bqapi.QueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			f.lastQuery = &req
		}
		if f.queryStatus != 0 && f.queryStatus != http.StatusOK {
			http.Error(w, `{"error":{"code":400,"message":"bad query","errors":[{"reason":"invalid"}]}}`, f.queryStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(f.queryResp)
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/queries/"):
		if f.badNextPage {
			_ = json.NewEncoder(w).Encode(&bqapi.GetQueryResultsResponse{
				JobComplete: true,
				Rows:        []*bqapi.TableRow{{F: []*bqapi.TableCell{{V: "invalid-int"}}}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(&bqapi.GetQueryResultsResponse{JobComplete: true})
	default:
		http.NotFound(w, r)
	}
}

func newFakeServer(t *testing.T, api *fakeBigQueryAPI) []option.ClientOption {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(api.serveHTTP))
	t.Cleanup(srv.Close)
	return []option.ClientOption{option.WithEndpoint(srv.URL), option.WithoutAuthentication()}
}

func TestConnectAndQueryLifecycle(t *testing.T) {
	sampleTS := time.Date(2026, 8, 11, 10, 0, 0, 123456000, time.UTC)
	api := &fakeBigQueryAPI{
		queryResp: &bqapi.QueryResponse{
			JobComplete:  true,
			JobReference: &bqapi.JobReference{ProjectId: "test-proj", JobId: "job-1"},
			Schema: &bqapi.TableSchema{Fields: []*bqapi.TableFieldSchema{
				{Name: "step", Type: "INTEGER"},
				{Name: "avg_val", Type: "FLOAT"},
				{Name: "metric_name", Type: "STRING"},
				{Name: "max_ts", Type: "TIMESTAMP"},
				{Name: "active", Type: "BOOLEAN"},
			}},
			TotalRows: 1,
			Rows: []*bqapi.TableRow{{F: []*bqapi.TableCell{
				{V: "100"}, {V: "0.875"}, {V: "train/loss"}, {V: strconv.FormatInt(sampleTS.UnixMicro(), 10)}, {V: "true"},
			}}},
		},
	}
	t.Setenv("BIGQUERY_PROJECT_ID", "test-proj")
	t.Setenv("BIGQUERY_DATASET", "xmc_metrics")
	t.Setenv("BIGQUERY_TABLE", "xmc_metrics")

	ctx := context.Background()
	opts, err := GetOptions()
	if err != nil {
		t.Fatalf("GetOptions() unexpected error: %v", err)
	}
	conn, err := ConnectWithOptions(ctx, opts, newFakeServer(t, api)...)
	if err != nil {
		t.Fatalf("ConnectWithOptions() unexpected error: %v", err)
	}
	defer func() {
		for range 2 {
			if err := conn.Close(); err != nil {
				t.Errorf("Close() error: %v", err)
			}
		}
	}()
	if got := conn.TableRef(); got != "`test-proj.xmc_metrics.xmc_metrics`" {
		t.Errorf("TableRef() = %q, want `test-proj.xmc_metrics.xmc_metrics`", got)
	}

	rows, err := conn.Query(ctx, "SELECT * FROM `test-proj.xmc_metrics.xmc_metrics` WHERE xid = ? AND metric_name = ? AND timestamp >= ?", int64(42), "train/loss", sampleTS)
	if err != nil {
		t.Fatalf("Query() unexpected error: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("rows.Next() = false, want first row (err = %v)", rows.Err())
	}

	var step int64
	var avgVal float64
	var metricName string
	var maxTS time.Time
	var active bool
	if err := rows.Scan(&step, &avgVal, &metricName, &maxTS, &active); err != nil {
		t.Fatalf("rows.Scan() unexpected error: %v", err)
	}
	if step != 100 || avgVal != 0.875 || metricName != "train/loss" || !maxTS.Equal(sampleTS) || !active {
		t.Errorf("scanned (%d, %f, %q, %v, %v), want (100, 0.875, \"train/loss\", %v, true)", step, avgVal, metricName, maxTS, active, sampleTS)
	}
	if rows.Next() || rows.Err() != nil {
		t.Errorf("exhausted iterator state Next()=true or Err()=%v", rows.Err())
	}

	api.mu.Lock()
	gotParams := len(api.lastQuery.QueryParameters)
	api.mu.Unlock()
	if gotParams != 3 {
		t.Errorf("sent %d positional query parameters, want 3", gotParams)
	}
}

func TestConnectAndQueryErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := ConnectWithOptions(ctx, nil); err == nil {
		t.Error("ConnectWithOptions(nil) succeeded, want error")
	}

	opts := &Options{ProjectID: "test-proj", Dataset: "xmc_metrics", Table: "xmc_metrics"}
	if _, err := ConnectWithOptions(ctx, opts, option.WithCredentialsFile("/nonexistent/credentials.json")); err == nil {
		t.Error("ConnectWithOptions(missing credentials file) succeeded, want client creation error")
	}
	if _, err := ConnectWithOptions(ctx, opts, newFakeServer(t, &fakeBigQueryAPI{tableStatus: http.StatusNotFound})...); err == nil {
		t.Error("ConnectWithOptions(missing table) succeeded, want ping error")
	}

	conn, err := ConnectWithOptions(ctx, opts, newFakeServer(t, &fakeBigQueryAPI{queryStatus: http.StatusBadRequest})...)
	if err != nil {
		t.Fatalf("ConnectWithOptions() unexpected error: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Query(ctx, "SELECT 1"); err == nil {
		t.Error("Query() with HTTP 400 succeeded, want error")
	}
}

func TestRowIteratorPaginationErrorAndScanErrors(t *testing.T) {
	api := &fakeBigQueryAPI{
		badNextPage: true,
		queryResp: &bqapi.QueryResponse{
			JobComplete:  true,
			JobReference: &bqapi.JobReference{ProjectId: "test-proj", JobId: "job-paged"},
			Schema:       &bqapi.TableSchema{Fields: []*bqapi.TableFieldSchema{{Name: "step", Type: "INTEGER"}}},
			TotalRows:    2,
			PageToken:    "next-page",
			Rows:         []*bqapi.TableRow{{F: []*bqapi.TableCell{{V: "1"}}}},
		},
	}
	opts := &Options{ProjectID: "test-proj", Dataset: "xmc_metrics", Table: "xmc_metrics"}
	conn, err := ConnectWithOptions(context.Background(), opts, newFakeServer(t, api)...)
	if err != nil {
		t.Fatalf("ConnectWithOptions() error: %v", err)
	}
	defer conn.Close()

	rows, err := conn.Query(context.Background(), "SELECT step FROM tbl")
	if err != nil {
		t.Fatalf("Query() error: %v", err)
	}

	var step int64
	if err := rows.Scan(&step); err == nil {
		t.Error("Scan() before Next() succeeded, want error")
	}
	if !rows.Next() {
		t.Fatal("first rows.Next() = false, want true")
	}
	for _, badArgs := range [][]any{{&step, &step}, {nil}, {(*int64)(nil)}, {new(int)}, {new(string)}} {
		if err := rows.Scan(badArgs...); err == nil {
			t.Errorf("Scan(%v) succeeded, want error", badArgs)
		}
	}
	for _, dest := range []any{new(float64), new(time.Time), new(bool), (*bigquery.Value)(nil)} {
		if err := assignColumn(0, dest, "wrong-type"); err == nil {
			t.Errorf("assignColumn(%T) succeeded, want error", dest)
		}
	}

	if rows.Next() || rows.Err() == nil || rows.Next() {
		t.Errorf("expected failing page fetch to stop iteration and populate Err() = %v", rows.Err())
	}
	_ = rows.Close()
	if rows.Next() || rows.Scan(&step) == nil {
		t.Error("expected Next()=false and Scan() error after Close()")
	}
}
