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
package health

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"cloud.google.com/go/spanner"
	database "cloud.google.com/go/spanner/admin/database/apiv1"
	dbadminpb "google.golang.org/genproto/googleapis/spanner/admin/database/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"xmanager_cloud/internal/storage/spanner/emulator"
)

func TestCheckSpanner(t *testing.T) {
	ctx := context.Background()

	t.Run("nil client returns ErrNilClient", func(t *testing.T) {
		if err := CheckSpanner(ctx, nil); !errors.Is(err, ErrNilClient) {
			t.Errorf("CheckSpanner(ctx, nil) = %v, want %v", err, ErrNilClient)
		}
	})

	t.Run("healthy client returns no error", func(t *testing.T) {
		xidClient, _, _ := emulator.SetupWithEmulator(ctx, t)
		// xidClient is automatically closed by Cleanup in emulator.SetupWithEmulator

		if err := CheckSpanner(ctx, xidClient); err != nil {
			t.Errorf("CheckSpanner(ctx, healthyClient) = %v, want nil", err)
		}
	})

	t.Run("closed client returns error", func(t *testing.T) {
		// We need a real client to close it.
		xidClient, _, _ := emulator.SetupWithEmulator(ctx, t)
		xidClient.Close()

		if err := CheckSpanner(ctx, xidClient); err == nil {
			t.Error("CheckSpanner(ctx, closedClient) succeeded, want error")
		}
	})
}

func createSchemaMigrationsTable(ctx context.Context, t *testing.T, emu *emulator.Emulator, dbURI string) {
	t.Helper()
	dba, err := database.NewDatabaseAdminClient(ctx, emu.ClientOptions()...)
	if err != nil {
		t.Fatalf("failed to create db admin client: %v", err)
	}
	defer dba.Close()

	op, err := dba.UpdateDatabaseDdl(ctx, &dbadminpb.UpdateDatabaseDdlRequest{
		Database: dbURI,
		Statements: []string{
			`CREATE TABLE SchemaMigrations (
				Version INT64,
				Dirty BOOL
			) PRIMARY KEY ()`,
		},
	})
	if err != nil {
		t.Fatalf("failed to update ddl: %v", err)
	}
	if err := op.Wait(ctx); err != nil {
		t.Fatalf("failed to wait for ddl update: %v", err)
	}
}

func insertMigrationVersion(ctx context.Context, t *testing.T, client *spanner.Client, version int64, dirty bool) {
	t.Helper()
	_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		// Clear existing rows first.
		if err := txn.BufferWrite([]*spanner.Mutation{
			spanner.Delete("SchemaMigrations", spanner.AllKeys()),
		}); err != nil {
			return err
		}
		// Insert new row.
		m := spanner.Insert("SchemaMigrations", []string{"Version", "Dirty"}, []interface{}{version, dirty})
		return txn.BufferWrite([]*spanner.Mutation{m})
	})
	if err != nil {
		t.Fatalf("failed to insert migration version: %v", err)
	}
}

func setupTestTable(ctx context.Context, t *testing.T, version int64, dirty bool) *spanner.Client {
	t.Helper()
	_, client, emu := emulator.SetupEmpty(ctx, t)
	dbURI := fmt.Sprintf("projects/%s/instances/%s/databases/%s", "test-project", "test-instance", emu.ESSDatabase)
	createSchemaMigrationsTable(ctx, t, emu, dbURI)
	insertMigrationVersion(ctx, t, client, version, dirty)
	return client
}

