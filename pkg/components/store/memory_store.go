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
	canonicalName := CanonicalComponentName(name)
	if canonicalName == "" {
		return nil, status.Error(codes.InvalidArgument, "component name is required")
	}
	if !IsValidComponentName(canonicalName) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid component name %q", name)
	}
	if spec == nil {
		return nil, status.Error(codes.InvalidArgument, "component spec is required")
	}

	deps := ExtractDependencies(spec, labels, canonicalName)
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, exists := m.componentsByName[canonicalName]
	if exists && rec.DeleteTime == nil && rec.State != componentspb.ComponentState_COMPONENT_STATE_DELETED {
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
		return cloneRecord(rec), nil
	}

	m.nextID++
	gen := int64(1)
	if desiredGen > 1 {
		gen = desiredGen
	}

	specClone := proto.Clone(spec).(*componentspb.ComponentSpec)
	specClone.Generation = gen

	rec = &ComponentRecord{
		ComponentID:  m.nextID,
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

	m.componentsByName[canonicalName] = rec
	m.componentsByID[rec.ComponentID] = rec

	return cloneRecord(rec), nil
}

// GetComponent retrieves a component record by canonical or full resource name (stub in Layer 1).
func (m *MemoryStore) GetComponent(ctx context.Context, name string) (*ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "method GetComponent not implemented in MemoryStore")
}

// GetComponentByID retrieves a component record by its unique database identifier (stub in Layer 1).
func (m *MemoryStore) GetComponentByID(ctx context.Context, componentID int64) (*ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "method GetComponentByID not implemented in MemoryStore")
}

// ListComponents returns all non-deleted components in the catalog (stub in Layer 1).
func (m *MemoryStore) ListComponents(ctx context.Context) ([]*ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "method ListComponents not implemented in MemoryStore")
}

// UpdateObservedGeneration synchronizes actual component access, generation, and state (stub in Layer 1).
func (m *MemoryStore) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string) error {
	return status.Error(codes.Unimplemented, "method UpdateObservedGeneration not implemented in MemoryStore")
}

// PrepareDeactivation checks deactivation safety and cascading requirements (stub).
func (m *MemoryStore) PrepareDeactivation(ctx context.Context, name string, requestCascade bool) (*PrepareDeactivationResult, error) {
	return nil, status.Error(codes.Unimplemented, "method PrepareDeactivation not implemented in MemoryStore")
}

// CompleteDeactivation marks a component in DISABLING state as DISABLED (stub).
func (m *MemoryStore) CompleteDeactivation(ctx context.Context, name string) error {
	return status.Error(codes.Unimplemented, "method CompleteDeactivation not implemented in MemoryStore")
}

// ExecuteDeletion deletes a component using the specified deletion policy (stub).
func (m *MemoryStore) ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool) error {
	return status.Error(codes.Unimplemented, "method ExecuteDeletion not implemented in MemoryStore")
}

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation (stub).
func (m *MemoryStore) ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	return nil, nil, status.Error(codes.Unimplemented, "method ResolveComponentSnapshot not implemented in MemoryStore")
}

// ValidateComponentRequirement checks if a required component type and version exists and is active (stub).
func (m *MemoryStore) ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*RequirementValidationResult, error) {
	return nil, status.Error(codes.Unimplemented, "method ValidateComponentRequirement not implemented in MemoryStore")
}

// GetPlatformHealth retrieves overall system component health metrics and status (stub).
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
