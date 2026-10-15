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

package migrations

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

const (
	defaultFileMode = 0644
	ansiRed         = "\033[31m"
	ansiReset       = "\033[0m"
)

// MigrationPreview holds the identifier and license-stripped SQL content of a down migration.
type MigrationPreview struct {
	Identifier string
	Content    string
}

// Up handles the migration up command using an engine-provided EmptyCheckFunc when uninitialized.
func Up(ctx context.Context, migrator *migrate.Migrate, isEmpty EmptyCheckFunc, steps *int, out io.Writer) error {
	out = DefaultWriter(out)
	if err := ensureInitializedOrEmpty(ctx, migrator, isEmpty, out); err != nil {
		return err
	}
	return applyUpMigrations(migrator, steps, out)
}

func ensureInitializedOrEmpty(ctx context.Context, migrator *migrate.Migrate, isEmpty EmptyCheckFunc, out io.Writer) error {
	migrationsInitialized, err := verifySchemaMigrationsInitialized(migrator)
	if err != nil {
		return err
	}
	if migrationsInitialized {
		return nil
	}
	empty := true
	if isEmpty != nil {
		empty, err = isEmpty(ctx)
		if err != nil {
			return fmt.Errorf("failed to check if database is empty: %w", err)
		}
	}
	if !empty {
		return fmt.Errorf("schema migrations table is not yet initialized in a non-empty database: " +
			"please verify your schema and run the 'force' command to set the correct migration version")
	}
	fmt.Fprintln(out, "Database is empty, proceeding with initial migration up.")
	return nil
}

func applyUpMigrations(migrator *migrate.Migrate, steps *int, out io.Writer) error {
	var err error
	if steps != nil {
		if *steps < 1 {
			return fmt.Errorf("step argument for 'up' command must be greater than 0")
		}
		err = migrator.Steps(*steps)
	} else {
		err = migrator.Up()
	}
	if errors.Is(err, migrate.ErrNoChange) {
		fmt.Fprintln(out, "No migrations to apply. Schema is already up to date.")
		return nil
	}
	if err != nil {
		return err
	}
	if steps != nil {
		fmt.Fprintf(out, "Migration up %d steps completed successfully.\n", *steps)
		return nil
	}
	fmt.Fprintln(out, "Migration up completed successfully.")
	return nil
}

func verifySchemaMigrationsInitialized(migrator *migrate.Migrate) (bool, error) {
	_, _, err := migrator.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to get migration version: %w", err)
	}
	return true, nil
}

// Down handles the migration down command with preview and user confirmation.
func Down(migrator *migrate.Migrate, src source.Driver, steps *int, out io.Writer, in io.Reader) error {
	out = DefaultWriter(out)
	in = DefaultReader(in)
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

	fmt.Fprintln(out, "\nPreparing to revert migrations...")
	if err := printPreview(out, src, currentVersion, numSteps); err != nil {
		return fmt.Errorf("failed to preview down migrations: %w", err)
	}

	confirmed, err := confirmRevert(out, in)
	if err != nil || !confirmed {
		return err
	}
	return applyDownSteps(migrator, numSteps, out)
}

func confirmRevert(out io.Writer, in io.Reader) (bool, error) {
	fmt.Fprint(out, ansiRed, "\nWARNING: You are about to revert migrations. This may result in DATA LOSS.\n", ansiReset)
	fmt.Fprint(out, "Do you want to continue? [Y/N]: ")
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("failed to read input: %w", err)
		}
		fmt.Fprintln(out, "\nMigration aborted by user.")
		return false, nil
	}
	response := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if response != "y" && response != "yes" {
		fmt.Fprintln(out, "Migration aborted by user.")
		return false, nil
	}
	return true, nil
}

func applyDownSteps(migrator *migrate.Migrate, numSteps int, out io.Writer) error {
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
	previews, err := ReadMigrationPreviews(src, currentVersion, steps)
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
		fmt.Fprintf(out, "\n--- Migration file: %s ---\n", p.Identifier)
		fmt.Fprintln(out, p.Content)
	}
	return nil
}

