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

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/naming"
)

// Ensure MemoryStore implements Store interface at compile time.
var _ Store = (*MemoryStore)(nil)

// MemoryStore provides a thread-safe in-memory test double and local implementation of Store.
type MemoryStore struct {
	mu               sync.RWMutex
	componentsByName map[string]*ComponentRecord
	componentsByID   map[int64]*ComponentRecord
	nextID           int64
	snapshots        map[string]map[int64]*SnapshotRecord
}

// NewMemoryStore creates a new thread-safe in-memory component store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		componentsByName: make(map[string]*ComponentRecord),
		componentsByID:   make(map[int64]*ComponentRecord),
		snapshots:        make(map[string]map[int64]*SnapshotRecord),
	}
}

// SetComponentIntent applies or updates component intent within the in-memory store.
func (m *MemoryStore) SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*ComponentRecord, error) {
	return m.setIntent(name, spec, labels, desiredGen, nil)
}

// SetSourcedComponentIntent applies an intent produced by src within the in-memory store.
func (m *MemoryStore) SetSourcedComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, src IntentSource) (*ComponentRecord, error) {
	if err := ValidateIntentSource(src); err != nil {
		return nil, err
	}
	return m.setIntent(name, spec, labels, 0, cloneIntentSource(&src))
}

// setIntent applies an intent ordered by desiredGen when src is nil and by src otherwise.
func (m *MemoryStore) setIntent(name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64, src *IntentSource) (*ComponentRecord, error) {
	canonicalName, err := validateIntentArgs(name, spec)
	if err != nil {
		return nil, err
	}

	deps := ExtractDependencies(spec, labels)
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := validateIntentDependencies(m.componentsByName, canonicalName, deps); err != nil {
		return nil, err
	}

	rec, exists := m.componentsByName[canonicalName]
	if exists && !isDeletedRecord(rec) {
		plan, err := planIntent(canonicalName, rec, spec, labels, deps, desiredGen, src)
		if err != nil {
			return nil, err
		}
		m.applyIntentPlanLocked(rec, plan, spec, labels, deps, src, now)
		return cloneRecord(rec), nil
	}

	m.nextID++
	rec = createNewIntentRecord(m.nextID, canonicalName, spec, labels, deps, newIntentGeneration(desiredGen, src), now)
	rec.Source = src
	m.componentsByName[canonicalName] = rec
	m.componentsByID[rec.ComponentID] = rec
	m.recordSnapshotLocked(rec)

	return cloneRecord(rec), nil
}

// applyIntentPlanLocked applies plan to the existing record rec. A nil src keeps rec.Source.
func (m *MemoryStore) applyIntentPlanLocked(rec *ComponentRecord, plan intentPlan, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, src *IntentSource, now time.Time) {
	switch plan.write {
	case writeSourceOnly:
		rec.Source = src
	case writeSpec:
		updateExistingIntent(rec, spec, labels, deps, plan.generation, now)
		if src != nil {
			rec.Source = src
		}
		m.recordSnapshotLocked(rec)
	}
}

func validateResourceName(name string) (string, error) {
	canonicalName := naming.CanonicalComponentName(name)
	if canonicalName == "" {
		return "", status.Error(codes.InvalidArgument, "component name is required")
	}
	if !naming.IsValidComponentName(canonicalName) {
		return "", status.Errorf(codes.InvalidArgument, "invalid component name %q", name)
	}
	return canonicalName, nil
}

func validateIntentArgs(name string, spec *componentspb.ComponentSpec) (string, error) {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return "", err
	}
	if spec == nil {
		return "", status.Error(codes.InvalidArgument, "component spec is required")
	}
	return canonicalName, nil
}

func updateExistingIntent(rec *ComponentRecord, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, newGen int64, now time.Time) {
	rec.Generation = newGen
	rec.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	rec.Spec = proto.Clone(spec).(*componentspb.ComponentSpec)
	rec.Spec.Generation = newGen
	rec.Dependencies = deps
	rec.Labels = copyMap(labels)
	rec.UpdateTime = now
	rec.Etag = generateEtag(rec)
	if rec.Status == nil {
		rec.Status = &componentspb.ComponentAccessStatus{}
	}
	rec.Status.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
}

