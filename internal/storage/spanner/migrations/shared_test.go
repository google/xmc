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
package main

import (
	"fmt"
	"testing"

	"golang/github_com/golang_migrate/migrate/v/v4/source/iofs/iofs"
	"xmanager_cloud/internal/storage/spanner/migrations/testing"
	"xmanager_cloud/storage/spanner/emulator/emulator"
)

type testRunCase struct {
	name          string
	migrationsDir string
	databaseID    string
	getDB         func(emu *emulator.Emulator) string
}

func withEmuDB(tc testRunCase, emu *emulator.Emulator) testRunCase {
	if tc.getDB == nil {
		panic(fmt.Sprintf("CRITICAL: testRunCase %q failed to declare a getDB selector callback", tc.name))
	}
	tc.databaseID = tc.getDB(emu)
	return tc
}

func mustCreateConfig(t *testing.T, tc testRunCase, command string, arg *int) *config {
	t.Helper()
	s, err := iofs.New(migrationsFS, tc.migrationsDir)
	if err != nil {
		t.Fatalf("Failed to create source driver: %v", err)
	}

	return &config{
		projectID:     "test-project",
		instanceID:    "test-instance",
		databaseID:    tc.databaseID,
		migrationsDir: tc.migrationsDir,
		command:       command,
		arg:           arg,
		customSourceDriver: &migrationtest.DriverInterceptor{
			Driver: s,
			DBName: tc.name,
		},
	}
}

func ptr(i int) *int {
	return &i
}