// ReadMigrationPreviews reads up to steps down-migration files starting backwards from currentVersion.
func ReadMigrationPreviews(src source.Driver, currentVersion uint, steps int) ([]MigrationPreview, error) {
	v := currentVersion
	var previews []MigrationPreview
	for i := 0; i < steps; i++ {
		preview, err := readMigration(src, v)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			return nil, fmt.Errorf("failed to read down migration for version %d: %w", v, err)
		}
		previews = append(previews, preview)

		prev, err := src.Prev(v)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			return nil, fmt.Errorf("failed to get previous version from %d: %w", v, err)
		}
		v = prev
	}
	return previews, nil
}

func readMigration(src source.Driver, version uint) (MigrationPreview, error) {
	reader, identifier, err := src.ReadDown(version)
	if err != nil {
		return MigrationPreview{}, err
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		return MigrationPreview{Identifier: identifier}, fmt.Errorf("failed to read content for %q: %w", identifier, err)
	}
	return MigrationPreview{
		Identifier: identifier,
		Content:    StripLeadingLicense(string(content)),
	}, nil
}

// StripLeadingLicense removes leading SQL comment lines ("--") and blank lines from content.
func StripLeadingLicense(content string) string {
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

// Init initializes the schema version to the latest migration version in src.
func Init(migrator *migrate.Migrate, src source.Driver, out io.Writer) error {
	out = DefaultWriter(out)
	latestVersion, err := CalculateLatestVersion(src)
	if err != nil {
		return fmt.Errorf("failed to get latest migration version: %w", err)
	}
	if err := migrator.Force(int(latestVersion)); err != nil {
		return fmt.Errorf("failed to set schema version to %d: %w", latestVersion, err)
	}
	fmt.Fprintf(out, "Schema version initialized to: %d\n", latestVersion)
	return nil
}

// CalculateLatestVersion returns the highest migration version available in d.
func CalculateLatestVersion(d source.Driver) (uint, error) {
	v, err := d.First()
	if err != nil {
		return 0, err
	}
	latest := v
	for {
		next, err := d.Next(latest)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			return 0, err
		}
		latest = next
	}
	return latest, nil
}

// CreateInDir generates a new pair of up/down migration SQL files under baseWorkspaceRelDir/migrationsDir.
func CreateInDir(baseWorkspaceRelDir, migrationsDir, name string, out io.Writer) error {
	out = DefaultWriter(out)
	if name == "" {
		return fmt.Errorf("migration name cannot be empty")
	}
	resolvedDir, err := ResolveMigrationsPath(baseWorkspaceRelDir, migrationsDir)
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
	return writeMigrationPair(resolvedDir, fmt.Sprintf("%03d", nextVersion), name, out)
}

func writeMigrationPair(resolvedDir, versionStr, name string, out io.Writer) error {
	var createdFiles []string
	for _, suffix := range []string{".up.sql", ".down.sql"} {
		fileName := fmt.Sprintf("%s_%s%s", versionStr, name, suffix)
		path := filepath.Join(resolvedDir, fileName)
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, defaultFileMode)
		if err != nil {
			for _, cf := range createdFiles {
				_ = os.Remove(cf)
			}
			return fmt.Errorf("failed to create file %q: %w", path, err)
		}
		_ = f.Close()
		createdFiles = append(createdFiles, path)
		fmt.Fprintf(out, "Created: %q\n", path)
	}
	return nil
}

func calculateNextVersion(dir string) (int, error) {
	d, err := iofs.New(os.DirFS(dir), ".")
	if err != nil {
		return 0, err
	}
	defer d.Close()

	// Return an error if the directory contains no existing migrations to guard against misconfigured paths.
	v, err := d.First()
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

// ResolveMigrationsPath resolves migrationsDir against baseWorkspaceRelDir when running via bazel run.
func ResolveMigrationsPath(baseWorkspaceRelDir, name string) (string, error) {
	if os.Getenv("TEST_TMPDIR") != "" || filepath.IsAbs(name) {
		return name, nil
	}
	ws := os.Getenv("BUILD_WORKSPACE_DIRECTORY")
	if ws == "" {
		return "", fmt.Errorf("the 'create' command must be run via 'bazel run'")
	}
	return filepath.Join(ws, baseWorkspaceRelDir, name), nil
}
