// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Command xmc-linter executes offline static validation on XMC component manifest directories.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"xmanager_cloud/pkg/components/linter"
)

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("xmc-linter", flag.ContinueOnError)
	fs.SetOutput(stderr)
	manifestsDir := fs.String("manifests-dir", "", "Path to directory containing component YAML manifests")
	rulesConfig := fs.String("rules-config", "", "Path to rules.yaml defining static checks and compatibility matrix")
	outputFormat := fs.String("output", "text", "Reporting format: text or json")
	if err := fs.Parse(args); err != nil || *manifestsDir == "" {
		if err == nil {
			fmt.Fprintf(stderr, "Error: --manifests-dir is required\n")
		}
		return 1
	}

	if *outputFormat != "text" && *outputFormat != "json" {
		fmt.Fprintf(stderr, "Error: invalid --output format %q, must be 'text' or 'json'\n", *outputFormat)
		return 1
	}

	var rules *linter.RulesConfig
	if *rulesConfig != "" {
		r, err := linter.LoadRules(*rulesConfig)
		if err != nil {
			fmt.Fprintf(stderr, "Error loading rules config %q: %v\n", *rulesConfig, err)
			return 1
		}
		rules = r
	}

	lint := linter.NewLinter(rules)
	res, err := lint.ValidateDirectory(*manifestsDir)
	if err != nil {
		fmt.Fprintf(stderr, "Error running linter on %q: %v\n", *manifestsDir, err)
		return 1
	}
	return printReport(res, *outputFormat, stdout, stderr)
}

func printReport(res *linter.ValidationResult, outputFormat string, stdout, stderr io.Writer) int {
	if outputFormat == "json" {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "Error marshaling JSON report: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(data))
		if res.HasErrors() {
			return 1
		}
		return 0
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(stdout, "[WARNING] %s\n", w)
	}
	for _, e := range res.Errors {
		fmt.Fprintf(stderr, "[ERROR] %s\n", e)
	}
	if res.HasErrors() {
		return 1
	}
	fmt.Fprintln(stdout, "All component manifests passed static validation successfully!")
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
