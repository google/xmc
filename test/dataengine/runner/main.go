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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	dataenginetest "xmanager_cloud/test/dataengine"
)

func main() {
	var (
		envFile      = flag.String("env_file", ".env", "Path to .env file")
		seedData     = flag.Bool("seed", true, "Seed dataset into ClickHouse before running tests")
		cleanData    = flag.Bool("clean", false, "Clean up test metrics from ClickHouse after running tests")
		updateGolden = flag.Bool("update_golden", false, "Update golden files with actual query output")
		testCase     = flag.String("test_case", "", "Filter to run a specific test case ID")
		tableName    = flag.String("table_name", "xmc_metrics", "Target ClickHouse metrics table")
		seedFile     = flag.String("seed_file", "testdata/metrics_seed.json", "Path to seed metrics JSON file")
		casesFile    = flag.String("test_cases", "testdata/test_cases.json", "Path to test cases JSON file")
		goldenDir    = flag.String("golden_dir", "testdata/golden", "Path to golden output directory")
	)
	flag.Parse()

	opts := &dataenginetest.HarnessOptions{
		EnvFile:        *envFile,
		SeedData:       *seedData,
		CleanData:      *cleanData,
		UpdateGolden:   *updateGolden,
		TestCaseFilter: *testCase,
		TableName:      *tableName,
		SeedFile:       *seedFile,
		TestCasesFile:  *casesFile,
		GoldenDir:      *goldenDir,
	}

	printHeader(opts)

	ctx := context.Background()
	results, err := dataenginetest.Run(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: Test runner execution failed: %v\n", err)
		os.Exit(1)
	}

	passedCount, failedCount := printResults(results, opts.UpdateGolden)
	printSummary(len(results), passedCount, failedCount)

	if failedCount > 0 {
		os.Exit(1)
	}
}

func printHeader(opts *dataenginetest.HarnessOptions) {
	fmt.Println("===============================================================")
	fmt.Println("       XMC DataEngine & Grammar of Graphics E2E Runner        ")
	fmt.Println("===============================================================")
	fmt.Printf("Env file:     %s\n", opts.EnvFile)
	fmt.Printf("Table:        %s\n", opts.TableName)
	fmt.Printf("Seed data:    %t\n", opts.SeedData)
	fmt.Printf("Update golden:%t\n", opts.UpdateGolden)
	if opts.TestCaseFilter != "" {
		fmt.Printf("Filter:       %s\n", opts.TestCaseFilter)
	}
	fmt.Println("---------------------------------------------------------------")
}

func printResults(results []*dataenginetest.TestResult, updateGolden bool) (int, int) {
	passed := 0
	failed := 0
	for _, res := range results {
		if res.Passed {
			passed++
			printPassedResult(res, updateGolden)
			continue
		}
		failed++
		printFailedResult(res)
	}
	return passed, failed
}

func printPassedResult(res *dataenginetest.TestResult, updateGolden bool) {
	if updateGolden {
		fmt.Printf("[UPDATED] %-30s (%v)\n", res.TestCaseID, res.Duration)
		return
	}
	fmt.Printf("[PASS]    %-30s (%v) - %s\n", res.TestCaseID, res.Duration, res.Description)
}

func printFailedResult(res *dataenginetest.TestResult) {
	fmt.Printf("[FAIL]    %-30s (%v) - %s\n", res.TestCaseID, res.Duration, res.Description)
	if res.Error != nil {
		fmt.Printf("          Error: %v\n", res.Error)
	}
	if res.Diff != "" {
		fmt.Printf("          Diff (-want +got):\n%s\n", res.Diff)
	}
}

func printSummary(total, passed, failed int) {
	fmt.Println("---------------------------------------------------------------")
	fmt.Printf("Summary: %d total, %d passed, %d failed\n", total, passed, failed)
	fmt.Println("===============================================================")
}
