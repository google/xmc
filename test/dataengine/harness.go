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
	"path/filepath"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/dashboard/dataengine"
	"xmanager_cloud/pkg/clickhouse"
)

// HarnessOptions controls the execution of the DataEngine test harness.
type HarnessOptions struct {
	EnvFile        string
	SeedData       bool
	CleanData      bool
	UpdateGolden   bool
	TestCaseFilter string
	TableName      string
	SeedFile       string
	TestCasesFile  string
	GoldenDir      string
}

// DefaultHarnessOptions returns standard default options.
func DefaultHarnessOptions() *HarnessOptions {
	return &HarnessOptions{
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
