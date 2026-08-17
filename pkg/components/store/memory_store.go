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

package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

var _ Store = (*MemoryStore)(nil)

// MemoryStore provides an in-memory implementation of Store for hermetic testing.
type MemoryStore struct {
	mu         sync.RWMutex
	components map[string]*ComponentRecord
	byID       map[int64]*ComponentRecord
	snapshots  map[int64][]*componentspb.ResolvedComponentCoordinates
	nextID     int64
}

// NewMemoryStore creates a new MemoryStore instance.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		components: make(map[string]*ComponentRecord),
		byID:       make(map[int64]*ComponentRecord),
		snapshots:  make(map[int64][]*componentspb.ResolvedComponentCoordinates),
		nextID:     1000,
	}
}

// SetComponentIntent applies or updates component intent in memory.
func (m *MemoryStore) SetComponentIntent(ctx context.Context, name string, intent *componentspb.ComponentSpecProto, desiredGen int64) (*ComponentRecord, error) {
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "component name must not be empty")
	}
	if intent == nil {
		return nil, status.Error(codes.InvalidArgument, "intent must not be nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate dependencies exist and are not deleted/disabled when enabling.
	if intent.GetEnabled() {
		for _, depName := range intent.GetDependencies() {
			dep, exists := m.components[depName]
			if !exists || dep.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
				return nil, status.Errorf(codes.FailedPrecondition, "dependency %q not found", depName)
			}
			if dep.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED || dep.State == componentspb.ComponentState_COMPONENT_STATE_DISABLING {
				return nil, status.Errorf(codes.FailedPrecondition, "dependency %q is disabled", depName)
			}
		}
	}

	existing, exists := m.components[name]
	now := time.Now().UTC()
	clonedIntent := proto.Clone(intent).(*componentspb.ComponentSpecProto)

	if !exists || existing.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		m.nextID++
		compID := m.nextID
		state := componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		if !intent.GetEnabled() {
			state = componentspb.ComponentState_COMPONENT_STATE_DISABLED
		}

		rec := &ComponentRecord{
			ComponentID: compID,
			Name:        name,
			ConfigProto: clonedIntent,
			State:       state,
			Generation:  desiredGen,
			CreateTime:  now,
			UpdateTime:  now,
		}
		m.components[name] = rec
		m.byID[compID] = rec
		return rec, nil
	}

	nextState := existing.State
	if !intent.GetEnabled() {
		if existing.State == componentspb.ComponentState_COMPONENT_STATE_ENABLED {
			// Check active dependents.
			var activeDeps []string
			for _, other := range m.components {
				if other.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED && other.State != componentspb.ComponentState_COMPONENT_STATE_DELETED {
					for _, d := range other.ConfigProto.GetDependencies() {
						if d == name {
							activeDeps = append(activeDeps, other.Name)
						}
					}
				}
			}
			if len(activeDeps) > 0 && !intent.GetLifecycleControl().GetCascade() {
				return nil, status.Errorf(codes.FailedPrecondition, "cannot disable %q: active dependent components exist (%s)", name, strings.Join(activeDeps, ", "))
			}
			nextState = componentspb.ComponentState_COMPONENT_STATE_DISABLING
		} else {
			nextState = componentspb.ComponentState_COMPONENT_STATE_DISABLED
		}
	} else if intent.GetEnabled() && (existing.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED || existing.State == componentspb.ComponentState_COMPONENT_STATE_DISABLING) {
		nextState = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	}

	existing.ConfigProto = clonedIntent
	existing.State = nextState
	existing.Generation = desiredGen
	existing.UpdateTime = now
	return existing, nil
}

// GetComponent retrieves a component record by its name.
func (m *MemoryStore) GetComponent(ctx context.Context, name string) (*ComponentRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, exists := m.components[name]
	if !exists || rec.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		return nil, status.Errorf(codes.NotFound, "component %q not found", name)
	}
	return rec, nil
}

// GetComponentByID retrieves a component record by ID.
func (m *MemoryStore) GetComponentByID(ctx context.Context, componentID int64) (*ComponentRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, exists := m.byID[componentID]
	if !exists {
		return nil, status.Errorf(codes.NotFound, "component ID %d not found", componentID)
	}
	return rec, nil
}