func createNewIntentRecord(id int64, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, gen int64, now time.Time) *ComponentRecord {
	specClone := proto.Clone(spec).(*componentspb.ComponentSpec)
	specClone.Generation = gen

	rec := &ComponentRecord{
		ComponentID:  id,
		Name:         canonicalName,
		Spec:         specClone,
		State:        componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Generation:   gen,
		Dependencies: deps,
		Labels:       copyMap(labels),
		CreateTime:   now,
		UpdateTime:   now,
		Status: &componentspb.ComponentAccessStatus{
			State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
	}
	rec.Etag = generateEtag(rec)
	return rec
}

// GetComponent retrieves a component record by canonical or full resource name.
func (m *MemoryStore) GetComponent(ctx context.Context, name string) (*ComponentRecord, error) {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, exists := m.componentsByName[canonicalName]
	if !exists || isDeletedRecord(rec) {
		return nil, ErrComponentNotFound
	}
	return cloneRecord(rec), nil
}

// GetComponentByID retrieves a component record by its unique database identifier.
func (m *MemoryStore) GetComponentByID(ctx context.Context, componentID int64) (*ComponentRecord, error) {
	if componentID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid component ID")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, exists := m.componentsByID[componentID]
	if !exists || isDeletedRecord(rec) {
		return nil, ErrComponentNotFound
	}
	return cloneRecord(rec), nil
}

// ListComponents returns all non-deleted components in the catalog sorted by name.
func (m *MemoryStore) ListComponents(ctx context.Context) ([]*ComponentRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*ComponentRecord
	for _, rec := range m.componentsByName {
		if !isDeletedRecord(rec) {
			results = append(results, cloneRecord(rec))
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results, nil
}

// UpdateObservedGeneration synchronizes actual component access, generation, and state.
func (m *MemoryStore) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string) (*ComponentRecord, error) {
	canonicalName, err := validateUpdateObservedArgs(name, observedGen)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, exists := m.componentsByName[canonicalName]
	if !exists || isDeletedRecord(rec) {
		return nil, ErrComponentNotFound
	}

	rec.ObservedGeneration = &observedGen
	if access != nil {
		rec.Status = proto.Clone(access).(*componentspb.ComponentAccessStatus)
	}
	if state != componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED {
		rec.State = state
	}
	applyStatusUpdates(rec, observedGen, state, statusMsg)

	rec.UpdateTime = time.Now().UTC()
	rec.Etag = generateEtag(rec)
	m.recordSnapshotLocked(rec)
	return cloneRecord(rec), nil
}

func validateUpdateObservedArgs(name string, observedGen int64) (string, error) {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return "", err
	}
	if observedGen < 0 {
		return "", status.Error(codes.InvalidArgument, "observed generation cannot be negative")
	}
	return canonicalName, nil
}

func isDeletedRecord(rec *ComponentRecord) bool {
	if rec == nil {
		return true
	}
	return rec.DeleteTime != nil || rec.State == componentspb.ComponentState_COMPONENT_STATE_DELETED
}

func applyStatusUpdates(rec *ComponentRecord, observedGen int64, state componentspb.ComponentState, statusMsg string) {
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

// PrepareDeactivation checks deactivation safety, transitions the target and any cascaded dependents to DISABLING.
func (m *MemoryStore) PrepareDeactivation(ctx context.Context, name string, requestCascade bool) (*PrepareDeactivationResult, error) {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, exists := m.componentsByName[canonicalName]
	if !exists || rec == nil || isDeletedRecord(rec) {
		return nil, ErrComponentNotFound
	}

	result, err := EvaluateDeactivationSafety(m.componentsByName, canonicalName, requestCascade, minDrainTimeout)
	if err != nil {
		if status.Code(err) == codes.FailedPrecondition && result != nil && len(result.BlockingDependents) > 0 {
			return nil, status.Errorf(codes.FailedPrecondition, "component %s has active dependents: %v", canonicalName, result.BlockingDependents)
		}
		return nil, err
	}

	now := time.Now().UTC()
	transitionToDisabling(rec, now)

	if requestCascade {
		for _, depName := range result.CascadedDeactivations {
			if depRec, ok := m.componentsByName[depName]; ok && depRec != nil && !isDeletedRecord(depRec) {
				transitionToDisabling(depRec, now)
			}
		}
	}

	return result, nil
}

// CompleteDeactivation marks a component in DISABLING state as DISABLED.
func (m *MemoryStore) CompleteDeactivation(ctx context.Context, name string, force bool) error {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, exists := m.componentsByName[canonicalName]
	if !exists || rec == nil || isDeletedRecord(rec) {
		return ErrComponentNotFound
	}

	if rec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		return nil
	}
	if rec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
		return ErrInvalidStateTransition
	}

	if !force {
		activeDependents, _ := findActiveDependents(m.componentsByName, canonicalName)
		if len(activeDependents) > 0 {
			return status.Errorf(codes.FailedPrecondition, "cannot complete deactivation of component %s: active dependents remaining: %v", canonicalName, activeDependents)
		}
	}

	now := time.Now().UTC()
	rec.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
	if rec.Status == nil {
		rec.Status = &componentspb.ComponentAccessStatus{}
	}
	rec.Status.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
	rec.Status.LastTransitionTime = timestamppb.New(now)
	rec.UpdateTime = now
	rec.Etag = generateEtag(rec)
	return nil
}

