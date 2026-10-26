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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"google.golang.org/api/option"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/dashboard/dataengine"
	xmcbigquery "xmanager_cloud/pkg/bigquery"
	"xmanager_cloud/pkg/clickhouse"
)

const (
	// StoreClickHouse executes the harness against ClickHouse.
	StoreClickHouse = "clickhouse"
	// StoreBigQuery executes the harness against Google Cloud BigQuery.
	StoreBigQuery = "bigquery"
)

// HarnessOptions controls the execution of the DataEngine test harness.
type HarnessOptions struct {
	Store                 string
	EnvFile               string
	SeedData              bool
	CleanData             bool
	UpdateGolden          bool
	TestCaseFilter        string
	TableName             string
	SeedFile              string
	TestCasesFile         string
	GoldenDir             string
	BigQueryClientOptions []option.ClientOption
}

// DefaultHarnessOptions returns standard default options.
func DefaultHarnessOptions() *HarnessOptions {
	return &HarnessOptions{
		Store:         StoreClickHouse,
		EnvFile:       ".env",
		SeedData:      true,
		CleanData:     false,
		UpdateGolden:  false,
		TableName:     "xmc_metrics",
		SeedFile:      "testdata/metrics_seed.json",
		TestCasesFile: "testdata/test_cases.json",
		GoldenDir:     "testdata/golden",
	}
}

// TestResult records the outcome of an individual test case.
type TestResult struct {
	TestCaseID  string
	Description string
	Duration    time.Duration
	Passed      bool
	Error       error
	Diff        string
}

// Run executes the complete DataEngine test workflow.
func Run(ctx context.Context, opts *HarnessOptions) ([]*TestResult, error) {
	if opts == nil {
		opts = DefaultHarnessOptions()
	}

	_ = LoadEnvFile(opts.EnvFile)
	store, err := resolveHarnessStore(opts.Store)
	if err != nil {
		return nil, err
	}
	if store == StoreBigQuery {
		return runBigQuery(ctx, opts)
	}

	conn, err := clickhouse.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ClickHouse: %w", err)
	}
	defer conn.Close()

	if err := EnsureSchema(ctx, conn, opts.TableName); err != nil {
		return nil, fmt.Errorf("failed to ensure schema: %w", err)
	}

	var seedXIDs []int64
	if opts.SeedData || opts.CleanData {
		var err error
		seedXIDs, err = ExtractSeedXIDs(opts.SeedFile)
		if err != nil {
			return nil, fmt.Errorf("failed to extract seed XIDs: %w", err)
		}
	}

	if err := prepareSeedData(ctx, conn, opts, seedXIDs); err != nil {
		return nil, err
	}

	engine, err := dataengine.New(conn, dataengine.WithTableName(opts.TableName))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize DataEngine: %w", err)
	}

	testCases, err := LoadTestCases(opts.TestCasesFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load test cases: %w", err)
	}

	results := runTestCases(ctx, engine, testCases, opts)

	if opts.CleanData {
		if err := CleanupTestMetrics(ctx, conn, opts.TableName, seedXIDs...); err != nil {
			fmt.Printf("WARNING: Failed to cleanup test metrics: %v\n", err)
		}
	}

	return results, nil
}

func prepareSeedData(ctx context.Context, conn driver.Conn, opts *HarnessOptions, seedXIDs []int64) error {
	if !opts.SeedData {
		return nil
	}
	_ = CleanupTestMetrics(ctx, conn, opts.TableName, seedXIDs...)
	count, err := SeedDataset(ctx, conn, opts.TableName, opts.SeedFile)
	if err != nil {
		return fmt.Errorf("failed to seed dataset: %w", err)
	}
	fmt.Printf("--> Seeded %d records into %s\n", count, opts.TableName)
	return nil
}

func resolveHarnessStore(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = os.Getenv("METRICS_STORE")
	}
	switch s := strings.ToLower(strings.TrimSpace(raw)); s {
	case "", StoreClickHouse:
		return StoreClickHouse, nil
	case StoreBigQuery:
		return StoreBigQuery, nil
	default:
		return "", fmt.Errorf("unsupported store %q: must be %q or %q", raw, StoreClickHouse, StoreBigQuery)
	}
}

func runBigQuery(ctx context.Context, opts *HarnessOptions) ([]*TestResult, error) {
	bqOpts, ephemeral, err := resolveBigQueryOptions(opts)
	if err != nil {
		return nil, err
	}
	if err := ensureBigQueryTable(ctx, bqOpts, ephemeral, opts.BigQueryClientOptions...); err != nil {
		return nil, fmt.Errorf("failed to ensure BigQuery schema: %w", err)
	}
	if ephemeral && opts.CleanData {
		defer dropEphemeralBigQueryTable(bqOpts, opts.BigQueryClientOptions)
	}
	conn, err := xmcbigquery.ConnectWithOptions(ctx, bqOpts, opts.BigQueryClientOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to BigQuery: %w", err)
	}
	defer conn.Close()

	cleanup, err := prepareBigQuerySeedData(ctx, conn, ephemeral, opts)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	return executeBigQueryCases(ctx, conn, opts)
}

func resolveBigQueryOptions(opts *HarnessOptions) (*xmcbigquery.Options, bool, error) {
	bqOpts, err := xmcbigquery.GetOptions()
	if err != nil {
		return nil, false, fmt.Errorf("failed to load BigQuery options: %w", err)
	}
	if opts.TableName != "" && opts.TableName != xmcbigquery.DefaultTable {
		bqOpts.Table = opts.TableName
		return bqOpts, false, nil
	}
	if bqOpts.Table != xmcbigquery.DefaultTable {
		return bqOpts, false, nil
	}
	if opts.SeedData {
		bqOpts.Table = newEphemeralBigQueryTableName()
		return bqOpts, true, nil
	}
	if opts.CleanData {
		return nil, false, fmt.Errorf("refusing to clean default BigQuery table %q without -seed or explicit -table_name", bqOpts.Table)
	}
	return bqOpts, false, nil
}