// ListComponents returns all registered non-deleted components.
func (m *MemoryStore) ListComponents(ctx context.Context) ([]*ComponentRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []*ComponentRecord
	for _, rec := range m.components {
		if rec.State != componentspb.ComponentState_COMPONENT_STATE_DELETED {
			list = append(list, rec)
		}
	}
	return list, nil
}

// PrepareDeactivation validates if a component can be safely deactivated.
func (m *MemoryStore) PrepareDeactivation(ctx context.Context, name string, requestCascade bool) (*PrepareDeactivationResult, error) {
	comp, err := m.GetComponent(ctx, name)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var blockingDeps []string
	for _, other := range m.components {
		if other.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED && other.State != componentspb.ComponentState_COMPONENT_STATE_DELETED {
			for _, d := range other.ConfigProto.GetDependencies() {
				if d == name {
					blockingDeps = append(blockingDeps, other.Name)
				}
			}
		}
	}

	drainSec := int32(5)
	if comp.ConfigProto != nil && comp.ConfigProto.GetLifecycleControl() != nil && comp.ConfigProto.GetLifecycleControl().GetDrainTimeoutSeconds() > 0 {
		drainSec = comp.ConfigProto.GetLifecycleControl().GetDrainTimeoutSeconds()
	}

	if len(blockingDeps) > 0 && !requestCascade {
		return &PrepareDeactivationResult{
			IsSafeToDeactivate:  false,
			BlockingDependents:  blockingDeps,
			RecommendedDrainSec: drainSec,
			RejectionReason:     fmt.Sprintf("active dependent components exist: %s", strings.Join(blockingDeps, ", ")),
		}, nil
	}

	return &PrepareDeactivationResult{
		IsSafeToDeactivate:    true,
		BlockingDependents:    nil,
		RecommendedDrainSec:   drainSec,
		CascadedDeactivations: blockingDeps,
	}, nil
}

// CompleteDeactivation transitions a component from DISABLING to DISABLED.
func (m *MemoryStore) CompleteDeactivation(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	comp, exists := m.components[name]
	if !exists || comp.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		return status.Errorf(codes.NotFound, "component %q not found", name)
	}
	comp.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
	comp.UpdateTime = time.Now().UTC()
	return nil
}

// UpdateObservedGeneration updates observed generation and access status in memory.
func (m *MemoryStore) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatusProto, statusCond string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	comp, exists := m.components[name]
	if !exists || comp.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		return status.Errorf(codes.NotFound, "component %q not found", name)
	}

	comp.ObservedGeneration = &observedGen
	if access != nil {
		comp.AccessPayload = proto.Clone(access).(*componentspb.ComponentAccessStatusProto)
	}
	if comp.State == componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION && strings.Contains(statusCond, "True") {
		comp.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	}
	comp.UpdateTime = time.Now().UTC()
	return nil
}

// ExecuteDeletion soft-deletes a component row.
func (m *MemoryStore) ExecuteDeletion(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	comp, exists := m.components[name]
	if !exists || comp.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		return status.Errorf(codes.NotFound, "component %q not found", name)
	}
	if comp.ConfigProto != nil && comp.ConfigProto.GetDeletionProtection() {
		return status.Errorf(codes.FailedPrecondition, "cannot delete %q: deletionProtection is enabled", name)
	}

	comp.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	comp.UpdateTime = time.Now().UTC()
	delete(m.components, name)
	return nil
}

