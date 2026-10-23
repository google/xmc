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

package dbstore

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dependency"
)

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
// The active record and the historical snapshots are read in one read-only snapshot.
func (s *Store) ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, nil, err
	}
	if generation < 0 {
		return nil, nil, status.Error(codes.InvalidArgument, "generation cannot be negative")
	}

	var rec *store.ComponentRecord
	var coords *componentspb.ResolvedComponentCoordinates
	err = s.repo.ReadSnapshot(ctx, func(ctx context.Context, r store.ComponentReader) error {
		var err error
		rec, coords, err = resolveSnapshot(ctx, r, canonicalName, generation)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return rec, coords, nil
}

// resolveSnapshot resolves the snapshot of canonicalName at generation (0 means the current
// generation) within r.
func resolveSnapshot(ctx context.Context, r store.ComponentReader, canonicalName string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	rec, err := r.GetActiveComponent(ctx, canonicalName)
	if err != nil {
		if errors.Is(err, store.ErrComponentNotFound) && generation > 0 {
			return resolveHistoricalSnapshotByName(ctx, r, canonicalName, generation)
		}
		return nil, nil, err
	}

	targetGen := generation
	if targetGen == 0 {
		targetGen = rec.Generation
	}

	if targetGen > rec.Generation {
		// If the active component is at a lower generation than requested (e.g. name was reused
		// after soft-deleting an older incarnation that reached a higher generation), attempt
		// resolving historically by name before returning NotFound.
		snapRec, coords, err := resolveHistoricalSnapshotByName(ctx, r, canonicalName, targetGen)
		if err == nil {
			return snapRec, coords, nil
		}
		if status.Code(err) != codes.NotFound {
			return nil, nil, err
		}
		return nil, nil, status.Errorf(codes.NotFound, "snapshot for generation %d of component %s not found (current generation is %d)", targetGen, canonicalName, rec.Generation)
	}

	if targetGen == rec.Generation {
		var coords *componentspb.ResolvedComponentCoordinates
		if rec.Status != nil && rec.Status.Coordinates != nil {
			coords = store.CloneCoordinates(rec.Status.Coordinates)
		}
		return store.CloneRecord(rec), coords, nil
	}

	return resolveHistoricalSnapshot(ctx, r, rec, targetGen, canonicalName)
}

// resolveHistoricalSnapshot resolves a snapshot of an active component by its ComponentId,
// inheriting the timestamps of the owning active record.
func resolveHistoricalSnapshot(ctx context.Context, r store.ComponentReader, rec *store.ComponentRecord, targetGen int64, canonicalName string) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	snapRec, err := r.GetSnapshot(ctx, rec.ComponentID, targetGen)
	if err != nil {
		return nil, nil, snapshotReadError(err, canonicalName, targetGen)
	}
	snapRec, coords := hydrateSnapshot(snapRec, canonicalName, rec.CreateTime, rec.UpdateTime)
	return snapRec, coords, nil
}

// resolveHistoricalSnapshotByName resolves a snapshot by component name, covering soft-deleted or
// recreated incarnations whose active record is unavailable. Timestamps come from the
// incarnation that owns the snapshot.
func resolveHistoricalSnapshotByName(ctx context.Context, r store.ComponentReader, canonicalName string, targetGen int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	snapRec, err := r.GetSnapshotByName(ctx, canonicalName, targetGen)
	if err != nil {
		return nil, nil, snapshotReadError(err, canonicalName, targetGen)
	}
	snapRec, coords := hydrateSnapshot(snapRec, canonicalName, snapRec.CreateTime, snapRec.UpdateTime)
	return snapRec, coords, nil
}

// snapshotReadError returns the NotFound status of a missing snapshot, or err unchanged.
func snapshotReadError(err error, canonicalName string, targetGen int64) error {
	if errors.Is(err, store.ErrSnapshotNotFound) {
		return status.Errorf(codes.NotFound, "historical snapshot for generation %d of component %s not found", targetGen, canonicalName)
	}
	return err
}

// hydrateSnapshot completes a decoded historical snapshot record and returns it with its
// coordinates. Timestamps are supplied by the caller because snapshots do not store their own.
func hydrateSnapshot(snapRec *store.ComponentRecord, canonicalName string, createTime, updateTime time.Time) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates) {
	snapRec.Name = canonicalName
	snapRec.CreateTime = createTime
	snapRec.UpdateTime = updateTime
	if snapRec.Status != nil {
		snapRec.State = snapRec.Status.State
		obsGen := snapRec.Status.ObservedGeneration
		snapRec.ObservedGeneration = &obsGen
	}
	snapRec.Dependencies = dependency.ExtractDependencies(snapRec.Spec, snapRec.Labels)
	snapRec.Etag = store.GenerateEtag(snapRec)

	var coords *componentspb.ResolvedComponentCoordinates
	if snapRec.Status != nil && snapRec.Status.Coordinates != nil {
		coords = store.CloneCoordinates(snapRec.Status.Coordinates)
	}
	return snapRec, coords
}