func newEphemeralBigQueryTableName() string {
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	return fmt.Sprintf("xmc_metrics_e2e_%d_%s", time.Now().UTC().UnixMilli(), hex.EncodeToString(suffix[:]))
}

func prepareBigQuerySeedData(ctx context.Context, conn xmcbigquery.Conn, ephemeral bool, opts *HarnessOptions) (func(), error) {
	var seedXIDs []int64
	if (opts.SeedData || opts.CleanData) && !ephemeral {
		var err error
		seedXIDs, err = ExtractSeedXIDs(opts.SeedFile)
		if err != nil {
			return nil, fmt.Errorf("failed to extract seed XIDs: %w", err)
		}
	}
	cleanup := buildBigQueryRowCleanup(conn, ephemeral, opts.CleanData, seedXIDs)
	if !opts.SeedData {
		return cleanup, nil
	}
	if !ephemeral {
		if err := CleanupBigQueryTestMetrics(ctx, conn, seedXIDs...); err != nil {
			return cleanup, fmt.Errorf("failed to purge pre-existing BigQuery seed rows: %w", err)
		}
	}
	count, err := SeedBigQueryDataset(ctx, conn, opts.SeedFile)
	if err != nil {
		return cleanup, fmt.Errorf("failed to seed BigQuery dataset: %w", err)
	}
	fmt.Printf("--> Seeded %d records into %s\n", count, conn.TableRef())
	return cleanup, nil
}

func dropEphemeralBigQueryTable(bqOpts *xmcbigquery.Options, clientOpts []option.ClientOption) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := DropBigQueryTable(cleanupCtx, bqOpts, clientOpts...); err != nil {
		fmt.Printf("WARNING: Failed to drop ephemeral BigQuery test table: %v\n", err)
	}
}

func buildBigQueryRowCleanup(conn xmcbigquery.Conn, ephemeral, cleanData bool, seedXIDs []int64) func() {
	if !cleanData || ephemeral {
		return nil
	}
	return func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := CleanupBigQueryTestMetrics(cleanupCtx, conn, seedXIDs...); err != nil {
			fmt.Printf("WARNING: Failed to cleanup BigQuery test metrics: %v\n", err)
		}
	}
}

func executeBigQueryCases(ctx context.Context, conn xmcbigquery.Conn, opts *HarnessOptions) ([]*TestResult, error) {
	engine, err := dataengine.NewBigQuery(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize BigQuery DataEngine: %w", err)
	}
	testCases, err := LoadTestCases(opts.TestCasesFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load test cases: %w", err)
	}
	return runTestCases(ctx, engine, testCases, opts), nil
}

func runTestCases(ctx context.Context, engine *dataengine.DataEngine, testCases []*TestCase, opts *HarnessOptions) []*TestResult {
	var results []*TestResult
	for _, tc := range testCases {
		if opts.TestCaseFilter != "" && tc.ID != opts.TestCaseFilter {
			continue
		}
		results = append(results, executeTestCase(ctx, engine, tc, opts))
	}
	return results
}

func executeTestCase(ctx context.Context, engine *dataengine.DataEngine, tc *TestCase, opts *HarnessOptions) *TestResult {
	start := time.Now()
	actualData, err := engine.FetchChartData(ctx, tc.Chart, tc.Request)
	duration := time.Since(start)

	if err != nil {
		return &TestResult{
			TestCaseID:  tc.ID,
			Description: tc.Description,
			Duration:    duration,
			Passed:      false,
			Error:       fmt.Errorf("FetchChartData failed: %w", err),
		}
	}

	goldenRelPath := filepath.Join(opts.GoldenDir, tc.GoldenFile)
	if opts.UpdateGolden {
		return handleGoldenUpdate(tc, goldenRelPath, opts.GoldenDir, actualData, duration)
	}
	return handleGoldenCompare(tc, goldenRelPath, actualData, duration)
}

func handleGoldenUpdate(tc *TestCase, goldenRelPath, goldenDir string, actualData *dpb.ChartData, duration time.Duration) *TestResult {
	resolvedDir, err := FindFile(goldenDir)
	targetFile := goldenRelPath
	if err == nil {
		targetFile = filepath.Join(resolvedDir, tc.GoldenFile)
	}

	if err := SaveGolden(targetFile, actualData); err != nil {
		return &TestResult{
			TestCaseID:  tc.ID,
			Description: tc.Description,
			Duration:    duration,
			Passed:      false,
			Error:       fmt.Errorf("failed to save golden file: %w", err),
		}
	}

	return &TestResult{
		TestCaseID:  tc.ID,
		Description: tc.Description,
		Duration:    duration,
		Passed:      true,
	}
}

func handleGoldenCompare(tc *TestCase, goldenRelPath string, actualData *dpb.ChartData, duration time.Duration) *TestResult {
	diff, err := CompareChartData(actualData, goldenRelPath)
	if err != nil {
		return &TestResult{
			TestCaseID:  tc.ID,
			Description: tc.Description,
			Duration:    duration,
			Passed:      false,
			Error:       err,
		}
	}

	if diff != "" {
		return &TestResult{
			TestCaseID:  tc.ID,
			Description: tc.Description,
			Duration:    duration,
			Passed:      false,
			Diff:        diff,
		}
	}

	return &TestResult{
		TestCaseID:  tc.ID,
		Description: tc.Description,
		Duration:    duration,
		Passed:      true,
	}
}
