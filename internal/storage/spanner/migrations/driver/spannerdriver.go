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
// Package spannerdriver provides a Spanner driver for golang-migrate with proto bundle support.
package spannerdriver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"go/log"
	"golang/cloud_google_com/go/spanner/v/v1/admin/database/apiv1/database"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	migratedb "golang/github_com/golang_migrate/migrate/v/v4/database/database"
	migratespanner "golang/github_com/golang_migrate/migrate/v/v4/database/spanner/spanner"
	"xmanager_cloud/storage/spanner/utils/spannerutils"

	adminpb "spanner/admin/database/v1/spanner_database_admin_go_proto"
)

// Spanner implements the database.Driver interface for Spanner.
// It is a wrapper around the Migrate Spanner driver to add proto bundle support.
type Spanner struct {
	driver  *migratespanner.Spanner
	options DriverOptions
}

// DriverOptions contains the options for creating a Spanner driver.
type DriverOptions struct {
	DatabasePath     string
	ProtoDescriptors []byte
	AdminClient      *database.DatabaseAdminClient
	DataClient       *spanner.Client
}

// New creates a new Spanner driver with the given options.
func New(options DriverOptions) (*Spanner, error) {
	if options.DatabasePath == "" {
		return nil, errors.New("databasePath must not be empty")
	}
	if options.AdminClient == nil {
		return nil, errors.New("adminClient must not be nil")
	}
	if options.DataClient == nil {
		return nil, errors.New("dataClient must not be nil")
	}

	db := migratespanner.NewDB(*options.AdminClient, *options.DataClient)
	driver, err := migratespanner.WithInstance(db, &migratespanner.Config{
		DatabaseName: options.DatabasePath,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create spanner driver: %v", err)
	}

	// Cast to migratespanner.Spanner as we require the Spanner driver.
	sDriver, ok := driver.(*migratespanner.Spanner)
	if !ok {
		return nil, fmt.Errorf("migratespanner.WithInstance returned unexpected type %T", driver)
	}

	return &Spanner{
		driver:  sDriver,
		options: options,
	}, nil
}

// Run applies a migration to the database. This function implements the same logic as the Migrate
// Spanner driver, with the addition of proto bundle support.
func (s *Spanner) Run(migration io.Reader) error {
	migr, err := io.ReadAll(migration)
	if err != nil {
		return err
	}
	stmts := spannerutils.ParseDDL(string(migr))
	ctx := context.Background()
	log.InfoContextf(ctx, "Applying the following DDL statements:\n%v;", strings.Join(stmts, ";\n"))

	op, err := s.options.AdminClient.UpdateDatabaseDdl(ctx, &adminpb.UpdateDatabaseDdlRequest{
		Database:         s.options.DatabasePath,
		ProtoDescriptors: s.options.ProtoDescriptors,
		Statements:       stmts,
	})

	if err != nil {
		return &migratedb.Error{OrigErr: err, Err: "migration failed", Query: migr}
	}

	if err := op.Wait(ctx); err != nil {
		return &migratedb.Error{OrigErr: err, Err: "migration failed", Query: migr}
	}

	return nil
}

// Open delegates to the underlying Migrate Spanner driver.
func (s *Spanner) Open(url string) (migratedb.Driver, error) {
	return s.driver.Open(url)
}

// Close delegates to the underlying Migrate Spanner driver.
func (s *Spanner) Close() error {
	return s.driver.Close()
}

// Lock delegates to the underlying Migrate Spanner driver.
func (s *Spanner) Lock() error {
	return s.driver.Lock()
}

// Unlock delegates to the underlying Migrate Spanner driver.
func (s *Spanner) Unlock() error {
	return s.driver.Unlock()
}

// SetVersion delegates to the underlying Migrate Spanner driver.
func (s *Spanner) SetVersion(version int, dirty bool) error {
	return s.driver.SetVersion(version, dirty)
}

// Version delegates to the underlying Migrate Spanner driver.
func (s *Spanner) Version() (version int, dirty bool, err error) {
	return s.driver.Version()
}

// Drop delegates to the underlying Migrate Spanner driver.
func (s *Spanner) Drop() error {
	return s.driver.Drop()
}
