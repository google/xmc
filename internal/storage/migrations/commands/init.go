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
	"errors"
	"fmt"
	"os"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source"
)

// Init initializes the schema version to the latest migration version.
func Init(migrator *migrate.Migrate, src source.Driver) error {
	latestVersion, err := calculateLatestVersion(src)
	if err != nil {
		return fmt.Errorf("failed to get latest migration version: %w", err)
	}

	if err := migrator.Force(int(latestVersion)); err != nil {
		return fmt.Errorf("failed to set schema version to %d: %w", latestVersion, err)
	}

	fmt.Printf("Schema version initialized to: %d\n", latestVersion)
	return nil
}

func calculateLatestVersion(d source.Driver) (uint, error) {
	v, err := d.First()
	if err != nil {
		return 0, err
	}
	latest := v
	for {
		next, err := d.Next(latest)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) { // End of migrations
				break
			}
			return 0, err
		}
		latest = next
	}
	return latest, nil
}
