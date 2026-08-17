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

// Command xmc-linter executes static linting and validation on XMC component manifest directories.
package main

import (
	"flag"
	"fmt"
	"os"

	"xmanager_cloud/pkg/components/linter"
)

func main() {
	var (
		manifestsDir = flag.String("manifests-dir", "", "Path to the directory containing component YAML manifests")
		rulesConfig  = flag.String("rules-config", "", "Path to rules.yaml defining static checks and compatibility matrix")
	)
	flag.Parse()

	if *manifestsDir == "" {
		fmt.Fprintf(os.Stderr, "Error: --manifests-dir is required\n")
		flag.Usage()
		os.Exit(1)
	}

	var rules linter.RulesConfig
	if *rulesConfig != "" {
		var err error
		rules, err = linter.LoadRules(*rulesConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading rules config %q: %v\n", *rulesConfig, err)
			os.Exit(1)
		}
	}

	lint := linter.NewLinter(rules)
	res, err := lint.ValidateDirectory(*manifestsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running linter on %q: %v\n", *manifestsDir, err)
		os.Exit(1)
	}

	if len(res.Warnings) > 0 {
		fmt.Println("Warnings:")
		for _, w := range res.Warnings {
			fmt.Printf("  - %s\n", w)
		}
	}

	if !res.Valid {
		fmt.Fprintf(os.Stderr, "Validation FAILED with %d error(s):\n", len(res.Errors))
		for _, e := range res.Errors {
			fmt.Fprintf(os.Stderr, "  [ERROR] %s\n", e)
		}
		os.Exit(1)
	}

	fmt.Println("All component manifests passed static validation successfully!")
}
