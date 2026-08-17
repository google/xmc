// Copyright 2026 Google LLC
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

// Package store provides persistence abstractions and Spanner implementations
// for the XMC Components System.
package store

import (
	"context"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// ComponentRecord represents a component catalog entity stored in Spanner SOT.
type ComponentRecord struct {
	ComponentID        int64
	Name               string
	ComponentClass     string
	ConfigProto        *componentspb.ComponentSpecProto
	AccessPayload      *componentspb.ComponentAccessStatusProto
	State              componentspb.ComponentState
	Generation         int64
	ObservedGeneration *int64
	CreateTime         time.Time
	UpdateTime         time.Time
	ActiveName         *string
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
	ErrorDetail             string
}

// Store defines the storage interface for XMC Components System.
type Store interface {
	// SetComponentIntent applies or updates component intent within a Spanner transaction.
	SetComponentIntent(ctx context.Context, name string, intent *componentspb.ComponentSpecProto, desiredGen int64) (*ComponentRecord, error)

	// GetComponent retrieves a component record by its unique instance name.
	GetComponent(ctx context.Context, name string) (*ComponentRecord, error)

	// GetComponentByID retrieves a component record by its primary key ID.
	GetComponentByID(ctx context.Context, componentID int64) (*ComponentRecord, error)

	// ListComponents returns all registered component records.
	ListComponents(ctx context.Context) ([]*ComponentRecord, error)

	// PrepareDeactivation validates if a component can be safely deactivated without breaking active workloads.
	PrepareDeactivation(ctx context.Context, name string, requestCascade bool) (*PrepareDeactivationResult, error)

	// CompleteDeactivation transitions a component from DISABLING to DISABLED in Spanner.
	CompleteDeactivation(ctx context.Context, name string) error

	// UpdateObservedGeneration updates observed generation and access status in Spanner.
	UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatusProto, statusCond string) error

	// ExecuteDeletion soft-deletes a component row (State = DELETED) to release name for reuse while preserving history.
	ExecuteDeletion(ctx context.Context, name string) error

	// ResolveComponentSnapshot locks and records component snapshots for an admitted experiment at launch time (T0).
	ResolveComponentSnapshot(ctx context.Context, xid int64, requestedClasses []string) ([]*componentspb.ResolvedComponentCoordinates, error)

	// ValidateComponentRequirement checks if a requested component class and parameters are satisfied by active catalog.
	ValidateComponentRequirement(ctx context.Context, requiredClass string, minParams map[string]string) (*RequirementValidationResult, error)

	// GetPlatformHealth computes platform-wide component health telemetry and CDC projection lag.
	GetPlatformHealth(ctx context.Context) (*componentspb.GetPlatformHealthResponse, error)
}
