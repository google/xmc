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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	_ "embed"

	"cloud.google.com/go/spanner"
	database "cloud.google.com/go/spanner/admin/database/apiv1"
	instance "cloud.google.com/go/spanner/admin/instance/apiv1"
	"google.golang.org/api/option"
	dbadminpb "cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	instancepb "cloud.google.com/go/spanner/admin/instance/apiv1/instancepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	projectURI  = "projects/test-project"
	instanceURI = "projects/test-project/instances/test-instance"
)

func RandomSuffix() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

type Options struct{}

func Start(ctx context.Context, opts Options) (*Emulator, error) {
	addr := os.Getenv("SPANNER_EMULATOR_HOST")
	if addr == "" {
		addr = "localhost:9010"
		os.Setenv("SPANNER_EMULATOR_HOST", addr)
	}
	return &Emulator{addr: addr}, nil
}

type Emulator struct {
	addr        string
	XIDDatabase string
	ESSDatabase string
}

func (e *Emulator) ClientOptions() []option.ClientOption {
	return []option.ClientOption{
		option.WithEndpoint(e.addr),
		option.WithoutAuthentication(),
	}
}

func (e *Emulator) Stop() {}

// EmulatorClients contains the initialized Spanner clients and Emulator instance.
type EmulatorClients struct {
	XIDClient *spanner.Client
	ESSClient *spanner.Client
	Emulator  *Emulator
}

func Setup(ctx context.Context, t *testing.T) (*spanner.Client, *spanner.Client) {
	c := SetupWithEmulator(ctx, t)
	return c.XIDClient, c.ESSClient
}

func SetupWithEmulator(ctx context.Context, t *testing.T) EmulatorClients {
	return setup(ctx, t, true)
}

func SetupEmpty(ctx context.Context, t *testing.T) EmulatorClients {
	return setup(ctx, t, false)
}

func setup(ctx context.Context, t *testing.T, applySchema bool) EmulatorClients {
	t.Helper()

	addr := os.Getenv("SPANNER_EMULATOR_HOST")
	if addr == "" {
		addr = "localhost:9010"
		os.Setenv("SPANNER_EMULATOR_HOST", addr)
	}

	emu := &Emulator{addr: addr}

	if err := createInstance(ctx, emu); err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}

	suffix := RandomSuffix()
	xidDatabase := fmt.Sprintf("test-xid-db-%s", suffix)
	essDatabase := fmt.Sprintf("test-ess-db-%s", suffix)

	emu.XIDDatabase = xidDatabase
	emu.ESSDatabase = essDatabase
	xidDBURI := fmt.Sprintf("projects/%s/instances/%s/databases/%s", projectID, instanceID, xidDatabase)
	essDBURI := fmt.Sprintf("projects/%s/instances/%s/databases/%s", projectID, instanceID, essDatabase)

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

	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		dba, err := database.NewDatabaseAdminClient(cleanupCtx, emu.ClientOptions()...)
		if err != nil {
			return
		}
		defer dba.Close()
		_ = dba.DropDatabase(cleanupCtx, &dbadminpb.DropDatabaseRequest{Database: xidDBURI})
		_ = dba.DropDatabase(cleanupCtx, &dbadminpb.DropDatabaseRequest{Database: essDBURI})
	})

	return EmulatorClients{
		XIDClient: xidClient,
		ESSClient: essClient,
		Emulator:  emu,
	}
}

func createInstance(ctx context.Context, emu *Emulator) error {
	i, err := instance.NewInstanceAdminClient(ctx, emu.ClientOptions()...)
	if err != nil {
		return err
	}
	defer i.Close()

	_, err = i.GetInstance(ctx, &instancepb.GetInstanceRequest{Name: instanceURI})
	if err == nil {
		return nil
	}

	op, err := i.CreateInstance(ctx, &instancepb.CreateInstanceRequest{
		Parent:     projectURI,
		InstanceId: instanceID,
		Instance: &instancepb.Instance{
			DisplayName: instanceID,
			NodeCount:   1,
		},
	})
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return nil
		}
		return fmt.Errorf("CreateInstance failed: %w", err)
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
