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

// Package memory provides an in-memory implementation of store.Store.
package memory

import (
	"context"
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
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dependency"
	"xmanager_cloud/pkg/components/store/lifecycle"
	"xmanager_cloud/pkg/components/store/ownership"
)

// Ensure MemoryStore implements Store interface at compile time.
var _ store.Store = (*MemoryStore)(nil)

var _ store.OwnershipGaugeReader = (*MemoryStore)(nil)

// MemoryStore provides a thread-safe in-memory test double and local implementation of Store.
type MemoryStore struct {
	mu               sync.RWMutex
	componentsByName map[string]*store.ComponentRecord
	componentsByID   map[int64]*store.ComponentRecord
	nextID           int64
	snapshots        map[string]map[int64]*store.SnapshotRecord
	fence            *store.ClusterIdentity // Ownership fence holder; nil before the first declaration.
	fenceLastSeen    time.Time              // Time of the last accepted declaration.
}

// NewMemoryStore creates a new thread-safe in-memory component store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		componentsByName: make(map[string]*store.ComponentRecord),
		componentsByID:   make(map[int64]*store.ComponentRecord),
		snapshots:        make(map[string]map[int64]*store.SnapshotRecord),
	}
}

// SetComponentIntent applies or updates component intent within the in-memory store.
func (m *MemoryStore) SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*store.ComponentRecord, error) {
	return m.setIntent(name, spec, labels, desiredGen, nil)
}

// SetSourcedComponentIntent applies an intent sent by src within the in-memory store.
func (m *MemoryStore) SetSourcedComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, src store.RequestSource) (*store.ComponentRecord, error) {
	if err := ownership.ValidateRequestSource(src); err != nil {
		return nil, err
	}
	return m.setIntent(name, spec, labels, 0, &src)
}

// setIntent applies an intent ordered by desiredGen when src is nil and by src otherwise.
func (m *MemoryStore) setIntent(name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64, src *store.RequestSource) (*store.ComponentRecord, error) {
	canonicalName, err := store.ValidateIntentArgs(name, spec)
	if err != nil {
		return nil, err
	}

	deps := dependency.ExtractDependencies(spec, labels)
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := ownership.CheckClusterFence(store.OperationSetComponentIntent, m.fence, src); err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	rec, plan, err := ownership.PlanLiveIntent(m.componentsByName, canonicalName, spec, labels, deps, desiredGen, src)
	if err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	if _, err := dependency.ValidateIntentDependencies(m.componentsByName, canonicalName, deps); err != nil {
		return nil, err
	}
	if rec != nil {
		m.applyIntentPlanLocked(rec, plan, spec, labels, deps, src, now)
		if plan.Write != ownership.WriteNothing {
			ownership.RecordOwnerAdoption(plan.PrevOwner, src, false)
		}
		return store.CloneRecord(rec), nil
	}

	m.nextID++
	rec = createNewIntentRecord(m.nextID, canonicalName, spec, labels, deps, plan.Generation, now)
	if src != nil {
		rec.Owner = store.OwnerOf(*src)
		rec.SourceLastSeenTime = now
	}
	m.componentsByName[canonicalName] = rec
	m.componentsByID[rec.ComponentID] = rec
	m.recordSnapshotLocked(rec)
	ownership.RecordOwnerAdoption(nil, src, true)

	return store.CloneRecord(rec), nil
}

// applyIntentPlanLocked applies plan to the existing record rec. A nil src keeps rec.Owner.
func (m *MemoryStore) applyIntentPlanLocked(rec *store.ComponentRecord, plan ownership.IntentPlan, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, src *store.RequestSource, now time.Time) {
	if plan.Write == ownership.WriteNothing {
		return
	}
	if src != nil {
		rec.Owner = store.OwnerOf(*src)
		rec.SourceLastSeenTime = now
	}
	if plan.Write == ownership.WriteSpec {
		updateExistingIntent(rec, spec, labels, deps, plan.Generation, now)
		m.recordSnapshotLocked(rec)
	}
}