// ResolveComponentSnapshot locks and flattens component configurations for an experiment at T0.
func (m *MemoryStore) ResolveComponentSnapshot(ctx context.Context, xid int64, requestedClasses []string) ([]*componentspb.ResolvedComponentCoordinates, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	resolvedMap := make(map[int64]*ComponentRecord)
	var queue []string
	queue = append(queue, requestedClasses...)

	for len(queue) > 0 {
		cls := queue[0]
		queue = queue[1:]

		var found *ComponentRecord
		for _, c := range m.components {
			if (c.Name == cls || (c.ConfigProto != nil && c.ConfigProto.GetComponentClass() == cls)) && c.State != componentspb.ComponentState_COMPONENT_STATE_DELETED {
				found = c
				break
			}
		}

		if found == nil {
			return nil, status.Errorf(codes.NotFound, "requested component class or name %q not found", cls)
		}
		if found.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
			return nil, status.Errorf(codes.FailedPrecondition, "requested component %q is not in ENABLED state", cls)
		}

		if _, exists := resolvedMap[found.ComponentID]; !exists {
			resolvedMap[found.ComponentID] = found
			if found.ConfigProto != nil {
				queue = append(queue, found.ConfigProto.GetDependencies()...)
			}
		}
	}

	var coordinates []*componentspb.ResolvedComponentCoordinates
	for _, comp := range resolvedMap {
		cls := comp.Name
		if comp.ConfigProto != nil && comp.ConfigProto.GetComponentClass() != "" {
			cls = comp.ConfigProto.GetComponentClass()
		}
		coords := &componentspb.ResolvedComponentCoordinates{
			ComponentClass:   cls,
			ComponentName:    comp.Name,
			LockedGeneration: comp.Generation,
			Access:           comp.AccessPayload,
		}
		coordinates = append(coordinates, coords)
	}

	m.snapshots[xid] = coordinates
	return coordinates, nil
}

// ValidateComponentRequirement checks if a requested class is satisfied.
func (m *MemoryStore) ValidateComponentRequirement(ctx context.Context, requiredClass string, minParams map[string]string) (*RequirementValidationResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var comp *ComponentRecord
	for _, c := range m.components {
		if (c.Name == requiredClass || (c.ConfigProto != nil && c.ConfigProto.GetComponentClass() == requiredClass)) && c.State != componentspb.ComponentState_COMPONENT_STATE_DELETED {
			comp = c
			break
		}
	}

	if comp == nil {
		return &RequirementValidationResult{
			IsSatisfied: false,
			ErrorDetail: fmt.Sprintf("no active component found satisfying class %q", requiredClass),
		}, nil
	}

	if comp.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		return &RequirementValidationResult{
			IsSatisfied:             false,
			SatisfyingComponentName: comp.Name,
			ErrorDetail:             fmt.Sprintf("component %q is disabled", comp.Name),
		}, nil
	}

	if comp.ConfigProto != nil && len(minParams) > 0 {
		params := comp.ConfigProto.GetParameters()
		for k, v := range minParams {
			if actual, ok := params[k]; !ok || actual != v {
				return &RequirementValidationResult{
					IsSatisfied:             false,
					SatisfyingComponentName: comp.Name,
					ErrorDetail:             fmt.Sprintf("parameter mismatch for %q: expected %q, got %q", k, v, actual),
				}, nil
			}
		}
	}

	return &RequirementValidationResult{
		IsSatisfied:             true,
		SatisfyingComponentName: comp.Name,
	}, nil
}

// GetPlatformHealth computes platform-wide component health telemetry.
func (m *MemoryStore) GetPlatformHealth(ctx context.Context) (*componentspb.GetPlatformHealthResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	resp := &componentspb.GetPlatformHealthResponse{}
	for _, c := range m.components {
		if c.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
			continue
		}
		observed := int64(0)
		if c.ObservedGeneration != nil {
			observed = *c.ObservedGeneration
		}
		lagging := observed < c.Generation
		cond := "Ready=True"
		if c.State == componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
			cond = "Ready=Unknown"
		} else if c.State == componentspb.ComponentState_COMPONENT_STATE_DISABLING || c.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			cond = "Ready=False"
		}

		if lagging || c.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			resp.IsPlatformDegraded = true
		}

		resp.ComponentMetrics = append(resp.ComponentMetrics, &componentspb.ComponentHealthTelemetry{
			ComponentName:          c.Name,
			StatusCondition:        cond,
			ObservedGeneration:     observed,
			DesiredGeneration:      c.Generation,
			IsCdcProjectionLagging: lagging,
		})
	}
	return resp, nil
}