func TestCheckSpannerWithVersion(t *testing.T) {
	ctx := context.Background()

	t.Run("nil client returns ErrNilClient", func(t *testing.T) {
		if err := CheckSpannerWithVersion(ctx, nil, 1); !errors.Is(err, ErrNilClient) {
			t.Errorf("CheckSpannerWithVersion(ctx, nil, 1) = %v, want %v", err, ErrNilClient)
		}
	})

	t.Run("invalid expectedVersion <= 0 returns error", func(t *testing.T) {
		_, essClient, _ := emulator.SetupEmpty(ctx, t)
		if err := CheckSpannerWithVersion(ctx, NewSpannerHealthClient(essClient), 0); err == nil {
			t.Error("CheckSpannerWithVersion(ctx, client, 0) succeeded, want error")
		}
		if err := CheckSpannerWithVersion(ctx, NewSpannerHealthClient(essClient), -1); err == nil {
			t.Error("CheckSpannerWithVersion(ctx, client, -1) succeeded, want error")
		}
	})

	t.Run("table does not exist returns error", func(t *testing.T) {
		_, essClient, _ := emulator.SetupEmpty(ctx, t)
		// Table SchemaMigrations is not created.
		err := CheckSpannerWithVersion(ctx, NewSpannerHealthClient(essClient), 1)
		if err == nil {
			t.Error("CheckSpannerWithVersion succeeded but table does not exist, want error")
		}
	})

	t.Run("empty table returns error", func(t *testing.T) {
		_, essClient, emu := emulator.SetupEmpty(ctx, t)
		dbURI := fmt.Sprintf("projects/%s/instances/%s/databases/%s", "test-project", "test-instance", emu.ESSDatabase)
		createSchemaMigrationsTable(ctx, t, emu, dbURI)

		err := CheckSpannerWithVersion(ctx, NewSpannerHealthClient(essClient), 1)
		if err == nil {
			t.Error("CheckSpannerWithVersion succeeded on empty table, want error")
		}
	})

	t.Run("version matches expected returns no error", func(t *testing.T) {
		essClient := setupTestTable(ctx, t, 5, false)

		if err := CheckSpannerWithVersion(ctx, NewSpannerHealthClient(essClient), 5); err != nil {
			t.Errorf("CheckSpannerWithVersion(ctx, client, 5) = %v, want nil", err)
		}
	})

	t.Run("version higher than expected returns error", func(t *testing.T) {
		essClient := setupTestTable(ctx, t, 10, false)

		if err := CheckSpannerWithVersion(ctx, NewSpannerHealthClient(essClient), 5); err == nil {
			t.Error("CheckSpannerWithVersion succeeded but version is higher, want error")
		}
	})

	t.Run("version lower than expected returns error", func(t *testing.T) {
		essClient := setupTestTable(ctx, t, 3, false)

		err := CheckSpannerWithVersion(ctx, NewSpannerHealthClient(essClient), 5)
		if err == nil {
			t.Error("CheckSpannerWithVersion succeeded but version is lower, want error")
		}
	})

	t.Run("dirty migration returns error", func(t *testing.T) {
		essClient := setupTestTable(ctx, t, 5, true)

		err := CheckSpannerWithVersion(ctx, NewSpannerHealthClient(essClient), 5)
		if err == nil {
			t.Error("CheckSpannerWithVersion succeeded on dirty migration, want error")
		}
	})

	t.Run("closed client returns error", func(t *testing.T) {
		essClient := setupTestTable(ctx, t, 5, false)
		essClient.Close()

		if err := CheckSpannerWithVersion(ctx, NewSpannerHealthClient(essClient), 5); err == nil {
			t.Error("CheckSpannerWithVersion succeeded on closed client, want error")
		}
	})
}

type mockDiscovery struct {
	discovery.DiscoveryInterface
	serverVersionFunc func() (*version.Info, error)
}

func (m mockDiscovery) ServerVersion() (*version.Info, error) {
	if m.serverVersionFunc != nil {
		return m.serverVersionFunc()
	}
	return nil, nil
}

type mockClientset struct {
	kubernetes.Interface
	discoveryFunc func() discovery.DiscoveryInterface
}

func (m mockClientset) Discovery() discovery.DiscoveryInterface {
	if m.discoveryFunc != nil {
		return m.discoveryFunc()
	}
	return nil
}

func TestCheckKubernetes(t *testing.T) {
	t.Run("nil clientset returns ErrNilClient", func(t *testing.T) {
		if err := CheckKubernetes(nil); !errors.Is(err, ErrNilClient) {
			t.Errorf("CheckKubernetes(nil) = %v, want %v", err, ErrNilClient)
		}
	})

	t.Run("nil discovery returns ErrNilClient", func(t *testing.T) {
		mockCS := mockClientset{
			discoveryFunc: func() discovery.DiscoveryInterface {
				return nil
			},
		}
		if err := CheckKubernetes(mockCS); !errors.Is(err, ErrNilClient) {
			t.Errorf("CheckKubernetes(nil discovery) = %v, want %v", err, ErrNilClient)
		}
	})

	t.Run("discovery error returns error", func(t *testing.T) {
		expectedErr := errors.New("server version error")
		mockCS := mockClientset{
			discoveryFunc: func() discovery.DiscoveryInterface {
				return mockDiscovery{
					serverVersionFunc: func() (*version.Info, error) {
						return nil, expectedErr
					},
				}
			},
		}
		if err := CheckKubernetes(mockCS); err == nil || !errors.Is(err, expectedErr) {
			t.Errorf("CheckKubernetes(discovery error) = %v, want error wrapping %v", err, expectedErr)
		}
	})

	t.Run("healthy returns no error", func(t *testing.T) {
		mockCS := mockClientset{
			discoveryFunc: func() discovery.DiscoveryInterface {
				return mockDiscovery{
					serverVersionFunc: func() (*version.Info, error) {
						return &version.Info{}, nil
					},
				}
			},
		}
		if err := CheckKubernetes(mockCS); err != nil {
			t.Errorf("CheckKubernetes(healthy) = %v, want nil", err)
		}
	})
}
