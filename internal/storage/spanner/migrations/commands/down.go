// Copyright 2025 Google LLC
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
package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang/github_com/golang_migrate/migrate/v/v4/migrate"
	"golang/github_com/golang_migrate/migrate/v/v4/source/source"
)

const (
	ansiRed   = "\033[31m"
	ansiReset = "\033[0m"
)

type migrationPreview struct {
	identifier string
	content    string
}

// Down handles the migration down command.
func Down(migrator *migrate.Migrate, src source.Driver, steps *int, out io.Writer, in io.Reader) error {
	if steps != nil && *steps < 1 {
		return fmt.Errorf("step argument for 'down' command must be greater than 0")
	}

	numSteps := 1
	if steps != nil {
		numSteps = *steps
	}

	currentVersion, _, err := migrator.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Fprintln(out, "No migrations applied. Nothing to revert.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	// Preview changes
	fmt.Fprintln(out, "\nPreparing to revert migrations...")
	if err := printPreview(out, src, currentVersion, numSteps); err != nil {
		return fmt.Errorf("failed to preview down migrations: %w", err)
	}

	fmt.Fprint(out, ansiRed, "\nWARNING: You are about to revert migrations. This may result in DATA LOSS.\n", ansiReset)
	fmt.Fprint(out, "Do you want to continue? [Y/N]: ")
	scanner := bufio.NewScanner(in)

	// Wait for user input.
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}
		// EOF with no input
		fmt.Fprintln(out, "\nMigration aborted by user.")
		return nil
	}
	response := strings.TrimSpace(scanner.Text())
	if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
		fmt.Fprintln(out, "Migration aborted by user.")
		return nil
	}

	if err := migrator.Steps(-numSteps); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			fmt.Fprintln(out, "No changes to apply.")
			return nil
		}
		return err
	}

	stepStr := "steps"
	if numSteps == 1 {
		stepStr = "step"
	}
	fmt.Fprintf(out, "Migration down %d %s completed successfully.\n", numSteps, stepStr)
	return nil
}

func printPreview(out io.Writer, src source.Driver, currentVersion uint, steps int) error {
	previews, err := readMigrationPreviews(src, currentVersion, steps)
	if err != nil {
		return fmt.Errorf("failed to read migration previews: %w", err)
	}

	if len(previews) == 0 {
		return fmt.Errorf("no migration found for current version %d", currentVersion)
	}

	if len(previews) < steps {
		return fmt.Errorf("requested to revert %d steps, but only %d migrations are available to revert", steps, len(previews))
	}

	for _, p := range previews {
		fmt.Fprintf(out, "\n--- Migration file: %s ---\n", p.identifier)
		fmt.Fprintln(out, p.content)
	}

	return nil
}

func readMigrationPreviews(src source.Driver, currentVersion uint, steps int) ([]migrationPreview, error) {
	v := currentVersion
	var previews []migrationPreview
	for i := 0; i < steps; i++ {
		// Get content for this version
		preview, err := readMigration(src, v)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break // No more down migrations
			}
			return nil, fmt.Errorf("failed to read down migration for version %d: %w", v, err)
		}
		previews = append(previews, preview)

		// Move to previous version
		prev, err := src.Prev(v)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break // Reached beginning
			}
			return nil, fmt.Errorf("failed to get previous version from %d: %w", v, err)
		}
		v = prev
	}
	return previews, nil
}

func readMigration(src source.Driver, version uint) (migrationPreview, error) {
	reader, identifier, err := src.ReadDown(version)
	if err != nil {
		return migrationPreview{}, err
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		return migrationPreview{identifier: identifier}, fmt.Errorf("failed to read content for %q: %w", identifier, err)
	}

	return migrationPreview{
		identifier: identifier,
		content:    stripLeadingLicense(string(content)),
	}, nil
}

// stripLeadingLicense removes leading license and copyright comments starting with "--" from migration content.
func stripLeadingLicense(content string) string {
	lines := strings.Split(content, "\n")
	start := 0
	for start < len(lines) {
		line := strings.TrimSpace(lines[start])
		if line != "" && !strings.HasPrefix(line, "--") {
			break
		}
		start++
	}
	return strings.TrimSpace(strings.Join(lines[start:], "\n"))
}
