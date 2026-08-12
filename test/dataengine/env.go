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
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadEnvFile reads KEY=VALUE declarations from a file and sets them in os.Environ
// if they are not already set.
func LoadEnvFile(filename string) error {
	if filename == "" {
		filename = ".env"
	}

	// Try relative or root candidate paths if direct file is not found
	targetPath := filename
	if _, err := os.Stat(targetPath); err != nil {
		candidates := []string{
			filename,
			filepath.Join("..", filename),
			filepath.Join("..", "..", filename),
			filepath.Join("..", "..", "..", filename),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				targetPath = c
				break
			}
		}
	}

	f, err := os.Open(targetPath)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}
