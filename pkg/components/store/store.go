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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/naming"
)

// Sentinel errors for component store operations.
var (
	ErrComponentNotFound      = status.Error(codes.NotFound, "component not found")
	ErrComponentAlreadyExists = status.Error(codes.AlreadyExists, "component already exists")
	ErrDeletionProtected      = status.Error(codes.FailedPrecondition, "component deletion protection is enabled")
	ErrHasActiveDependents    = status.Error(codes.FailedPrecondition, "component has active dependents")
	ErrInvalidGeneration      = status.Error(codes.InvalidArgument, "invalid generation")
	ErrInvalidStateTransition = status.Error(codes.FailedPrecondition, "invalid state transition")
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
	// Source identifies the declarative object whose intent was last adopted, or is nil when no
	// sourced intent has been applied.
	Source *IntentSource
}

// IntentSource identifies the declarative object, such as a Kubernetes custom resource, that
// produced an intent. UID and CreateTime identify its epoch: a recreated object has a new UID and a
// later CreateTime. Generation is the object's own generation, which is independent of the
// component generation.
type IntentSource struct {
	UID        string
	CreateTime time.Time
	Generation int64
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
	//
	// A new component starts at max(desiredGen, 1). For an existing component, a changed intent
	// sets the generation to desiredGen if it is newer, else to the current generation + 1, and
	// resets the state to COMPONENT_STATE_PENDING_RECONCILIATION.
	//
	// An intent is unchanged when desiredGen is not newer than the current generation (0 means
	// unspecified) and it has equal labels, dependencies, and spec (ignoring spec.generation). An
	// unchanged intent is a no-op: the existing record is returned without a generation bump,
	// snapshot, UpdateTime write, or re-queue, and its state and status are not reset (state is
	// driven by UpdateObservedGeneration). Validation still runs first, so dependency cycles and
	// unregistered or soft-deleted dependencies are reported even for an unchanged intent; the
	// latter as FailedPrecondition with a message starting with DependencyNotFoundMessagePrefix.
	// SpannerStore answers an unchanged intent from one strong read, without a read-write
	// transaction, only when all its dependencies are registered and active; committed state never
	// holds a cycle, so the outcome is the same. Changed and new intents pay that extra read before
	// the read-write transaction.
	//
	// Records whose generation already exceeds the caller's generation (inflated by writes made
	// before intents were idempotent) keep the higher number, and later changes bump from it.
	// Callers that can identify their source should use SetSourcedComponentIntent, which does not
	// compare against desiredGen. SetComponentIntent leaves the recorded Source unchanged.
	SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*ComponentRecord, error)

	// SetSourcedComponentIntent applies an intent produced by src, ordering intents by src instead
	// of by generation. Validation, the definition of an unchanged intent (ignoring generations),
	// and the SpannerStore read-only fast path are as for SetComponentIntent.
	//
	// A new component starts at generation 1. For an existing component the intent is classified
	// against the recorded Source:
	//   - Same UID: the intent is current unless src.Generation is older than the recorded one,
	//     in which case a changed intent is stale and an unchanged one is a no-op.
	//   - Different UID, or no recorded Source: the intent is adopted.
	//
	// A current or adopted intent that changes the record bumps the generation by exactly 1,
	// resets the state as for SetComponentIntent, and records src. An unchanged one records src,
	// without a generation bump, snapshot, UpdateTime write, or state reset, when src differs from
	// the recorded Source, and is a no-op otherwise. A stale intent fails with FailedPrecondition
	// and a message starting with StaleIntentMessagePrefix, leaving the record unchanged. src must
	// have a non-empty UID, a non-zero CreateTime, and a positive Generation, else InvalidArgument.
	SetSourcedComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, src IntentSource) (*ComponentRecord, error)

	// GetComponent retrieves a component record by canonical or full resource name.
	GetComponent(ctx context.Context, name string) (*ComponentRecord, error)

	// GetComponentByID retrieves a component record by its unique database identifier.
	GetComponentByID(ctx context.Context, componentID int64) (*ComponentRecord, error)

	// ListComponents returns all non-deleted components in the catalog.
	ListComponents(ctx context.Context) ([]*ComponentRecord, error)

	// PrepareDeactivation checks deactivation safety and cascading requirements.
	PrepareDeactivation(ctx context.Context, name string, requestCascade bool) (*PrepareDeactivationResult, error)

	// CompleteDeactivation marks a component in DISABLING state as DISABLED.
	CompleteDeactivation(ctx context.Context, name string, force bool) error

	// UpdateObservedGeneration synchronizes actual component access and state, returning the updated record.
	UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string) (*ComponentRecord, error)

	// ExecuteDeletion deletes a component using the specified deletion policy.
	ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool) error

	// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
	ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*ComponentRecord, *componentspb.ResolvedComponentCoordinates, error)

	// ValidateComponentRequirement checks if a required component type and version exists and is active.
	ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*RequirementValidationResult, error)

	// GetPlatformHealth retrieves overall system component health metrics and status.
	// PageSize defaults to 50 when <= 0, and is clamped to a maximum of 1000 per AIP-158.
	GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error)
}

// ToProtoComponent converts a ComponentRecord to a public Component protobuf message.
func ToProtoComponent(r *ComponentRecord) *componentspb.Component {
	if r == nil {
		return nil
	}
	comp := &componentspb.Component{
		Name: naming.FullComponentName(r.Name),
		Etag: r.Etag,
	}
	if r.Spec != nil {
		comp.Spec = proto.Clone(r.Spec).(*componentspb.ComponentSpec)
	}
	comp.Status = buildProtoStatus(r)
	populateProtoTimestamps(comp, r)
	if len(r.Labels) > 0 {
		comp.Labels = copyMap(r.Labels)
	}
	return comp
}

// buildProtoStatus constructs the ComponentAccessStatus proto from a record.
func buildProtoStatus(r *ComponentRecord) *componentspb.ComponentAccessStatus {
	if r.Status != nil {
		return mergeExistingStatus(r)
	}
	if r.State == componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED && r.ObservedGeneration == nil {
		return nil
	}
	status := &componentspb.ComponentAccessStatus{
		State: r.State,
	}
	if r.ObservedGeneration != nil {
		status.ObservedGeneration = *r.ObservedGeneration
	}
	return status
}

// mergeExistingStatus clones and merges top-level record state/generation into status.
func mergeExistingStatus(r *ComponentRecord) *componentspb.ComponentAccessStatus {
	status := proto.Clone(r.Status).(*componentspb.ComponentAccessStatus)
	if r.State != componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED && status.GetState() == componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED {
		status.State = r.State
	}
	if r.ObservedGeneration != nil && status.GetObservedGeneration() == 0 {
		status.ObservedGeneration = *r.ObservedGeneration
	}
	return status
}

// populateProtoTimestamps copies non-zero record timestamps to the component proto.
func populateProtoTimestamps(comp *componentspb.Component, r *ComponentRecord) {
	if !r.CreateTime.IsZero() {
		comp.CreateTime = timestamppb.New(r.CreateTime)
	}
	if !r.UpdateTime.IsZero() {
		comp.UpdateTime = timestamppb.New(r.UpdateTime)
	}
	if r.DeleteTime != nil && !r.DeleteTime.IsZero() {
		comp.DeleteTime = timestamppb.New(*r.DeleteTime)
	}
}

// copyMap creates a shallow copy of a string key-value map.
func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
