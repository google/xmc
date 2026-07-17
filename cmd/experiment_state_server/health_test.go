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
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	database "cloud.google.com/go/spanner/admin/database/apiv1"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	dbadminpb "google.golang.org/genproto/googleapis/spanner/admin/database/v1"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/storage/spanner/migrations/version"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
)

type fakeXidClient struct {
	xidgrpcpb.XidServiceClient
}

type fakeTaskQueueClient struct {
	PingErr error
}

func (f *fakeTaskQueueClient) PingRedis(ctx context.Context) error {
	return f.PingErr
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

func TestActiveHealthCheck(t *testing.T) {
	setupSpannerWithVersion := func(t *testing.T, versionVal int64, dirty bool) *spanner.Client {
		t.Helper()
		ctx := context.Background()
		_, essClient, emu := emulator.SetupWithEmulator(ctx, t)
		dbURI := fmt.Sprintf("projects/%s/instances/%s/databases/%s", "test-project", "test-instance", emu.ESSDatabase)
		createSchemaMigrationsTable(ctx, t, emu, dbURI)
		insertMigrationVersion(ctx, t, essClient, versionVal, dirty)
		return essClient
	}

	tests := []struct {
		name         string
		setupSpanner func(t *testing.T) *spanner.Client
		setupTq      func() TaskQueueClient
		wantCode     int
		wantBody     string
	}{
		{
			name: "redis_ping_error",
			setupSpanner: func(t *testing.T) *spanner.Client {
				return setupSpannerWithVersion(t, version.EssExpectedSchemaVersion(), false)
			},
			setupTq: func() TaskQueueClient {
				return &fakeTaskQueueClient{PingErr: fmt.Errorf("fake redis error")}
			},
			wantCode: http.StatusInternalServerError,
			wantBody: "fake redis error",
		},
		{
			name: "all_valid",
			setupSpanner: func(t *testing.T) *spanner.Client {
				return setupSpannerWithVersion(t, version.EssExpectedSchemaVersion(), false)
			},
			setupTq: func() TaskQueueClient {
				return &fakeTaskQueueClient{}
			},
			wantCode: http.StatusOK,
			wantBody: "OK",
		},
		{
			name:         "nil_spanner",
			setupTq:      func() TaskQueueClient { return &fakeTaskQueueClient{} },
			setupSpanner: func(t *testing.T) *spanner.Client { return nil },
			wantCode:     http.StatusInternalServerError,
			wantBody:     "spanner client is nil",
		},
		{
			name: "spanner_version_older",
			setupSpanner: func(t *testing.T) *spanner.Client {
				return setupSpannerWithVersion(t, version.EssExpectedSchemaVersion()-1, false)
			},
			setupTq:  func() TaskQueueClient { return &fakeTaskQueueClient{} },
			wantCode: http.StatusInternalServerError,
			wantBody: "is older than expected version",
		},
		{
			name: "spanner_version_dirty",
			setupSpanner: func(t *testing.T) *spanner.Client {
				return setupSpannerWithVersion(t, version.EssExpectedSchemaVersion(), true)
			},
			setupTq:  func() TaskQueueClient { return &fakeTaskQueueClient{} },
			wantCode: http.StatusInternalServerError,
			wantBody: "is in a dirty state",
		},
		{
			name: "spanner_table_missing",
			setupSpanner: func(t *testing.T) *spanner.Client {
				_, essClient, _ := emulator.SetupWithEmulator(context.Background(), t)
				return essClient
			},
			setupTq:  func() TaskQueueClient { return &fakeTaskQueueClient{} },
			wantCode: http.StatusInternalServerError,
			wantBody: "SchemaMigrations",
		},
		{
			name: "spanner_version_newer",
			setupSpanner: func(t *testing.T) *spanner.Client {
				return setupSpannerWithVersion(t, version.EssExpectedSchemaVersion()+1, false)
			},
			setupTq:  func() TaskQueueClient { return &fakeTaskQueueClient{} },
			wantCode: http.StatusOK,
			wantBody: "OK",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var spClient *spanner.Client
			if tc.setupSpanner != nil {
				spClient = tc.setupSpanner(t)
			}

			var tqClient TaskQueueClient
			if tc.setupTq != nil {
				tqClient = tc.setupTq()
			}

			var xidClient xidgrpcpb.XidServiceClient = &fakeXidClient{}
			handler := activeHealthCheck(spClient, xidClient, tqClient)

			req := httptest.NewRequest("GET", "/readyz", nil)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != tc.wantCode {
				t.Errorf("activeHealthCheck() status code = %v, want %v", rr.Code, tc.wantCode)
			}

			gotBody := rr.Body.String()
			if tc.wantBody != "" {
				if !strings.Contains(gotBody, tc.wantBody) {
					t.Errorf("activeHealthCheck() body = %q, want substring %q", gotBody, tc.wantBody)
				}
			}
		})
	}
}

func TestLivenessCheck(t *testing.T) {
	handler := livenessCheck()

	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("livenessCheck() status code = %v, want %v", rr.Code, http.StatusOK)
	}

	gotBody := rr.Body.String()
	wantBody := "OK"
	if gotBody != wantBody {
		t.Errorf("livenessCheck() body = %q, want %q", gotBody, wantBody)
	}
}
