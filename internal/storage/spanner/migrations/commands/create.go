// Copyright 2025 Google LLC
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
package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang/github_com/golang_migrate/migrate/v/v4/source/iofs/iofs"
)

const defaultFileMode = 0644

// Create generates a new pair of migration files.
func Create(migrationsDir, name string) error {
	if name == "" {
		return fmt.Errorf("migration name cannot be empty")
	}
	resolvedDir, err := resolveRelativePath(migrationsDir)
	if err != nil {
		return err
	}

	if _, err := os.Stat(resolvedDir); os.IsNotExist(err) {
		return fmt.Errorf("migrations directory %q does not exist", resolvedDir)
	}

	nextVersion, err := calculateNextVersion(resolvedDir)
	if err != nil {
		return fmt.Errorf("failed to determine next version: %w", err)
	}

	versionStr := fmt.Sprintf("%03d", nextVersion)
	var createdFiles []string

	for _, suffix := range []string{".up.sql", ".down.sql"} {
		fileName := fmt.Sprintf("%s_%s%s", versionStr, name, suffix)
		path := filepath.Join(resolvedDir, fileName)

		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, defaultFileMode)
		if err != nil {
			for _, cf := range createdFiles {
				os.Remove(cf)
			}
			return fmt.Errorf("failed to create file %q: %w", path, err)
		}
		f.Close()
		createdFiles = append(createdFiles, path)
		fmt.Printf("Created: %q\n", path)
	}

	return nil
}

func calculateNextVersion(dir string) (int, error) {
	d, err := iofs.New(os.DirFS(dir), ".")
	if err != nil {
		return 0, err
	}
	defer d.Close()

	v, err := d.First()
	// in case there are no migrations in the provided directory, error is returned
	if err != nil {
		return 0, err
	}

	maxVersion := v
	for {
		next, err := d.Next(maxVersion)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			return 0, err
		}
		maxVersion = next
	}

	return int(maxVersion) + 1, nil
}

func resolveRelativePath(name string) (string, error) {
	// Allow overriding the path for testing.
	if testTmpDir := os.Getenv("TEST_TMPDIR"); testTmpDir != "" {
		return name, nil
	}

	ws := os.Getenv("BUILD_WORKSPACE_DIRECTORY")
	if ws == "" {
		return "", fmt.Errorf("the 'create' command must be run via 'blaze run'")
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("failed to get current file information")
	}

	// Go up two levels from commands/ to migrations/
	baseDir := filepath.Dir(filepath.Dir(filename))

	return filepath.Join(ws, baseDir, name), nil
}