// ExecuteDeletion deletes a component using the specified deletion policy.
func (m *MemoryStore) ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool) error {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, exists := m.componentsByName[canonicalName]
	if !exists || rec == nil || isDeletedRecord(rec) {
		return ErrComponentNotFound
	}

	if err := ValidateDeletionSafety(m.componentsByName, canonicalName, policy, force); err != nil {
		return err
	}

	now := time.Now().UTC()
	transitionToDeleted(rec, now)
	if policy != componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED && rec.Spec != nil {
		rec.Spec.DeletionPolicy = policy
	}

	deleted := []string{canonicalName}
	// Per api/protos/components/v1alpha1/components.proto (line 44), DELETION_POLICY_UNSPECIFIED
	// defaults to DELETION_POLICY_DELETE (cascading resource deletion across dependents).
	if policy == componentspb.DeletionPolicy_DELETION_POLICY_DELETE || policy == componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED {
		allDependents, _ := findAllDependents(m.componentsByName, canonicalName)
		for _, depName := range allDependents {
			if depRec, ok := m.componentsByName[depName]; ok && depRec != nil && !isDeletedRecord(depRec) {
				transitionToDeleted(depRec, now)
				deleted = append(deleted, depName)
			}
		}
	}
	m.dropEdgesToLocked(deleted)
	return nil
}

// dropEdgesToLocked removes every dependency edge to the soft-deleted components named in deleted.
// SpannerStore stores edges by ComponentId and reads only edges to active components, so an edge to
// a soft-deleted component disappears and never matches a later re-registration of the same name.
// MemoryStore stores edges by name, so it drops them at deletion to keep the same semantics. Like
// SpannerStore, it leaves the specs, UpdateTime, and Etag of the dependents unchanged.
func (m *MemoryStore) dropEdgesToLocked(deleted []string) {
	isDeleted := func(dep string) bool { return slices.Contains(deleted, dep) }
	for _, rec := range m.componentsByID {
		if !slices.ContainsFunc(rec.Dependencies, isDeleted) {
			continue
		}
		// Copy before deleting: snapshots and returned clones may share the backing array.
		rec.Dependencies = slices.DeleteFunc(slices.Clone(rec.Dependencies), isDeleted)
		if len(rec.Dependencies) == 0 {
			rec.Dependencies = nil
		}
	}
}