// DeclareClusterEpoch declares cluster the writer of sourced intents within the in-memory store.
func (m *MemoryStore) DeclareClusterEpoch(ctx context.Context, cluster store.ClusterIdentity) (*store.DeclarationResult, error) {
	if err := ownership.ValidateClusterIdentity(cluster); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	outcome, err := ownership.PlanDeclaration(m.fence, cluster)
	if err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	res := &store.DeclarationResult{Outcome: outcome, Active: cluster}
	now := time.Now().UTC()
	if outcome == store.DeclarationTakeover {
		for _, rec := range m.componentsByName {
			if !store.IsDeletedRecord(rec) && rec.Owner != nil {
				rec.Owner = ownership.TakenOverOwner(rec.Owner, cluster)
				// Starts the adoption grace period of the owner_stale gauge, as in Spanner.
				rec.SourceLastSeenTime = now
				res.RecordsReassigned++
			}
		}
	}
	m.fence = &cluster
	m.fenceLastSeen = now
	return res, nil
}

// ReadOwnershipGauges reads the fence and counts the stale owners in the in-memory store at now.
func (m *MemoryStore) ReadOwnershipGauges(ctx context.Context, now time.Time) (store.OwnershipGauges, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	g := store.OwnershipGauges{StaleOwners: ownership.CountStaleOwnedComponents(m.componentsByName, now)}
	if m.fence != nil {
		g.FenceDeclared, g.FenceEpoch, g.FenceLastSeenTime = true, m.fence.Epoch, m.fenceLastSeen
	}
	return g, nil
}

func updateExistingIntent(rec *store.ComponentRecord, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, newGen int64, now time.Time) {
	rec.Generation = newGen
	rec.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	rec.Spec = proto.Clone(spec).(*componentspb.ComponentSpec)
	rec.Spec.Generation = newGen
	rec.Dependencies = deps
	rec.Labels = store.CopyMap(labels)
	rec.UpdateTime = now
	rec.Etag = store.GenerateEtag(rec)
	if rec.Status == nil {
		rec.Status = &componentspb.ComponentAccessStatus{}
	}
	rec.Status.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
}

func createNewIntentRecord(id int64, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, gen int64, now time.Time) *store.ComponentRecord {
	specClone := proto.Clone(spec).(*componentspb.ComponentSpec)
	specClone.Generation = gen

	rec := &store.ComponentRecord{
		ComponentID:  id,
		Name:         canonicalName,
		Spec:         specClone,
		State:        componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Generation:   gen,
		Dependencies: deps,
		Labels:       store.CopyMap(labels),
		CreateTime:   now,
		UpdateTime:   now,
		Status: &componentspb.ComponentAccessStatus{
			State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
	}
	rec.Etag = store.GenerateEtag(rec)
	return rec
}

// GetComponent retrieves a component record by canonical or full resource name.
func (m *MemoryStore) GetComponent(ctx context.Context, name string) (*store.ComponentRecord, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, exists := m.componentsByName[canonicalName]
	if !exists || store.IsDeletedRecord(rec) {
		return nil, store.ErrComponentNotFound
	}
	return store.CloneRecord(rec), nil
}

// GetComponentByID retrieves a component record by its unique database identifier.
func (m *MemoryStore) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	if componentID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid component ID")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, exists := m.componentsByID[componentID]
	if !exists || store.IsDeletedRecord(rec) {
		return nil, store.ErrComponentNotFound
	}
	return store.CloneRecord(rec), nil
}

