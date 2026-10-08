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
	"cmp"
	"fmt"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/semver"
)

// ResolveComponentSnapshotFromMap resolves or caches an immutable snapshot of the component name at
// generation (0 means the current generation) in the same order as SpannerStore: the active
// incarnation resolves its own generations by ComponentId, and a later generation, or a name
// without an active incarnation, resolves the newest incarnation holding a snapshot at generation.
// components and snapshots are keyed by ComponentId.
func ResolveComponentSnapshotFromMap(components map[int64]*ComponentRecord, snapshots map[int64]map[int64]*SnapshotRecord, name string, generation int64) (*ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	canonicalName, err := validateSnapshotArgs(name, generation)
	if err != nil {
		return nil, nil, err
	}
	incarnations := incarnationsNewestFirst(components, canonicalName)
	active := activeIncarnation(incarnations)
	switch {
	case active != nil && generation <= active.Generation:
		return resolveActiveSnapshot(active, snapshots, canonicalName, generation)
	case active != nil:
		rec, coords, err := newestRecordedSnapshot(incarnations, snapshots, canonicalName, generation)
		if status.Code(err) == codes.NotFound {
			return nil, nil, laterGenerationNotFound(canonicalName, generation, active.Generation)
		}
		return rec, coords, err
	case generation == 0:
		return nil, nil, ErrComponentNotFound
	default:
		return newestRecordedSnapshot(incarnations, snapshots, canonicalName, generation)
	}
}

// ResolveComponentSnapshotByIDFromMap resolves the snapshot of the incarnation componentID of the
// component name at generation, like SpannerStore.ResolveComponentSnapshotByID: an active
// incarnation resolves its own generations as ResolveComponentSnapshotFromMap does, a soft-deleted
// one resolves only recorded snapshots (its final record is never used as a fallback, so
// generation 0 is NotFound), and an incarnation of another name is NotFound.
func ResolveComponentSnapshotByIDFromMap(components map[int64]*ComponentRecord, snapshots map[int64]map[int64]*SnapshotRecord, name string, componentID, generation int64) (*ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	canonicalName, err := validatePinnedSnapshotArgs(name, componentID, generation)
	if err != nil {
		return nil, nil, err
	}
	rec := components[componentID]
	if rec != nil && rec.Name == canonicalName && !isDeletedRecord(rec) {
		return resolveActiveSnapshot(rec, snapshots, canonicalName, generation)
	}
	if generation == 0 {
		return nil, nil, noActiveIncarnation(canonicalName, componentID)
	}
	snap := recordedSnapshotAt(snapshots, componentID, generation)
	if rec == nil || rec.Name != canonicalName || snap == nil {
		return nil, nil, incarnationSnapshotNotFound(canonicalName, componentID, generation)
	}
	return cloneRecord(snap.Record), cloneCoordinates(snap.Coordinates), nil
}

// noActiveIncarnation reports a pinned request for the current generation (0) of an incarnation
// that is not the active one: a soft-deleted incarnation resolves only explicit generations.
func noActiveIncarnation(canonicalName string, componentID int64) error {
	return status.Errorf(codes.NotFound, "component %s has no active incarnation with component ID %d; a soft-deleted incarnation resolves only an explicit generation", canonicalName, componentID)
}

// incarnationSnapshotNotFound reports a pinned request that matches no stored snapshot, whether
// the incarnation is unknown, belongs to another name, or has no snapshot at generation.
func incarnationSnapshotNotFound(canonicalName string, componentID, generation int64) error {
	return status.Errorf(codes.NotFound, "snapshot for generation %d of component %s with component ID %d not found", generation, canonicalName, componentID)
}

// validateSnapshotArgs returns the canonical name of a snapshot request.
func validateSnapshotArgs(name string, generation int64) (string, error) {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return "", err
	}
	if generation < 0 {
		return "", status.Error(codes.InvalidArgument, "generation cannot be negative")
	}
	return canonicalName, nil
}

// validatePinnedSnapshotArgs returns the canonical name of a snapshot request pinned to an
// incarnation.
func validatePinnedSnapshotArgs(name string, componentID, generation int64) (string, error) {
	if componentID <= 0 {
		return "", status.Error(codes.InvalidArgument, "component ID must be positive")
	}
	return validateSnapshotArgs(name, generation)
}

