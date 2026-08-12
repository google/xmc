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

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
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

	if opts.SeedData {
		_ = CleanupTestMetrics(ctx, conn, opts.TableName, 9001, 9002)
		count, err := SeedDataset(ctx, conn, opts.TableName, opts.SeedFile)
		if err != nil {
			return nil, fmt.Errorf("failed to seed dataset: %w", err)
		}
		fmt.Printf("--> Seeded %d records into %s\n", count, opts.TableName)
	}

	engine, err := dataengine.New(conn, dataengine.WithTableName(opts.TableName))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize DataEngine: %w", err)
	}

	testCases, err := LoadTestCases(opts.TestCasesFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load test cases: %w", err)
	}

	var results []*TestResult
	for _, tc := range testCases {
		if opts.TestCaseFilter != "" && tc.ID != opts.TestCaseFilter {
			continue
		}
		res := executeTestCase(ctx, engine, tc, opts)
		results = append(results, res)
	}

	if opts.CleanData {
		if err := CleanupTestMetrics(ctx, conn, opts.TableName, 9001, 9002); err != nil {
			fmt.Printf("WARNING: Failed to cleanup test metrics: %v\n", err)
		}
	}

	return results, nil
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