// ListComponents returns all non-deleted components in the catalog sorted by name.
func (m *MemoryStore) ListComponents(ctx context.Context) ([]*store.ComponentRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*store.ComponentRecord
	for _, rec := range m.componentsByName {
		if !store.IsDeletedRecord(rec) {
			results = append(results, store.CloneRecord(rec))
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results, nil
}

// UpdateObservedGeneration synchronizes actual component access, generation, and state.
func (m *MemoryStore) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string, src ...store.RequestSource) (*store.ComponentRecord, error) {
	canonicalName, reqSrc, err := ownership.ValidateUpdateObservedArgs(name, observedGen, src)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, err := m.lookupMutableRecordLocked(store.OperationUpdateObservedGeneration, canonicalName, reqSrc)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	recordOwnerInMemory(rec, reqSrc, now)
	if !store.ApplyObservation(rec, observedGen, access, state, statusMsg) {
		// An unchanged heartbeat records only the owner: UpdateTime and the snapshot stay.
		return store.CloneRecord(rec), nil
	}

	rec.UpdateTime = now
	rec.Etag = store.GenerateEtag(rec)
	m.recordSnapshotLocked(rec)
	return store.CloneRecord(rec), nil
}

// recordOwnerInMemory records src as the owner of rec at now (preserving Generation when already
// owned by src.UID) and counts any owner adoption.
func recordOwnerInMemory(rec *store.ComponentRecord, src *store.RequestSource, now time.Time) {
	if src == nil {
		return
	}
	prev := store.CloneSourceOwner(rec.Owner)
	rec.Owner = ownership.NonIntentOwner(rec.Owner, src)
	rec.SourceLastSeenTime = now
	ownership.RecordOwnerAdoption(prev, src, false)
}

// lookupMutableRecordLocked checks the cluster fence, finds the active record canonicalName, and
// checks source ownership for operation.
func (m *MemoryStore) lookupMutableRecordLocked(operation, canonicalName string, src *store.RequestSource) (*store.ComponentRecord, error) {
	if err := ownership.CheckClusterFence(operation, m.fence, src); err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	rec, exists := m.componentsByName[canonicalName]
	if !exists || rec == nil || store.IsDeletedRecord(rec) {
		return nil, store.ErrComponentNotFound
	}
	if err := ownership.CheckSourceMutation(operation, canonicalName, rec.Owner, src); err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	return rec, nil
}

// PrepareDeactivation checks deactivation safety, transitions the target and any cascaded dependents to DISABLING.
func (m *MemoryStore) PrepareDeactivation(ctx context.Context, name string, requestCascade bool, src ...store.RequestSource) (*store.PrepareDeactivationResult, error) {
	canonicalName, reqSrc, err := ownership.ValidateSourcedResourceArgs(name, src)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, err := m.lookupMutableRecordLocked(store.OperationPrepareDeactivation, canonicalName, reqSrc)
	if err != nil {
		return nil, err
	}

	result, err := lifecycle.EvaluateDeactivationSafety(m.componentsByName, canonicalName, requestCascade, lifecycle.MinDrainTimeout)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	recordOwnerInMemory(rec, reqSrc, now)
	transitionToDisabling(rec, now)

	if requestCascade {
		for _, depName := range result.CascadedDeactivations {
			if depRec, ok := m.componentsByName[depName]; ok && depRec != nil && !store.IsDeletedRecord(depRec) {
				transitionToDisabling(depRec, now)
			}
		}
	}

	return result, nil
}

// CompleteDeactivation marks a component in DISABLING state as DISABLED.
func (m *MemoryStore) CompleteDeactivation(ctx context.Context, name string, force bool, src ...store.RequestSource) error {
	canonicalName, reqSrc, err := ownership.ValidateSourcedResourceArgs(name, src)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, err := m.lookupMutableRecordLocked(store.OperationCompleteDeactivation, canonicalName, reqSrc)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	if rec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		recordOwnerInMemory(rec, reqSrc, now)
		return nil
	}
	if rec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
		return store.ErrInvalidStateTransition
	}

	if !force {
		activeDependents, _ := lifecycle.FindActiveDependents(m.componentsByName, canonicalName)
		if len(activeDependents) > 0 {
			return status.Errorf(codes.FailedPrecondition, "cannot complete deactivation of component %s: active dependents remaining: %v", canonicalName, activeDependents)
		}
	}
	recordOwnerInMemory(rec, reqSrc, now)
	rec.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
	if rec.Status == nil {
		rec.Status = &componentspb.ComponentAccessStatus{}
	}
	rec.Status.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
	rec.Status.LastTransitionTime = timestamppb.New(now)
	rec.UpdateTime = now
	rec.Etag = store.GenerateEtag(rec)
	return nil
}