func transitionToDisabling(rec *ComponentRecord, now time.Time) {
	if rec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLING || rec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		return
	}
	rec.State = componentspb.ComponentState_COMPONENT_STATE_DISABLING
	if rec.Status == nil {
		rec.Status = &componentspb.ComponentAccessStatus{}
	}
	rec.Status.State = componentspb.ComponentState_COMPONENT_STATE_DISABLING
	rec.Status.LastTransitionTime = timestamppb.New(now)
	rec.UpdateTime = now
	rec.Etag = generateEtag(rec)
}

func transitionToDeleted(rec *ComponentRecord, now time.Time) {
	rec.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	if rec.Status == nil {
		rec.Status = &componentspb.ComponentAccessStatus{}
	}
	rec.Status.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	rec.Status.LastTransitionTime = timestamppb.New(now)
	rec.DeleteTime = &now
	rec.UpdateTime = now
	rec.Etag = generateEtag(rec)
}

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
func (m *MemoryStore) ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ResolveComponentSnapshotFromMap(m.componentsByName, m.snapshots, name, generation)
}

// ValidateComponentRequirement checks if a required component type and version exists and is active.
func (m *MemoryStore) ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*RequirementValidationResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return ValidateComponentRequirementFromMap(m.componentsByName, name, reqType, minVersion)
}

// GetPlatformHealth retrieves overall system component health metrics and status.
func (m *MemoryStore) GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return AggregatePlatformHealth(m.componentsByName, filter, pageSize, pageToken)
}

func (m *MemoryStore) recordSnapshotLocked(rec *ComponentRecord) {
	if rec == nil {
		return
	}
	if m.snapshots[rec.Name] == nil {
		m.snapshots[rec.Name] = make(map[int64]*SnapshotRecord)
	}
	var coords *componentspb.ResolvedComponentCoordinates
	if rec.Status != nil && rec.Status.Coordinates != nil {
		coords = proto.Clone(rec.Status.Coordinates).(*componentspb.ResolvedComponentCoordinates)
	}
	m.snapshots[rec.Name][rec.Generation] = &SnapshotRecord{
		Name:        rec.Name,
		Generation:  rec.Generation,
		Record:      cloneRecord(rec),
		Coordinates: coords,
		CreatedAt:   rec.UpdateTime,
	}
}

// cloneRecord performs a deep copy of a ComponentRecord.
func cloneRecord(r *ComponentRecord) *ComponentRecord {
	if r == nil {
		return nil
	}
	cloned := &ComponentRecord{
		ComponentID: r.ComponentID,
		Name:        r.Name,
		State:       r.State,
		Generation:  r.Generation,
		CreateTime:  r.CreateTime,
		UpdateTime:  r.UpdateTime,
		Etag:        r.Etag,
	}
	if r.Spec != nil {
		cloned.Spec = proto.Clone(r.Spec).(*componentspb.ComponentSpec)
	}
	if r.Status != nil {
		cloned.Status = proto.Clone(r.Status).(*componentspb.ComponentAccessStatus)
	}
	if r.ObservedGeneration != nil {
		gen := *r.ObservedGeneration
		cloned.ObservedGeneration = &gen
	}
	if r.DeleteTime != nil {
		t := *r.DeleteTime
		cloned.DeleteTime = &t
	}
	if len(r.Dependencies) > 0 {
		cloned.Dependencies = make([]string, len(r.Dependencies))
		copy(cloned.Dependencies, r.Dependencies)
	}
	if len(r.Labels) > 0 {
		cloned.Labels = copyMap(r.Labels)
	}
	cloned.Source = cloneIntentSource(r.Source)
	return cloned
}

// generateEtag generates a deterministic 16-character hex hash based on record state.
func generateEtag(rec *ComponentRecord) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d:%s:%d:%d", rec.ComponentID, rec.Name, rec.Generation, rec.UpdateTime.UnixNano())
	return hex.EncodeToString(h.Sum(nil)[:8])
}
