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

package lifecycle

import (
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/semver"
	"xmanager_cloud/pkg/components/store"
)

// ResolveComponentSnapshotFromMap resolves or caches an immutable snapshot of a component at the requested generation.
func ResolveComponentSnapshotFromMap(components map[string]*store.ComponentRecord, snapshots map[string]map[int64]*store.SnapshotRecord, name string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, nil, err
	}
	if generation < 0 {
		return nil, nil, status.Error(codes.InvalidArgument, "generation cannot be negative")
	}

	rec, exists := components[canonicalName]
	if !exists || rec == nil || store.IsDeletedRecord(rec) {
		return nil, nil, store.ErrComponentNotFound
	}

	targetGen := generation
	if targetGen == 0 {
		targetGen = rec.Generation
	}
	if targetGen > rec.Generation {
		return nil, nil, status.Errorf(codes.NotFound, "snapshot for generation %d of component %s not found (current generation is %d)", targetGen, canonicalName, rec.Generation)
	}

	return getOrCacheSnapshot(rec, snapshots, canonicalName, targetGen)
}

func getOrCacheSnapshot(currentRec *store.ComponentRecord, snapshots map[string]map[int64]*store.SnapshotRecord, canonicalName string, targetGen int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	if snap, ok := snapshots[canonicalName][targetGen]; ok && snap != nil && snap.Record != nil {
		return store.CloneRecord(snap.Record), store.CloneCoordinates(snap.Coordinates), nil
	}

	if targetGen < currentRec.Generation {
		return nil, nil, status.Errorf(codes.NotFound, "historical snapshot for generation %d of component %s not found", targetGen, canonicalName)
	}

	snap := createSnapshotRecord(currentRec, targetGen)
	if snapshots != nil {
		if snapshots[canonicalName] == nil {
			snapshots[canonicalName] = make(map[int64]*store.SnapshotRecord)
		}
		snapshots[canonicalName][targetGen] = snap
	}

	return store.CloneRecord(snap.Record), store.CloneCoordinates(snap.Coordinates), nil
}

func createSnapshotRecord(rec *store.ComponentRecord, gen int64) *store.SnapshotRecord {
	var coords *componentspb.ResolvedComponentCoordinates
	if rec.Status != nil && rec.Status.Coordinates != nil {
		coords = store.CloneCoordinates(rec.Status.Coordinates)
	}

	clonedRec := store.CloneRecord(rec)
	clonedRec.Generation = gen
	if clonedRec.Spec != nil {
		clonedRec.Spec.Generation = gen
	}

	return &store.SnapshotRecord{
		Name:        rec.Name,
		Generation:  gen,
		Record:      clonedRec,
		Coordinates: coords,
		CreatedAt:   time.Now().UTC(),
	}
}

// ValidateComponentRequirementFromMap checks whether a required component exists, is ENABLED, and satisfies type/version constraints.
func ValidateComponentRequirementFromMap(components map[string]*store.ComponentRecord, name string, reqType componentspb.ComponentType, minVersion string) (*store.RequirementValidationResult, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, err
	}
	if minVersion != "" {
		if _, err := semver.Parse(minVersion); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid minimum version %q: %v", minVersion, err)
		}
	}

	rec, exists := components[canonicalName]
	if !exists || rec == nil || store.IsDeletedRecord(rec) {
		return &store.RequirementValidationResult{
			IsSatisfied:             false,
			SatisfyingComponentName: canonicalName,
			Reason:                  fmt.Sprintf("component %q not found", canonicalName),
		}, nil
	}

	return EvaluateComponentRequirement(rec, canonicalName, reqType, minVersion)
}

// EvaluateComponentRequirement evaluates whether rec satisfies reqType and minVersion.
func EvaluateComponentRequirement(rec *store.ComponentRecord, canonicalName string, reqType componentspb.ComponentType, minVersion string) (*store.RequirementValidationResult, error) {
	if rec.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		return &store.RequirementValidationResult{
			IsSatisfied:             false,
			SatisfyingComponentName: canonicalName,
			Reason:                  fmt.Sprintf("component %q is in state %s, required ENABLED", canonicalName, rec.State),
			Component:               store.CloneRecord(rec),
		}, nil
	}

	if reqType != componentspb.ComponentType_COMPONENT_TYPE_UNSPECIFIED {
		if rec.Spec == nil || rec.Spec.GetType() != reqType {
			actualType := componentspb.ComponentType_COMPONENT_TYPE_UNSPECIFIED
			if rec.Spec != nil {
				actualType = rec.Spec.GetType()
			}
			return &store.RequirementValidationResult{
				IsSatisfied:             false,
				SatisfyingComponentName: canonicalName,
				Reason:                  fmt.Sprintf("component %q has type %s, required %s", canonicalName, actualType, reqType),
				Component:               store.CloneRecord(rec),
			}, nil
		}
	}

	return checkVersionRequirement(rec, canonicalName, minVersion)
}

func checkVersionRequirement(rec *store.ComponentRecord, canonicalName, minVersion string) (*store.RequirementValidationResult, error) {
	if minVersion == "" {
		return &store.RequirementValidationResult{
			IsSatisfied:             true,
			SatisfyingComponentName: canonicalName,
			Reason:                  "requirement satisfied",
			Component:               store.CloneRecord(rec),
		}, nil
	}

	if rec.Spec == nil || rec.Spec.GetVersion() == "" {
		return &store.RequirementValidationResult{
			IsSatisfied:             false,
			SatisfyingComponentName: canonicalName,
			Reason:                  fmt.Sprintf("component %q has no version specified", canonicalName),
			Component:               store.CloneRecord(rec),
		}, nil
	}

	satisfies, err := semver.SatisfiesMinVersion(rec.Spec.GetVersion(), minVersion)
	if err != nil {
		return &store.RequirementValidationResult{
			IsSatisfied:             false,
			SatisfyingComponentName: canonicalName,
			Reason:                  fmt.Sprintf("component %q has invalid semver %q: %v", canonicalName, rec.Spec.GetVersion(), err),
			Component:               store.CloneRecord(rec),
		}, nil
	}

	if !satisfies {
		return &store.RequirementValidationResult{
			IsSatisfied:             false,
			SatisfyingComponentName: canonicalName,
			Reason:                  fmt.Sprintf("component %q version %s does not satisfy minimum version %s", canonicalName, rec.Spec.GetVersion(), minVersion),
			Component:               store.CloneRecord(rec),
		}, nil
	}

	return &store.RequirementValidationResult{
		IsSatisfied:             true,
		SatisfyingComponentName: canonicalName,
		Reason:                  "requirement satisfied",
		Component:               store.CloneRecord(rec),
	}, nil
}
