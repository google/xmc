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
	"io"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source"
	commonmigrations "xmanager_cloud/internal/storage/migrations"
)

type migrationPreview struct {
	identifier string
	content    string
}

// Down handles the migration down command.
func Down(migrator *migrate.Migrate, src source.Driver, steps *int, out io.Writer, in io.Reader) error {
	return commonmigrations.Down(migrator, src, steps, out, in)
}

func readMigrationPreviews(src source.Driver, currentVersion uint, steps int) ([]migrationPreview, error) {
	shared, err := commonmigrations.ReadMigrationPreviews(src, currentVersion, steps)
	if err != nil {
		return nil, err
	}
	previews := make([]migrationPreview, len(shared))
	for i, p := range shared {
		previews[i] = migrationPreview{
			identifier: p.Identifier,
			content:    p.Content,
		}
	}
	return previews, nil
}

// stripLeadingLicense removes leading license and copyright comments starting with "--" from migration content.
func stripLeadingLicense(content string) string {
	return commonmigrations.StripLeadingLicense(content)
}
