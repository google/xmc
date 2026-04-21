package commands

import (
	"errors"
	"fmt"
	"os"

	"golang/github_com/golang_migrate/migrate/v/v4/migrate"
	"golang/github_com/golang_migrate/migrate/v/v4/source/source"
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
