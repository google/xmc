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

// Package store provides storage interfaces, record definitions, and test doubles
// for the XMC Components System.
package store

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// Sentinel errors for component store operations.
var (
	ErrComponentNotFound      = status.Error(codes.NotFound, "component not found")
	ErrComponentAlreadyExists = status.Error(codes.AlreadyExists, "component already exists")
	ErrDeletionProtected      = status.Error(codes.FailedPrecondition, "component deletion protection is enabled")
	ErrHasActiveDependents    = status.Error(codes.FailedPrecondition, "component has active dependents")
	ErrInvalidGeneration      = status.Error(codes.InvalidArgument, "invalid generation")
	ErrInvalidStateTransition = status.Error(codes.FailedPrecondition, "invalid state transition")
	ErrDependencyNotFound     = status.Error(codes.FailedPrecondition, "dependency not found")
	ErrDependencyCycle        = status.Error(codes.FailedPrecondition, "dependency cycle detected")
)

// ComponentRecord represents a component catalog entity stored in the Component Store.
type ComponentRecord struct {
	ComponentID        int64
	Name               string
	Spec               *componentspb.ComponentSpec
	Status             *componentspb.ComponentAccessStatus
	State              componentspb.ComponentState
	Generation         int64
	ObservedGeneration *int64
	Dependencies       []string
	CreateTime         time.Time
	UpdateTime         time.Time
	DeleteTime         *time.Time
	Labels             map[string]string
	Etag               string
}

// PrepareDeactivationResult contains safety check outcomes for component deactivation.
type PrepareDeactivationResult struct {
	IsSafeToDeactivate    bool
	BlockingDependents    []string
	RecommendedDrainSec   int32
	RejectionReason       string
	CascadedDeactivations []string
}

// RequirementValidationResult holds the outcome of a requirement validation check.
type RequirementValidationResult struct {
	IsSatisfied             bool
	SatisfyingComponentName string
	Reason                  string
	Component               *ComponentRecord
}

// SnapshotRecord represents an immutable snapshot of a component at a specific generation.
type SnapshotRecord struct {
	Name        string
	Generation  int64
	Record      *ComponentRecord
	Coordinates *componentspb.ResolvedComponentCoordinates
	CreatedAt   time.Time
}

// Store defines the storage interface for XMC Components System.
type Store interface {
	// SetComponentIntent applies or updates component intent within the store.
	SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*ComponentRecord, error)

	// GetComponent retrieves a component record by canonical or full resource name.
	GetComponent(ctx context.Context, name string) (*ComponentRecord, error)

	// GetComponentByID retrieves a component record by its unique database identifier.
	GetComponentByID(ctx context.Context, componentID int64) (*ComponentRecord, error)

	// ListComponents returns all non-deleted components in the catalog.
	ListComponents(ctx context.Context) ([]*ComponentRecord, error)

	// PrepareDeactivation checks deactivation safety and cascading requirements.
	PrepareDeactivation(ctx context.Context, name string, requestCascade bool) (*PrepareDeactivationResult, error)

	// CompleteDeactivation marks a component in DISABLING state as DISABLED.
	CompleteDeactivation(ctx context.Context, name string) error

	// UpdateObservedGeneration synchronizes actual component access and state.
	UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string) error

	// ExecuteDeletion deletes a component using the specified deletion policy.
	ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool) error

	// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
	ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*ComponentRecord, *componentspb.ResolvedComponentCoordinates, error)

	// ValidateComponentRequirement checks if a required component type and version exists and is active.
	ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*RequirementValidationResult, error)

	// GetPlatformHealth retrieves overall system component health metrics and status.
	GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error)
}
