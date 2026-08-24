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
	"os"

	"xmanager_cloud/pkg/components/linter"
)

func main() {
	manifestsDir := flag.String("manifests-dir", "", "Path to directory containing component YAML manifests")
	rulesConfig := flag.String("rules-config", "", "Path to rules.yaml defining static checks and compatibility matrix")
	outputFormat := flag.String("output", "text", "Reporting format: text or json")
	flag.Parse()

	if *manifestsDir == "" {
		fmt.Fprintf(os.Stderr, "Error: --manifests-dir is required\n")
		flag.Usage()
		os.Exit(1)
	}

	lint := linter.NewLinter()
	res, err := lint.ValidateDirectory(*manifestsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running linter on %q: %v\n", *manifestsDir, err)
		os.Exit(1)
	}

	if *outputFormat == "json" {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling JSON diagnostics: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
		if !res.Valid {
			os.Exit(1)
		}
		return
	}

	for _, w := range res.Warnings {
		fmt.Printf("[WARNING] %s\n", w)
	}
	if !res.Valid {
		fmt.Fprintf(os.Stderr, "Validation FAILED with %d error(s):\n", len(res.Errors))
		for _, e := range res.Errors {
			fmt.Fprintf(os.Stderr, "  [ERROR] %s\n", e)
		}
		os.Exit(1)
	}
	_ = rulesConfig
	fmt.Println("All component manifests passed static validation successfully!")
}