// laterGenerationNotFound reports that no snapshot exists at a generation after the current one.
func laterGenerationNotFound(canonicalName string, generation, current int64) error {
	return status.Errorf(codes.NotFound, "snapshot for generation %d of component %s not found (current generation is %d)", generation, canonicalName, current)
}

// incarnationsNewestFirst returns the records named canonicalName, newest first, ordered like
// SpannerStore by creation time and then ComponentId.
func incarnationsNewestFirst(components map[int64]*ComponentRecord, canonicalName string) []*ComponentRecord {
	var incarnations []*ComponentRecord
	for _, rec := range components {
		if rec != nil && rec.Name == canonicalName {
			incarnations = append(incarnations, rec)
		}
	}
	slices.SortFunc(incarnations, func(a, b *ComponentRecord) int {
		return cmp.Or(b.CreateTime.Compare(a.CreateTime), cmp.Compare(b.ComponentID, a.ComponentID))
	})
	return incarnations
}

// activeIncarnation returns the incarnation that is not soft-deleted, or nil.
func activeIncarnation(incarnations []*ComponentRecord) *ComponentRecord {
	if i := slices.IndexFunc(incarnations, func(rec *ComponentRecord) bool { return !isDeletedRecord(rec) }); i >= 0 {
		return incarnations[i]
	}
	return nil
}

// resolveActiveSnapshot resolves generation (0 means current) of the active incarnation rec.
func resolveActiveSnapshot(rec *ComponentRecord, snapshots map[int64]map[int64]*SnapshotRecord, canonicalName string, generation int64) (*ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	targetGen := cmp.Or(generation, rec.Generation)
	if targetGen > rec.Generation {
		return nil, nil, laterGenerationNotFound(canonicalName, targetGen, rec.Generation)
	}
	return getOrCacheSnapshot(rec, snapshots, canonicalName, targetGen)
}

// newestRecordedSnapshot returns the snapshot at generation of the newest incarnation holding one.
func newestRecordedSnapshot(incarnations []*ComponentRecord, snapshots map[int64]map[int64]*SnapshotRecord, canonicalName string, generation int64) (*ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	for _, rec := range incarnations {
		if snap := recordedSnapshotAt(snapshots, rec.ComponentID, generation); snap != nil {
			return cloneRecord(snap.Record), cloneCoordinates(snap.Coordinates), nil
		}
	}
	return nil, nil, historicalSnapshotNotFound(canonicalName, generation)
}

// recordedSnapshotAt returns the recorded snapshot of the incarnation componentID at generation,
// or nil.
func recordedSnapshotAt(snapshots map[int64]map[int64]*SnapshotRecord, componentID, generation int64) *SnapshotRecord {
	if snap := snapshots[componentID][generation]; snap != nil && snap.Record != nil {
		return snap
	}
	return nil
}

func historicalSnapshotNotFound(canonicalName string, generation int64) error {
	return status.Errorf(codes.NotFound, "historical snapshot for generation %d of component %s not found", generation, canonicalName)
}

func getOrCacheSnapshot(currentRec *ComponentRecord, snapshots map[int64]map[int64]*SnapshotRecord, canonicalName string, targetGen int64) (*ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	id := currentRec.ComponentID
	if snap := recordedSnapshotAt(snapshots, id, targetGen); snap != nil {
		return cloneRecord(snap.Record), cloneCoordinates(snap.Coordinates), nil
	}

	if targetGen < currentRec.Generation {
		return nil, nil, historicalSnapshotNotFound(canonicalName, targetGen)
	}

	snap := createSnapshotRecord(currentRec, targetGen)
	if snapshots != nil {
		if snapshots[id] == nil {
			snapshots[id] = make(map[int64]*SnapshotRecord)
		}
		snapshots[id][targetGen] = snap
	}

	return cloneRecord(snap.Record), cloneCoordinates(snap.Coordinates), nil
}

func createSnapshotRecord(rec *ComponentRecord, gen int64) *SnapshotRecord {
	var coords *componentspb.ResolvedComponentCoordinates
	if rec.Status != nil && rec.Status.Coordinates != nil {
		coords = cloneCoordinates(rec.Status.Coordinates)
	}

	clonedRec := cloneRecord(rec)
	clonedRec.Generation = gen
	if clonedRec.Spec != nil {
		clonedRec.Spec.Generation = gen
	}

	return &SnapshotRecord{
		Name:        rec.Name,
		Generation:  gen,
		Record:      clonedRec,
		Coordinates: coords,
		CreatedAt:   time.Now().UTC(),
	}
}

