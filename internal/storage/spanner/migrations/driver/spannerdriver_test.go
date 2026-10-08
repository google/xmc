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
package spannerdriver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"cloud_spanner_emulator/public/go/emulator"
	"golang/cloud_google_com/go/spanner/v/v1/admin/database/apiv1/database"
	"golang/cloud_google_com/go/spanner/v/v1/admin/instance/apiv1/instance"
	"golang/protobuf/v2/reflect/protodesc/protodesc"
	"google.golang.org/protobuf/proto"

	emptypb "protobuf/empty_go_proto"
	adminpb "spanner/admin/database/v1/spanner_database_admin_go_proto"
	instancepb "spanner/admin/instance/v1/spanner_instance_admin_go_proto"
	xmcemulator "xmanager_cloud/storage/spanner/emulator/emulator"
)

const (
	projectID    = "test-project"
	instanceID   = "test-instance"
	instancePath = "projects/test-project/instances/test-instance"
)

func newSpannerServer(ctx context.Context, t *testing.T) *emulator.Emulator {
	t.Helper()

	emu, err := emulator.Start(ctx, emulator.Options{})
	if err != nil {
		t.Fatalf("emulator.Start() failed: %v", err)
	}
	t.Cleanup(emu.Stop)

	instClient, err := instance.NewInstanceAdminClient(ctx, emu.ClientOptions()...)
	if err != nil {
		t.Fatalf("instance.NewInstanceAdminClient() failed: %v", err)
	}
	_, err = instClient.GetInstance(ctx, &instancepb.GetInstanceRequest{Name: instancePath})
	if err != nil {
		op1, err := instClient.CreateInstance(ctx, &instancepb.CreateInstanceRequest{
			Parent:     fmt.Sprintf("projects/%s", projectID),
			InstanceId: instanceID,
			Instance: &instancepb.Instance{
				DisplayName: instanceID,
				NodeCount:   1,
			},
		})
		if err != nil {
			t.Fatalf("instClient.CreateInstance() failed: %v", err)
		}
		if _, err := op1.Wait(ctx); err != nil {
			t.Fatalf("op1.Wait() failed: %v", err)
		}
	}

	return emu
}

func createAdminClient(ctx context.Context, t *testing.T, emu *emulator.Emulator) *database.DatabaseAdminClient {
	t.Helper()
	dbAdmin, err := database.NewDatabaseAdminClient(ctx, emu.ClientOptions()...)
	if err != nil {
		t.Fatalf("database.NewDatabaseAdminClient() failed: %v", err)
	}
	return dbAdmin
}

func createDatabase(ctx context.Context, t *testing.T, dbAdmin *database.DatabaseAdminClient, dbName string) {
	t.Helper()
	dbReq := &adminpb.CreateDatabaseRequest{
		Parent:          instancePath,
		CreateStatement: "CREATE DATABASE `" + dbName + "`",
	}
	op, err := dbAdmin.CreateDatabase(ctx, dbReq)
	if err != nil {
		t.Fatalf("dbAdmin.CreateDatabase(%q) failed: %v", dbName, err)
	}
	if _, err := op.Wait(ctx); err != nil {
		t.Fatalf("op.Wait() failed: %v", err)
	}
}

func createDataClient(ctx context.Context, t *testing.T, emu *emulator.Emulator, dbPath string) *spanner.Client {
	t.Helper()
	dataClient, err := spanner.NewClient(ctx, dbPath, emu.ClientOptions()...)
	if err != nil {
		t.Fatalf("spanner.NewClient(%q) failed: %v", dbPath, err)
	}
	return dataClient
}

func TestNew(t *testing.T) {
	ctx := t.Context()
	emu := newSpannerServer(ctx, t)
	adminClient := createAdminClient(ctx, t, emu)
	dbName := "test-db-" + xmcemulator.RandomSuffix()
	dbPath := fmt.Sprintf("%s/databases/%s", instancePath, dbName)
	createDatabase(ctx, t, adminClient, dbName)
	dataClient := createDataClient(ctx, t, emu, dbPath)

	tests := []struct {
		name    string
		options DriverOptions
		wantErr bool
	}{
		{
			name: "valid options",
			options: DriverOptions{
				DatabasePath:     dbPath,
				ProtoDescriptors: []byte{},
				AdminClient:      adminClient,
				DataClient:       dataClient,
			},
			wantErr: false,
		},
		{
			name: "missing database path",
			options: DriverOptions{
				ProtoDescriptors: []byte("test"),
				AdminClient:      adminClient,
				DataClient:       dataClient,
			},
			wantErr: true,
		},
		{
			name: "missing admin client",
			options: DriverOptions{
				DatabasePath:     dbPath,
				ProtoDescriptors: []byte("test"),
				DataClient:       dataClient,
			},
			wantErr: true,
		},
		{
			name: "missing data client",
			options: DriverOptions{
				DatabasePath:     dbPath,
				ProtoDescriptors: []byte("test"),
				AdminClient:      adminClient,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.options)
			if (err != nil) != tt.wantErr {
				t.Errorf("New(%v) got error = %v, wantErr %v", tt.options, err, tt.wantErr)
			}
		})
	}
}