// ExecuteDeletion deletes a component using the specified deletion policy.
func (m *MemoryStore) ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool, src ...store.RequestSource) error {
	canonicalName, reqSrc, err := ownership.ValidateSourcedResourceArgs(name, src)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, err := m.lookupMutableRecordLocked(store.OperationExecuteDeletion, canonicalName, reqSrc)
	if err != nil {
		return err
	}

	if err := lifecycle.ValidateDeletionSafety(m.componentsByName, canonicalName, policy, force); err != nil {
		return err
	}
	now := time.Now().UTC()
	if reqSrc != nil {
		// Like recordOwnerInMemory, but with DeletionOwner: the deleting owner object keeps the
		// larger SourceGeneration, matching SpannerStore.
		prev := store.CloneSourceOwner(rec.Owner)
		rec.Owner = ownership.DeletionOwner(rec.Owner, *reqSrc)
		rec.SourceLastSeenTime = now
		ownership.RecordOwnerAdoption(prev, reqSrc, false)
	}
	transitionToDeleted(rec, now)
	if policy != componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED && rec.Spec != nil {
		rec.Spec.DeletionPolicy = policy
	}

	deleted := []string{canonicalName}
	// Per api/protos/components/v1alpha1/components.proto (line 44), DELETION_POLICY_UNSPECIFIED
	// defaults to DELETION_POLICY_DELETE (cascading resource deletion across dependents).
	if policy == componentspb.DeletionPolicy_DELETION_POLICY_DELETE || policy == componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED {
		allDependents, _ := lifecycle.FindAllDependents(m.componentsByName, canonicalName)
		for _, depName := range allDependents {
			if depRec, ok := m.componentsByName[depName]; ok && depRec != nil && !store.IsDeletedRecord(depRec) {
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

func transitionToDisabling(rec *store.ComponentRecord, now time.Time) {
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
	rec.Etag = store.GenerateEtag(rec)
}

func transitionToDeleted(rec *store.ComponentRecord, now time.Time) {
	rec.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	if rec.Status == nil {
		rec.Status = &componentspb.ComponentAccessStatus{}
	}
	rec.Status.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	rec.Status.LastTransitionTime = timestamppb.New(now)
	rec.DeleteTime = &now
	rec.UpdateTime = now
	rec.Etag = store.GenerateEtag(rec)
}

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
func (m *MemoryStore) ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return lifecycle.ResolveComponentSnapshotFromMap(m.componentsByName, m.snapshots, name, generation)
}

// ValidateComponentRequirement checks if a required component type and version exists and is active.
func (m *MemoryStore) ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*store.RequirementValidationResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return lifecycle.ValidateComponentRequirementFromMap(m.componentsByName, name, reqType, minVersion)
}

// GetPlatformHealth retrieves overall system component health metrics and status.
func (m *MemoryStore) GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return lifecycle.AggregatePlatformHealth(m.componentsByName, filter, pageSize, pageToken)
}

func (m *MemoryStore) recordSnapshotLocked(rec *store.ComponentRecord) {
	if rec == nil {
		return
	}
	if m.snapshots[rec.Name] == nil {
		m.snapshots[rec.Name] = make(map[int64]*store.SnapshotRecord)
	}
	var coords *componentspb.ResolvedComponentCoordinates
	if rec.Status != nil && rec.Status.Coordinates != nil {
		coords = proto.Clone(rec.Status.Coordinates).(*componentspb.ResolvedComponentCoordinates)
	}
	m.snapshots[rec.Name][rec.Generation] = &store.SnapshotRecord{
		Name:        rec.Name,
		Generation:  rec.Generation,
		Record:      store.CloneRecord(rec),
		Coordinates: coords,
		CreatedAt:   rec.UpdateTime,
	}
}

// OwnerByIDForTest returns the recorded SourceOwner of componentID, including on a soft-deleted
// row, or an error when the row has no recorded owner.
func (m *MemoryStore) OwnerByIDForTest(componentID int64) (*store.SourceOwner, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.componentsByID[componentID]
	if !ok || rec.Owner == nil {
		return nil, fmt.Errorf("component %d has no recorded owner", componentID)
	}
	return store.CloneSourceOwner(rec.Owner), nil
}

// SetSourceLastSeenTimeForTest overwrites the SourceLastSeenTime of canonicalName in tests.
func (m *MemoryStore) SetSourceLastSeenTimeForTest(canonicalName string, seenAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec := m.componentsByName[canonicalName]; rec != nil {
		rec.SourceLastSeenTime = seenAt
	}
}