func cloneCoordinates(coords *componentspb.ResolvedComponentCoordinates) *componentspb.ResolvedComponentCoordinates {
	if coords == nil {
		return nil
	}
	return proto.Clone(coords).(*componentspb.ResolvedComponentCoordinates)
}

// ValidateComponentRequirementFromMap checks whether a required component exists, is ENABLED, and satisfies type/version constraints.
func ValidateComponentRequirementFromMap(components map[string]*ComponentRecord, name string, reqType componentspb.ComponentType, minVersion string) (*RequirementValidationResult, error) {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return nil, err
	}
	if minVersion != "" {
		if _, err := semver.Parse(minVersion); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid minimum version %q: %v", minVersion, err)
		}
	}

	rec, exists := components[canonicalName]
	if !exists || rec == nil || isDeletedRecord(rec) {
		return &RequirementValidationResult{
			IsSatisfied:             false,
			SatisfyingComponentName: canonicalName,
			Reason:                  fmt.Sprintf("component %q not found", canonicalName),
		}, nil
	}

	return evaluateComponentRequirement(rec, canonicalName, reqType, minVersion)
}

func evaluateComponentRequirement(rec *ComponentRecord, canonicalName string, reqType componentspb.ComponentType, minVersion string) (*RequirementValidationResult, error) {
	if rec.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		return &RequirementValidationResult{
			IsSatisfied:             false,
			SatisfyingComponentName: canonicalName,
			Reason:                  fmt.Sprintf("component %q is in state %s, required ENABLED", canonicalName, rec.State),
			Component:               cloneRecord(rec),
		}, nil
	}

	if reqType != componentspb.ComponentType_COMPONENT_TYPE_UNSPECIFIED {
		if rec.Spec == nil || rec.Spec.GetType() != reqType {
			actualType := componentspb.ComponentType_COMPONENT_TYPE_UNSPECIFIED
			if rec.Spec != nil {
				actualType = rec.Spec.GetType()
			}
			return &RequirementValidationResult{
				IsSatisfied:             false,
				SatisfyingComponentName: canonicalName,
				Reason:                  fmt.Sprintf("component %q has type %s, required %s", canonicalName, actualType, reqType),
				Component:               cloneRecord(rec),
			}, nil
		}
	}

	return checkVersionRequirement(rec, canonicalName, minVersion)
}

func checkVersionRequirement(rec *ComponentRecord, canonicalName, minVersion string) (*RequirementValidationResult, error) {
	if minVersion == "" {
		return &RequirementValidationResult{
			IsSatisfied:             true,
			SatisfyingComponentName: canonicalName,
			Reason:                  "requirement satisfied",
			Component:               cloneRecord(rec),
		}, nil
	}

	if rec.Spec == nil || rec.Spec.GetVersion() == "" {
		return &RequirementValidationResult{
			IsSatisfied:             false,
			SatisfyingComponentName: canonicalName,
			Reason:                  fmt.Sprintf("component %q has no version specified", canonicalName),
			Component:               cloneRecord(rec),
		}, nil
	}

	satisfies, err := semver.SatisfiesMinVersion(rec.Spec.GetVersion(), minVersion)
	if err != nil {
		return &RequirementValidationResult{
			IsSatisfied:             false,
			SatisfyingComponentName: canonicalName,
			Reason:                  fmt.Sprintf("component %q has invalid semver %q: %v", canonicalName, rec.Spec.GetVersion(), err),
			Component:               cloneRecord(rec),
		}, nil
	}

	if !satisfies {
		return &RequirementValidationResult{
			IsSatisfied:             false,
			SatisfyingComponentName: canonicalName,
			Reason:                  fmt.Sprintf("component %q version %s does not satisfy minimum version %s", canonicalName, rec.Spec.GetVersion(), minVersion),
			Component:               cloneRecord(rec),
		}, nil
	}

	return &RequirementValidationResult{
		IsSatisfied:             true,
		SatisfyingComponentName: canonicalName,
		Reason:                  "requirement satisfied",
		Component:               cloneRecord(rec),
	}, nil
}
