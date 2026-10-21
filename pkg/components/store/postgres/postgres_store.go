// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package postgres provides a PostgreSQL implementation of store.Store.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/pkg/components/store"
)

// Ensure PostgresStore implements Store interface at compile time.
var _ store.Store = (*PostgresStore)(nil)

// PostgresStore provides a PostgreSQL-backed production implementation of store.Store.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a new PostgreSQL-backed component store.
func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, pgdb.WrapStatusError("postgres pool must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	return &PostgresStore{
		pool: pool,
	}, nil
}

// SetComponentIntent applies or updates component intent within PostgreSQL.
func (s *PostgresStore) SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: SetComponentIntent is not implemented")
}

// SetSourcedComponentIntent applies an intent sent by src within PostgreSQL.
func (s *PostgresStore) SetSourcedComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, src store.RequestSource) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: SetSourcedComponentIntent is not implemented")
}

// DeclareClusterEpoch declares cluster the writer of sourced intents within PostgreSQL.
func (s *PostgresStore) DeclareClusterEpoch(ctx context.Context, cluster store.ClusterIdentity) (*store.DeclarationResult, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: DeclareClusterEpoch is not implemented")
}

// GetComponent retrieves a component record by canonical or full resource name.
func (s *PostgresStore) GetComponent(ctx context.Context, name string) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: GetComponent is not implemented")
}

// GetComponentByID retrieves a component record by its unique database identifier.
func (s *PostgresStore) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: GetComponentByID is not implemented")
}

// ListComponents returns all non-deleted components in the catalog.
func (s *PostgresStore) ListComponents(ctx context.Context) ([]*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: ListComponents is not implemented")
}

// PrepareDeactivation checks deactivation safety and cascading requirements.
func (s *PostgresStore) PrepareDeactivation(ctx context.Context, name string, requestCascade bool, src ...store.RequestSource) (*store.PrepareDeactivationResult, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: PrepareDeactivation is not implemented")
}

// CompleteDeactivation marks a component in DISABLING state as DISABLED.
func (s *PostgresStore) CompleteDeactivation(ctx context.Context, name string, force bool, src ...store.RequestSource) error {
	return status.Error(codes.Unimplemented, "postgres component store: CompleteDeactivation is not implemented")
}

// UpdateObservedGeneration synchronizes actual component access and state, returning the updated record.
func (s *PostgresStore) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string, src ...store.RequestSource) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: UpdateObservedGeneration is not implemented")
}

// ExecuteDeletion deletes a component using the specified deletion policy.
func (s *PostgresStore) ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool, src ...store.RequestSource) error {
	return status.Error(codes.Unimplemented, "postgres component store: ExecuteDeletion is not implemented")
}

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
func (s *PostgresStore) ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	return nil, nil, status.Error(codes.Unimplemented, "postgres component store: ResolveComponentSnapshot is not implemented")
}

// ValidateComponentRequirement checks if a required component type and version exists and is active.
func (s *PostgresStore) ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*store.RequirementValidationResult, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: ValidateComponentRequirement is not implemented")
}

// GetPlatformHealth retrieves overall system component health metrics and status.
func (s *PostgresStore) GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: GetPlatformHealth is not implemented")
}