func emptyProtoDescriptorBytes(t *testing.T) []byte {
	t.Helper()
	fds := &descpb.FileDescriptorSet{
		File: []*descpb.FileDescriptorProto{
			protodesc.ToFileDescriptorProto(emptypb.File_google_protobuf_empty_proto),
		},
	}
	bytes, err := proto.Marshal(fds)
	if err != nil {
		t.Fatalf("proto.Marshal(FileDescriptorSet) failed: %v", err)
	}
	return bytes
}

func TestRun(t *testing.T) {
	ctx := t.Context()
	emu := newSpannerServer(ctx, t)
	adminClient := createAdminClient(ctx, t, emu)

	tests := []struct {
		name             string
		migration        string
		protoDescriptors []byte
		wantErr          bool
		wantDDLs         []string
	}{
		{
			name:             "simple create table",
			migration:        "CREATE TABLE MyTable (Id INT64, Name STRING(MAX)) PRIMARY KEY (Id)",
			protoDescriptors: []byte{},
			wantErr:          false,
			wantDDLs:         []string{"CREATE TABLE SchemaMigrations", "CREATE TABLE MyTable"},
		},
		{
			name:             "multiple statements",
			migration:        "CREATE TABLE EmptyTable1 () PRIMARY KEY (); CREATE TABLE EmptyTable2 () PRIMARY KEY ();",
			protoDescriptors: []byte{},
			wantErr:          false,
			wantDDLs:         []string{"CREATE TABLE SchemaMigrations", "CREATE TABLE EmptyTable1", "CREATE TABLE EmptyTable2"},
		},
		{
			name:             "create table with proto column",
			migration:        "CREATE PROTO BUNDLE (google.protobuf.Empty); CREATE TABLE MyTable (Id INT64, ProtoColumn `google.protobuf.Empty`) PRIMARY KEY (Id)",
			protoDescriptors: emptyProtoDescriptorBytes(t),
			wantErr:          false,
			wantDDLs:         []string{"CREATE TABLE SchemaMigrations", "CREATE PROTO BUNDLE", "CREATE TABLE MyTable"},
		},
		{
			name:             "invalid ddl",
			migration:        "INVALID DDL",
			protoDescriptors: []byte{},
			wantErr:          true,
			// regardless of the error, SchemaMigrations is always created.
			wantDDLs: []string{"CREATE TABLE SchemaMigrations"},
		},
		{
			name:             "invalid proto descriptors",
			migration:        "CREATE PROTO BUNDLE (google.protobuf.Empty)",
			protoDescriptors: []byte("invalid"),
			wantErr:          true,
			// regardless of the error, SchemaMigrations is always created.
			wantDDLs: []string{"CREATE TABLE SchemaMigrations"},
		},
		{
			name:             "complex ddl (identity & proto)",
			migration:        "CREATE PROTO BUNDLE (google.protobuf.Empty); CREATE TABLE ComplexTable (Id INT64 GENERATED BY DEFAULT AS IDENTITY (BIT_REVERSED_POSITIVE)) PRIMARY KEY (Id)",
			protoDescriptors: emptyProtoDescriptorBytes(t),
			wantErr:          false,
			wantDDLs:         []string{"CREATE TABLE SchemaMigrations", "CREATE PROTO BUNDLE", "CREATE TABLE ComplexTable"},
		},
		{
			name:             "strip comments",
			migration:        "-- this is a comment\nCREATE TABLE CommentTable (Id INT64) PRIMARY KEY (Id); -- another comment",
			protoDescriptors: []byte{},
			wantErr:          false,
			wantDDLs:         []string{"CREATE TABLE SchemaMigrations", "CREATE TABLE CommentTable"},
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new database for each test case.
			dbName := fmt.Sprintf("testdb-%d-%s", i, xmcemulator.RandomSuffix())
			dbPath := fmt.Sprintf("%s/databases/%s", instancePath, dbName)
			createDatabase(ctx, t, adminClient, dbName)
			dataClient := createDataClient(ctx, t, emu, dbPath)
			defer dataClient.Close()

			driver, err := New(DriverOptions{
				DatabasePath:     dbPath,
				ProtoDescriptors: tt.protoDescriptors,
				AdminClient:      adminClient,
				DataClient:       dataClient,
			})
			if err != nil {
				t.Fatalf("New() failed: %v", err)
			}

			err = driver.Run(strings.NewReader(tt.migration))
			t.Logf("Run() error: %v", err)
			if (err != nil) != tt.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}

			ddl, err := adminClient.GetDatabaseDdl(ctx, &adminpb.GetDatabaseDdlRequest{Database: dbPath})
			if err != nil {
				t.Fatalf("GetDatabaseDdl() failed: %v", err)
			}

			if len(ddl.Statements) != len(tt.wantDDLs) {
				t.Errorf("GetDatabaseDdl() got %d statements (%v), want %d (%v)", len(ddl.Statements), ddl.Statements, len(tt.wantDDLs), tt.wantDDLs)
			}

			for _, wantDDL := range tt.wantDDLs {
				found := false
				for _, stmt := range ddl.Statements {
					if strings.Contains(stmt, wantDDL) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Run() got ddl: %v, want to contain substring %q", ddl.Statements, wantDDL)
				}
			}
		})
	}
}
