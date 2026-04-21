// Package emulator provides a test double for the XManager Cloud Spanner
// storage layer, backed by an in-memory Cloud Spanner emulator.
package emulator

import (
	"context"
	"fmt"
	"testing"

	_ "embed"

	"cloud_spanner_emulator/public/go/emulator"
	"golang/cloud_google_com/go/spanner/v/v1/admin/database/apiv1/database"
	"golang/cloud_google_com/go/spanner/v/v1/admin/instance/apiv1/instance"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/storage/spanner/utils/spannerutils"

	dbadminpb "spanner/admin/database/v1/spanner_database_admin_go_proto"
	instancepb "spanner/admin/instance/v1/spanner_instance_admin_go_proto"
)

var (
	//go:embed xmanager_cloud/storage/spanner/schema/xid-schema.sql
	xidSchema string
	//go:embed xmanager_cloud/xid_service/proto/xid_messages.pb
	xidDescriptors []byte
	//go:embed xmanager_cloud/storage/spanner/schema/ess-schema.sql
	essSchema string
	//go:embed xmanager_cloud/experiment_state_server/proto/ess_messages.pb
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

// Setup starts the emulator and creates clients for the XID and ESS databases, respectively.
func Setup(ctx context.Context, t *testing.T) (*spanner.Client, *spanner.Client) {
	xidClient, essClient, _ := SetupWithEmulator(ctx, t)
	return xidClient, essClient
}

// SetupWithEmulator starts the emulator and creates clients, returning the emulator instance.
func SetupWithEmulator(ctx context.Context, t *testing.T) (*spanner.Client, *spanner.Client, *emulator.Emulator) {
	return setup(ctx, t, true)
}

// SetupEmpty starts the emulator and creates clients for empty databases (no schema), returning the emulator instance.
func SetupEmpty(ctx context.Context, t *testing.T) (*spanner.Client, *spanner.Client, *emulator.Emulator) {
	return setup(ctx, t, false)
}

func setup(ctx context.Context, t *testing.T, applySchema bool) (*spanner.Client, *spanner.Client, *emulator.Emulator) {
	t.Helper()

	// Start the emulator.
	emu := startEmulator(ctx, t, applySchema)

	// Create clients for the XID and ESS databases.
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

// createInstance creates a new instance in the emulator, which is required for creating databases.
func createInstance(ctx context.Context, emu *emulator.Emulator) error {
	i, err := instance.NewInstanceAdminClient(ctx, emu.ClientOptions()...)
	if err != nil {
		return fmt.Errorf("failed to create an instance admin client for emulator: %v", err)
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
		return err
	}
	_, err = op.Wait(ctx)
	return err
}

// createDatabase creates a new database in the emulator.
func createDatabase(ctx context.Context, emu *emulator.Emulator, databaseID string, databaseURI string, schema string, descriptors []byte, applySchema bool) error {
	dba, err := database.NewDatabaseAdminClient(ctx, emu.ClientOptions()...)
	if err != nil {
		return fmt.Errorf("failed to create a database admin client for emulator: %v", err)
	}
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

	// Apply schema.
	statements := spannerutils.ParseDDL(schema)
	updateOp, err := dba.UpdateDatabaseDdl(ctx, &dbadminpb.UpdateDatabaseDdlRequest{
		Database:         databaseURI,
		Statements:       statements,
		ProtoDescriptors: descriptors,
	})
	if err != nil {
		return fmt.Errorf("failed to update database DDL for %s: %w", databaseURI, err)
	}
	err = updateOp.Wait(ctx)
	return err
}

func startEmulator(ctx context.Context, t *testing.T, applySchema bool) *emulator.Emulator {
	t.Helper()

	emu, err := emulator.Start(ctx, emulator.Options{})
	if err != nil {
		t.Fatalf("failed to bring up in-process cloud spanner emulator: %v", err)
	}

	t.Cleanup(emu.Stop)

	if err := createInstance(ctx, emu); err != nil {
		t.Fatalf("failed to create an instance in emulator: %v", err)
	}

	if err := createDatabase(ctx, emu, xidDatabase, xidDBURI, xidSchema, xidDescriptors, applySchema); err != nil {
		t.Fatalf("failed to create XID database in emulator: %v", err)
	}

	if err := createDatabase(ctx, emu, essDatabase, essDBURI, essSchema, essDescriptors, applySchema); err != nil {
		t.Fatalf("failed to create ESS database in emulator: %v", err)
	}
	return emu
}
