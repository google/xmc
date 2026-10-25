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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	bqapi "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/option"
	xmcbigquery "xmanager_cloud/pkg/bigquery"
)

type fakeSeederAPI struct {
	mu           sync.Mutex
	tableStatus  int
	createStatus int
	queryStatus  int
	createdTable *bqapi.Table
	queries      []*bqapi.QueryRequest
}

func (f *fakeSeederAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/tables/"):
		if f.tableStatus != 0 && f.tableStatus != http.StatusOK {
			reason := "notFound"
			if f.tableStatus != http.StatusNotFound {
				reason = "accessDenied"
			}
			http.Error(w, `{"error":{"code":`+jsonInt(f.tableStatus)+`,"message":"table err","errors":[{"reason":"`+reason+`"}]}}`, f.tableStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(&bqapi.Table{
			TableReference: &bqapi.TableReference{ProjectId: "test-proj", DatasetId: "xmc_metrics", TableId: "xmc_metrics"},
		})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/tables"):
		var tbl bqapi.Table
		_ = json.NewDecoder(r.Body).Decode(&tbl)
		f.createdTable = &tbl
		if f.createStatus != 0 && f.createStatus != http.StatusOK {
			http.Error(w, `{"error":{"code":400,"message":"create failed","errors":[{"reason":"invalid"}]}}`, f.createStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(&tbl)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/queries"):
		var req bqapi.QueryRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.queries = append(f.queries, &req)
		if f.queryStatus != 0 && f.queryStatus != http.StatusOK {
			http.Error(w, `{"error":{"code":400,"message":"query failed","errors":[{"reason":"invalid"}]}}`, f.queryStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(&bqapi.QueryResponse{
			JobComplete:  true,
			JobReference: &bqapi.JobReference{ProjectId: "test-proj", JobId: "job-1"},
			Schema:       &bqapi.TableSchema{},
		})
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/queries/"):
		_ = json.NewEncoder(w).Encode(&bqapi.GetQueryResultsResponse{JobComplete: true})
	default:
		http.NotFound(w, r)
	}
}

func jsonInt(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func startFakeSeederServer(t *testing.T, api *fakeSeederAPI) []option.ClientOption {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(api.serveHTTP))
	t.Cleanup(srv.Close)
	return []option.ClientOption{option.WithEndpoint(srv.URL), option.WithoutAuthentication()}
}

func TestEnsureBigQuerySchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	opts := &xmcbigquery.Options{ProjectID: "test-proj", Dataset: "xmc_metrics", Table: "xmc_metrics"}

	if err := EnsureBigQuerySchema(ctx, nil); err == nil {
		t.Error("EnsureBigQuerySchema(nil) succeeded, want validation error")
	}
	if err := EnsureBigQuerySchema(ctx, opts, option.WithCredentialsFile("/nonexistent/creds.json")); err == nil {
		t.Error("EnsureBigQuerySchema(missing creds) succeeded, want error")
	}

	existingAPI := &fakeSeederAPI{tableStatus: http.StatusOK}
	if err := EnsureBigQuerySchema(ctx, opts, startFakeSeederServer(t, existingAPI)...); err != nil {
		t.Fatalf("EnsureBigQuerySchema(existing) error: %v", err)
	}
	if existingAPI.createdTable != nil {
		t.Error("EnsureBigQuerySchema(existing) called Table.Create unexpectedly")
	}

	deniedAPI := &fakeSeederAPI{tableStatus: http.StatusForbidden}
	if err := EnsureBigQuerySchema(ctx, opts, startFakeSeederServer(t, deniedAPI)...); err == nil {
		t.Error("EnsureBigQuerySchema(403) succeeded, want error")
	}

	createFailAPI := &fakeSeederAPI{tableStatus: http.StatusNotFound, createStatus: http.StatusBadRequest}
	if err := EnsureBigQuerySchema(ctx, opts, startFakeSeederServer(t, createFailAPI)...); err == nil {
		t.Error("EnsureBigQuerySchema(create 400) succeeded, want error")
	}

	createOKAPI := &fakeSeederAPI{tableStatus: http.StatusNotFound}
	if err := EnsureBigQuerySchema(ctx, opts, startFakeSeederServer(t, createOKAPI)...); err != nil {
		t.Fatalf("EnsureBigQuerySchema(404 -> create) error: %v", err)
	}
	if createOKAPI.createdTable == nil {
		t.Fatal("expected Table.Create request body")
	}
	if got := createOKAPI.createdTable.TimePartitioning.Field; got != "timestamp" {
		t.Errorf("TimePartitioning.Field = %q, want timestamp", got)
	}
	if diff := cmp.Diff([]string{"xid", "metric_name", "work_unit_id"}, createOKAPI.createdTable.Clustering.Fields); diff != "" {
		t.Errorf("Clustering.Fields mismatch (-want +got):\n%s", diff)
	}
	if got := len(createOKAPI.createdTable.Schema.Fields); got != 10 {
		t.Errorf("Schema.Fields count = %d, want 10", got)
	}
}

func TestSeedAndCleanupBigQueryDataset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	opts := &xmcbigquery.Options{ProjectID: "test-proj", Dataset: "xmc_metrics", Table: "xmc_metrics"}
	api := &fakeSeederAPI{}
	conn, err := xmcbigquery.ConnectWithOptions(ctx, opts, startFakeSeederServer(t, api)...)
	if err != nil {
		t.Fatalf("ConnectWithOptions() error: %v", err)
	}
	defer conn.Close()

	tmpDir := t.TempDir()
	emptyPath := filepath.Join(tmpDir, "empty.json")
	_ = os.WriteFile(emptyPath, []byte(`[]`), 0600)
	if n, err := SeedBigQueryDataset(ctx, conn, emptyPath); err != nil || n != 0 {
		t.Errorf("SeedBigQueryDataset(empty) = (%d, %v), want (0, nil)", n, err)
	}

	badPath := filepath.Join(tmpDir, "bad.json")
	_ = os.WriteFile(badPath, []byte(`{invalid`), 0600)
	if _, err := SeedBigQueryDataset(ctx, conn, badPath); err == nil {
		t.Error("SeedBigQueryDataset(bad JSON) succeeded, want error")
	}
	if _, err := SeedBigQueryDataset(ctx, conn, filepath.Join(tmpDir, "missing.json")); err == nil {
		t.Error("SeedBigQueryDataset(missing) succeeded, want error")
	}

	seedPath := filepath.Join(tmpDir, "seed.json")
	seedJSON := `[
		{"xid":9001,"work_unit_id":1,"execution_id":1,"job_name":"worker","replica_id":"","metric_name":"loss","metric_value":1.5,"step":10,"timestamp":"2026-08-11T10:00:00.123456Z"},
		{"xid":9001,"work_unit_id":1,"execution_id":1,"job_name":"worker","replica_id":"1","metric_name":"loss","metric_value":1.2,"step":20,"timestamp":"2026-08-11T10:01:00Z"},
		{"xid":9002,"work_unit_id":2,"execution_id":1,"job_name":"eval","replica_id":"0","metric_name":"acc","metric_value":0.9,"step":20,"timestamp":"not-a-timestamp"}
	]`
	_ = os.WriteFile(seedPath, []byte(seedJSON), 0600)
	count, err := SeedBigQueryDataset(ctx, conn, seedPath)
	if err != nil || count != 3 {
		t.Fatalf("SeedBigQueryDataset() = (%d, %v), want (3, nil)", count, err)
	}

	if err := CleanupBigQueryTestMetrics(ctx, conn); err != nil {
		t.Errorf("CleanupBigQueryTestMetrics(empty) error: %v", err)
	}
	if err := CleanupBigQueryTestMetrics(ctx, conn, 9001, 9002); err != nil {
		t.Fatalf("CleanupBigQueryTestMetrics() error: %v", err)
	}

	api.mu.Lock()
	var structFields []string
	for _, f := range api.queries[0].QueryParameters[0].ParameterType.ArrayType.StructTypes {
		structFields = append(structFields, f.Name)
	}
	api.queryStatus = http.StatusBadRequest
	gotQueries := len(api.queries)
	api.mu.Unlock()

	wantFields := []string{"xid", "work_unit_id", "execution_id", "job_name", "replica_id", "metric_name", "metric_value", "step", "timestamp"}
	if diff := cmp.Diff(wantFields, structFields); diff != "" {
		t.Errorf("QueryParameter struct field names mismatch (-want +got):\n%s", diff)
	}
	if gotQueries != 2 {
		t.Errorf("recorded %d DML queries, want 2 (1 INSERT + 1 DELETE)", gotQueries)
	}
	if _, err := SeedBigQueryDataset(ctx, conn, seedPath); err == nil {
		t.Error("SeedBigQueryDataset(HTTP 400) succeeded, want error")
	}
	if err := CleanupBigQueryTestMetrics(ctx, conn, 9001); err == nil {
		t.Error("CleanupBigQueryTestMetrics(HTTP 400) succeeded, want error")
	}
}

func TestRunBigQueryHarnessWorkflow(t *testing.T) {
	ctx := context.Background()
	t.Setenv("METRICS_STORE", "bigquery")
	if got, err := resolveHarnessStore(""); err != nil || got != StoreBigQuery {
		t.Fatalf("resolveHarnessStore(\"\") with METRICS_STORE=bigquery = (%q, %v)", got, err)
	}
	if _, err := Run(ctx, &HarnessOptions{Store: "invalid", EnvFile: "missing.env"}); err == nil {
		t.Error("Run(invalid store) succeeded, want error")
	}

	t.Setenv("BIGQUERY_PROJECT_ID", "test-proj")
	t.Setenv("BIGQUERY_DATASET", "xmc_metrics")
	t.Setenv("BIGQUERY_TABLE", "xmc_metrics")

	tmpDir := t.TempDir()
	seedPath := filepath.Join(tmpDir, "seed.json")
	_ = os.WriteFile(seedPath, []byte(`[{"xid":1001,"work_unit_id":1,"execution_id":1,"job_name":"w","metric_name":"loss","metric_value":0.5,"step":1,"timestamp":"2026-08-11T10:00:00Z"}]`), 0600)
	casesPath := filepath.Join(tmpDir, "cases.json")
	_ = os.WriteFile(casesPath, []byte(`[{"id":"tc_empty","description":"empty","golden_file":"empty.json","request":{"name":"experiments/1001/charts/c1"},"chart":{"name":"experiments/1001/charts/c1","xid":1001,"wid":1,"plots":[{"plot_id":"p1","type":2,"data":{"metrics":["loss"]}}]}}]`), 0600)
	goldenDir := filepath.Join(tmpDir, "golden")
	_ = os.MkdirAll(goldenDir, 0700)
	_ = os.WriteFile(filepath.Join(goldenDir, "empty.json"), []byte(`{"name":"experiments/1001/charts/c1","xid":"1001","wid":"1"}`), 0600)

	api := &fakeSeederAPI{}
	results, err := Run(ctx, &HarnessOptions{
		Store:                 StoreBigQuery,
		EnvFile:               filepath.Join(tmpDir, "missing.env"),
		SeedData:              true,
		CleanData:             true,
		TableName:             "custom_metrics",
		SeedFile:              seedPath,
		TestCasesFile:         casesPath,
		GoldenDir:             goldenDir,
		BigQueryClientOptions: startFakeSeederServer(t, api),
	})
	if err != nil {
		t.Fatalf("Run(StoreBigQuery) error: %v", err)
	}
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("Run(StoreBigQuery) result = %+v, want 1 passing test case", results[0])
	}
}
