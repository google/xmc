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
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
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
	canonicalName, err := validateIntentArgs(name, spec)
	if err != nil {
		return nil, err
	}

	deps := ExtractDependencies(spec, labels, canonicalName)
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	records := make([]*ComponentRecord, 0, len(m.componentsByName))
	for _, r := range m.componentsByName {
		records = append(records, r)
	}
	if err := ValidateProspectiveDependencies(records, canonicalName, deps); err != nil {
		return nil, err
	}

	rec, exists := m.componentsByName[canonicalName]
	if exists && !isDeletedRecord(rec) {
		updateExistingIntent(rec, spec, labels, deps, desiredGen, now)
		return cloneRecord(rec), nil
	}

	m.nextID++
	rec = createNewIntentRecord(m.nextID, canonicalName, spec, labels, deps, desiredGen, now)
	m.componentsByName[canonicalName] = rec
	m.componentsByID[rec.ComponentID] = rec

	return cloneRecord(rec), nil
}

func validateResourceName(name string) (string, error) {
	canonicalName := CanonicalComponentName(name)
	if canonicalName == "" {
		return "", status.Error(codes.InvalidArgument, "component name is required")
	}
	if !IsValidComponentName(canonicalName) {
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

func updateExistingIntent(rec *ComponentRecord, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64, now time.Time) {
	newGen := rec.Generation + 1
	if desiredGen > rec.Generation {
		newGen = desiredGen
	}
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

func createNewIntentRecord(id int64, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64, now time.Time) *ComponentRecord {
	gen := int64(1)
	if desiredGen > 1 {
		gen = desiredGen
	}

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
func (m *MemoryStore) CompleteDeactivation(ctx context.Context, name string) error {
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

	if rec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
		return ErrInvalidStateTransition
	}

	activeDependents, _ := findActiveDependents(m.componentsByName, canonicalName)
	if len(activeDependents) > 0 {
		return status.Errorf(codes.FailedPrecondition, "cannot complete deactivation of component %s: active dependents remaining: %v", canonicalName, activeDependents)
	}

	now := time.Now().UTC()
	rec.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
	if rec.Status == nil {
		rec.Status = &componentspb.ComponentAccessStatus{}
	}
	rec.Status.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
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

	if policy == componentspb.DeletionPolicy_DELETION_POLICY_DELETE {
		activeDependents, _ := findActiveDependents(m.componentsByName, canonicalName)
		for _, depName := range activeDependents {
			if depRec, ok := m.componentsByName[depName]; ok && depRec != nil && !isDeletedRecord(depRec) {
				transitionToDeleted(depRec, now)
			}
		}
	}
	return nil
}

func transitionToDisabling(rec *ComponentRecord, now time.Time) {
	rec.State = componentspb.ComponentState_COMPONENT_STATE_DISABLING
	if rec.Status == nil {
		rec.Status = &componentspb.ComponentAccessStatus{}
	}
	rec.Status.State = componentspb.ComponentState_COMPONENT_STATE_DISABLING
	rec.UpdateTime = now
	rec.Etag = generateEtag(rec)
}

func transitionToDeleted(rec *ComponentRecord, now time.Time) {
	rec.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	if rec.Status == nil {
		rec.Status = &componentspb.ComponentAccessStatus{}
	}
	rec.Status.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	rec.DeleteTime = &now
	rec.UpdateTime = now
	rec.Etag = generateEtag(rec)
}

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
// Note: To be implemented in subsequent snapshot resolution tasks.
func (m *MemoryStore) ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	return nil, nil, status.Error(codes.Unimplemented, "method ResolveComponentSnapshot not implemented in MemoryStore")
}

// ValidateComponentRequirement checks if a required component type and version exists and is active.
// Note: To be implemented in subsequent dependency validation tasks.
func (m *MemoryStore) ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*RequirementValidationResult, error) {
	return nil, status.Error(codes.Unimplemented, "method ValidateComponentRequirement not implemented in MemoryStore")
}

// GetPlatformHealth retrieves overall system component health metrics and status.
// Note: To be implemented in subsequent platform health monitoring tasks.
func (m *MemoryStore) GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetPlatformHealth not implemented in MemoryStore")
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
	return cloned
}

// generateEtag generates a deterministic 16-character hex hash based on record state.
func generateEtag(rec *ComponentRecord) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d:%s:%d:%d", rec.ComponentID, rec.Name, rec.Generation, rec.UpdateTime.UnixNano())
	return hex.EncodeToString(h.Sum(nil)[:8])
}
