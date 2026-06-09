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
package emulator

import (
	"context"
	"os"
	"testing"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
)

func tableExists(ctx context.Context, t *testing.T, client *spanner.Client, tableName string) bool {
	t.Helper()
	stmt := spanner.NewStatement("SELECT table_name FROM information_schema.tables WHERE table_catalog = '' and table_schema = '' AND table_name = @tableName")
	stmt.Params["tableName"] = tableName
	iter := client.Single().Query(ctx, stmt)
	defer iter.Stop()
	_, err := iter.Next()
	if err == iterator.Done {
		return false
	}
	if err != nil {
		t.Fatalf("Failed to query INFORMATION_SCHEMA.TABLES for table %s: %v", tableName, err)
	}
	return true
}

func TestEmulatorSetup(t *testing.T) {
	ctx := context.Background()
	xidClient, essClient := Setup(ctx, t)

	// Check XID tables
	xidTables := []string{"Xids"}
	for _, table := range xidTables {
		if !tableExists(ctx, t, xidClient, table) {
			t.Errorf("XID table %s does not exist after setup", table)
		}
	}

	// Check ESS tables
	essTables := []string{"Experiments", "WorkUnits", "Operations", "WorkUnitExecutions"}
	for _, table := range essTables {
		if !tableExists(ctx, t, essClient, table) {
			t.Errorf("ESS table %s does not exist after setup", table)
		}
	}
}

func TestCreateInstance_Concurrent(t *testing.T) {
	ctx := context.Background()
	addr := os.Getenv("SPANNER_EMULATOR_HOST")
	if addr == "" {
		addr = "localhost:9010"
	}
	emu := &Emulator{addr: addr}

	// Run createInstance concurrently in multiple goroutines
	const numGoroutines = 5
	errChan := make(chan error, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			errChan <- createInstance(ctx, emu)
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		err := <-errChan
		if err != nil {
			t.Errorf("Concurrent createInstance failed: %v", err)
		}
	}
}
