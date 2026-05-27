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

package emulator

import (
	"context"
	"os"
	"testing"

	_ "embed"

	"cloud.google.com/go/spanner"
	database "cloud.google.com/go/spanner/admin/database/apiv1"
	instance "cloud.google.com/go/spanner/admin/instance/apiv1"
	"google.golang.org/api/option"
	dbadminpb "google.golang.org/genproto/googleapis/spanner/admin/database/v1"
	instancepb "google.golang.org/genproto/googleapis/spanner/admin/instance/v1"
	"xmanager_cloud/storage/spanner/utils/spannerutils"
)

var (
	//go:embed xid-schema.sql
	xidSchema string
	//go:embed xid_messages.pb
	xidDescriptors []byte
	//go:embed ess-schema.sql
	essSchema string
	//go:embed ess_messages.pb
	essDescriptors []byte
)

const (
	projectID   = "test-project"
	instanceID  = "test-instance"
	xidDatabase = "test-xid-database"
	essDatabase = "test-ess-database"
	projectURI  = "projects/test-project"
	instanceURI = "projects/test-project/instances/test-instance"
	xidDBURI    = "projects/test-project/instances/test-instance/databases/test-xid-database"
	essDBURI    = "projects/test-project/instances/test-instance/databases/test-ess-database"
)

type Options struct{}

func Start(ctx context.Context, opts Options) (*Emulator, error) {
	addr := os.Getenv("SPANNER_EMULATOR_HOST")
	if addr == "" {
		addr = "localhost:9010"
	}
	return &Emulator{addr: addr}, nil
}

type Emulator struct {
	addr string
}

func (e *Emulator) ClientOptions() []option.ClientOption {
	return []option.ClientOption{
		option.WithEndpoint(e.addr),
		option.WithoutAuthentication(),
	}
}

func (e *Emulator) Stop() {}

func Setup(ctx context.Context, t *testing.T) (*spanner.Client, *spanner.Client) {
	xidClient, essClient, _ := SetupWithEmulator(ctx, t)
	return xidClient, essClient
}

func SetupWithEmulator(ctx context.Context, t *testing.T) (*spanner.Client, *spanner.Client, *Emulator) {
	return setup(ctx, t, true)
}

func SetupEmpty(ctx context.Context, t *testing.T) (*spanner.Client, *spanner.Client, *Emulator) {
	return setup(ctx, t, false)
}

func setup(ctx context.Context, t *testing.T, applySchema bool) (*spanner.Client, *spanner.Client, *Emulator) {
	t.Helper()

	addr := os.Getenv("SPANNER_EMULATOR_HOST")
	if addr == "" {
		addr = "localhost:9010"
	}

	emu := &Emulator{addr: addr}

	if err := createInstance(ctx, emu); err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}

	if err := createDatabase(ctx, emu, xidDatabase, xidDBURI, xidSchema, xidDescriptors, applySchema); err != nil {
		t.Fatalf("failed to create XID database: %v", err)
	}

	if err := createDatabase(ctx, emu, essDatabase, essDBURI, essSchema, essDescriptors, applySchema); err != nil {
		t.Fatalf("failed to create ESS database: %v", err)
	}

	xidClient, err := spanner.NewClient(ctx, xidDBURI, emu.ClientOptions()...)
	if err != nil {
		t.Fatalf("failed to create spanner client: %v", err)
	}
	essClient, err := spanner.NewClient(ctx, essDBURI, emu.ClientOptions()...)
	if err != nil {
		t.Fatalf("failed to create spanner client: %v", err)
	}

	t.Cleanup(func() { xidClient.Close() })
	t.Cleanup(func() { essClient.Close() })

	return xidClient, essClient, emu
}

func createInstance(ctx context.Context, emu *Emulator) error {
	i, err := instance.NewInstanceAdminClient(ctx, emu.ClientOptions()...)
	if err != nil {
		return err
	}
	defer i.Close()

	op, err := i.CreateInstance(ctx, &instancepb.CreateInstanceRequest{
		Parent:     projectURI,
		InstanceId: instanceID,
		Instance: &instancepb.Instance{
			DisplayName: instanceID,
			NodeCount:   1,
		},
	})
	if err != nil {
		return nil
	}
	_, err = op.Wait(ctx)
	return err
}

func createDatabase(ctx context.Context, emu *Emulator, databaseID string, databaseURI string, schema string, descriptors []byte, applySchema bool) error {
	dba, err := database.NewDatabaseAdminClient(ctx, emu.ClientOptions()...)
	if err != nil {
		return err
	}
	defer dba.Close()

	_ = dba.DropDatabase(ctx, &dbadminpb.DropDatabaseRequest{Database: databaseURI})

	op, err := dba.CreateDatabase(ctx, &dbadminpb.CreateDatabaseRequest{
		Parent:          instanceURI,
		CreateStatement: "CREATE DATABASE `" + databaseID + "`",
	})
	if err != nil {
		return err
	}
	if _, err = op.Wait(ctx); err != nil {
		return err
	}

	if !applySchema {
		return nil
	}

	statements := spannerutils.ParseDDL(schema)
	updateOp, err := dba.UpdateDatabaseDdl(ctx, &dbadminpb.UpdateDatabaseDdlRequest{
		Database:         databaseURI,
		Statements:       statements,
		ProtoDescriptors: descriptors,
	})
	if err != nil {
		return err
	}
	return updateOp.Wait(ctx)
}
