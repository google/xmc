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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/naming"
)

// Sentinel errors for component store operations. Rejections that carry a google.rpc.ErrorInfo
// (see error_info.go) have no sentinel: callers match their reason.
var (
	ErrComponentNotFound      = status.Error(codes.NotFound, "component not found")
	ErrComponentAlreadyExists = status.Error(codes.AlreadyExists, "component already exists")
	ErrDeletionProtected      = status.Error(codes.FailedPrecondition, "component deletion protection is enabled")
	ErrInvalidGeneration      = status.Error(codes.InvalidArgument, "invalid generation")
	ErrInvalidStateTransition = status.Error(codes.FailedPrecondition, "invalid state transition")
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
	Owner              *SourceOwner // Source of the last owner write, or nil when never written by a source.
	SourceLastSeenTime time.Time    // Timestamp of the last committed owner write.
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

// RequestSource identifies the declarative object (Kubernetes CR) that sent a request. The owner
// of a record is the (cluster, namespace) of the object that last wrote it, and UID is the
// object's incarnation within that owner. Generation orders requests within one UID.
type RequestSource struct {
	UID, Namespace, ClusterUID string
	Generation, Epoch          int64
	// ClusterName is the human-readable name of the source's cluster, used only in messages. It is
	// optional and never recorded.
	ClusterName string
	// ExpectedOwnerUID is a compare-and-swap guard: when it names the owner UID, a source with
	// another UID in the owner's namespace and cluster takes the record over. It is never recorded.
	ExpectedOwnerUID string
}

// SourceOwner is the recorded owner of a component: the source of its last owner write.
type SourceOwner struct {
	UID, Namespace, ClusterUID string
	Generation, Epoch          int64
}

// OwnerOf returns the owner that an owner write from src records.
func OwnerOf(src RequestSource) *SourceOwner {
	return &SourceOwner{UID: src.UID, Generation: src.Generation, Namespace: src.Namespace, ClusterUID: src.ClusterUID, Epoch: src.Epoch}
}

// Records reports whether o already records src. A nil o records nothing.
func (o *SourceOwner) Records(src RequestSource) bool {
	return o != nil && *o == *OwnerOf(src)
}

// IsOlderGeneration reports whether src is an older generation of the owner object o (same UID). A
// nil o has no generations.
func (o *SourceOwner) IsOlderGeneration(src RequestSource) bool {
	return o != nil && o.UID == src.UID && src.Generation < o.Generation
}

// CloneSourceOwner returns a copy of o, or nil for a nil o.
func CloneSourceOwner(o *SourceOwner) *SourceOwner {
	if o == nil {
		return nil
	}
	cp := *o
	return &cp
}

// ClusterIdentity identifies a Kubernetes cluster that writes Component intents: the UID of its
// kube-system Namespace, a human-readable name used only in messages, and its fencing epoch.
type ClusterIdentity struct {
	UID, Name string
	Epoch     int64
}

// DeclarationOutcome is how an accepted DeclareClusterEpoch changed the ownership fence: created,
// refreshed (heartbeat and any changed name), raised, or took over every active owned component.
type DeclarationOutcome int

const (
	DeclarationCreated DeclarationOutcome = iota + 1
	DeclarationRefreshed
	DeclarationRaised
	DeclarationTakeover
)

// DeclarationResult is the result of an accepted DeclareClusterEpoch: the outcome, the resulting
// fence holder, and the number of active components whose owner moved in a takeover.
type DeclarationResult struct {
	Outcome           DeclarationOutcome
	Active            ClusterIdentity
	RecordsReassigned int64
}

// OwnershipGauges is a point-in-time read of the stored state behind the CMS ownership gauges.
type OwnershipGauges struct {
	FenceDeclared     bool      // False before the first DeclareClusterEpoch.
	FenceEpoch        int64     // ComponentOwnershipFence.Epoch.
	FenceLastSeenTime time.Time // ComponentOwnershipFence.LastSeenTime.
	StaleOwners       int64     // Active owned components with SourceLastSeenTime <= now - OwnerStaleThreshold.
}

// OwnershipGaugeReader reads the stored state behind the CMS ownership gauges at now.
type OwnershipGaugeReader interface {
	ReadOwnershipGauges(ctx context.Context, now time.Time) (OwnershipGauges, error)
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
	// dbstore.Store answers an unchanged intent from one strong read, without a read-write
	// transaction, only when all its dependencies are registered and active; committed state never
	// holds a cycle, so the outcome is the same. Changed and new intents pay that extra read before
	// the read-write transaction.
	//
	// Records whose generation already exceeds the caller's generation (inflated by writes made
	// before intents were idempotent) keep the higher number, and later changes bump from it.
	// Recreate such records to realign them. SetComponentIntent leaves the recorded Owner unchanged.
	SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*ComponentRecord, error)

	// SetSourcedComponentIntent applies an intent sent by src (validated by ValidateRequestSource),
	// ordered by src instead of by generation. src must come from the cluster that declared the
	// ownership fence, at the fence epoch (checkClusterFence: CLUSTER_NOT_DECLARED,
	// CLUSTER_EPOCH_REGRESSED, CLUSTER_FENCED).
	// A new component starts at generation 1; on an existing one, src must pass checkSourceOwnership
	// (SOURCE_NAMESPACE_CONFLICT, SOURCE_INCARNATION_MISMATCH) before planSourcedIntent decides
	// whether the intent is stale (STALE_GENERATION), a no-op, an owner-only write, or a spec write
	// (+1 generation). Every write records src as owner.
	SetSourcedComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, src RequestSource) (*ComponentRecord, error)

	// DeclareClusterEpoch makes cluster (validated by ValidateClusterIdentity) the holder of the
	// ownership fence at its epoch (see planDeclaration) and reassigns active owned components on
	// a takeover. Rejections are FailedPrecondition (CLUSTER_EPOCH_REGRESSED, CLUSTER_FENCED).
	DeclareClusterEpoch(ctx context.Context, cluster ClusterIdentity) (*DeclarationResult, error)

	// GetComponent retrieves a component record by canonical or full resource name.
	GetComponent(ctx context.Context, name string) (*ComponentRecord, error)

	// GetComponentByID retrieves a component record by its unique database identifier.
	GetComponentByID(ctx context.Context, componentID int64) (*ComponentRecord, error)

	// ListComponents returns all non-deleted components in the catalog.
	ListComponents(ctx context.Context) ([]*ComponentRecord, error)

	// PrepareDeactivation checks deactivation safety and cascading requirements.
	PrepareDeactivation(ctx context.Context, name string, requestCascade bool, src ...RequestSource) (*PrepareDeactivationResult, error)

	// CompleteDeactivation marks a component in DISABLING state as DISABLED.
	CompleteDeactivation(ctx context.Context, name string, force bool, src ...RequestSource) error

	// UpdateObservedGeneration synchronizes actual component access and state, returning the updated record.
	UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string, src ...RequestSource) (*ComponentRecord, error)

	// ExecuteDeletion deletes a component using the specified deletion policy.
	ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool, src ...RequestSource) error

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
		Name:        naming.FullComponentName(r.Name),
		Etag:        r.Etag,
		ComponentId: r.ComponentID,
	}
	if r.Spec != nil {
		comp.Spec = proto.Clone(r.Spec).(*componentspb.ComponentSpec)
	}
	comp.Status = buildProtoStatus(r)
	populateProtoTimestamps(comp, r)
	if len(r.Labels) > 0 {
		comp.Labels = CopyMap(r.Labels)
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

// CopyMap creates a shallow copy of a string key-value map.
func CopyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// IsDeletedRecord reports whether rec is nil, in DELETED state, or has a DeleteTime set.
func IsDeletedRecord(rec *ComponentRecord) bool {
	return rec == nil || rec.State == componentspb.ComponentState_COMPONENT_STATE_DELETED || rec.DeleteTime != nil
}

// CloneRecord returns a deep copy of r, or nil when r is nil.
func CloneRecord(r *ComponentRecord) *ComponentRecord {
	if r == nil {
		return nil
	}
	cp := *r
	if r.Spec != nil {
		cp.Spec = proto.Clone(r.Spec).(*componentspb.ComponentSpec)
	}
	if r.Status != nil {
		cp.Status = proto.Clone(r.Status).(*componentspb.ComponentAccessStatus)
	}
	if r.ObservedGeneration != nil {
		gen := *r.ObservedGeneration
		cp.ObservedGeneration = &gen
	}
	if r.DeleteTime != nil {
		dt := *r.DeleteTime
		cp.DeleteTime = &dt
	}
	cp.Dependencies = nil
	if len(r.Dependencies) > 0 {
		cp.Dependencies = make([]string, len(r.Dependencies))
		copy(cp.Dependencies, r.Dependencies)
	}
	cp.Labels = nil
	if len(r.Labels) > 0 {
		cp.Labels = CopyMap(r.Labels)
	}
	cp.Owner = CloneSourceOwner(r.Owner)
	return &cp
}

// CloneCoordinates returns a deep copy of coords, or nil when coords is nil.
func CloneCoordinates(coords *componentspb.ResolvedComponentCoordinates) *componentspb.ResolvedComponentCoordinates {
	if coords == nil {
		return nil
	}
	return proto.Clone(coords).(*componentspb.ResolvedComponentCoordinates)
}

// GenerateEtag generates a deterministic 16-character hex hash based on record state.
func GenerateEtag(rec *ComponentRecord) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d:%s:%d:%d", rec.ComponentID, rec.Name, rec.Generation, rec.UpdateTime.UnixNano())
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// ValidateResourceName validates and returns the canonical component name of name.
func ValidateResourceName(name string) (string, error) {
	canonicalName := naming.CanonicalComponentName(name)
	if canonicalName == "" {
		return "", status.Error(codes.InvalidArgument, "component name is required")
	}
	if !naming.IsValidComponentName(canonicalName) {
		return "", status.Errorf(codes.InvalidArgument, "invalid component name %q", name)
	}
	return canonicalName, nil
}

// ValidateIntentArgs validates the component name and spec of an intent request.
func ValidateIntentArgs(name string, spec *componentspb.ComponentSpec) (string, error) {
	canonicalName, err := ValidateResourceName(name)
	if err != nil {
		return "", err
	}
	if spec == nil {
		return "", status.Error(codes.InvalidArgument, "component spec is required")
	}
	return canonicalName, nil
}

// ApplyObservation applies an UpdateObservedGeneration of observedGen, access, state and
// statusMsg to rec, and reports whether it changed the observed generation, state or status.
func ApplyObservation(rec *ComponentRecord, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string) bool {
	prevObserved, prevState := rec.ObservedGeneration, rec.State
	var prevStatus *componentspb.ComponentAccessStatus
	if rec.Status != nil {
		prevStatus = proto.Clone(rec.Status).(*componentspb.ComponentAccessStatus)
	}

	rec.ObservedGeneration = &observedGen
	if access != nil {
		rec.Status = proto.Clone(access).(*componentspb.ComponentAccessStatus)
	}
	if state != componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED {
		rec.State = state
	}
	ApplyStatusUpdates(rec, observedGen, state, statusMsg)

	return prevObserved == nil || *prevObserved != observedGen || prevState != rec.State || !proto.Equal(prevStatus, rec.Status)
}

// ApplyStatusUpdates mutates rec.Status in place with observedGen, state, and statusMsg.
func ApplyStatusUpdates(rec *ComponentRecord, observedGen int64, state componentspb.ComponentState, statusMsg string) {
	if rec.Status == nil && (observedGen > 0 || statusMsg != "" || state != componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED) {
		rec.Status = &componentspb.ComponentAccessStatus{}
	}
	if rec.Status == nil {
		return
	}
	rec.Status.ObservedGeneration = observedGen
	if state != componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED {
		rec.Status.State = state
	}
	if statusMsg != "" {
		rec.Status.HealthMessage = statusMsg
	}
}
