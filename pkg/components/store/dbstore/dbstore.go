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

// Package dbstore implements store.Store over a store.ComponentRepository. It owns every catalog
// rule (ownership fencing, intent planning, dependency validation, lifecycle transitions and
// snapshot resolution) and leaves queries, mutations and transaction mechanics to the repository,
// so each database backend only implements the repository.
package dbstore

import (
	"context"
	"fmt"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/semver"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/lifecycle"
)

// Store is a database-backed store.Store whose persistence is delegated to a
// store.ComponentRepository.
type Store struct {
	repo store.ComponentRepository
}

// New returns a Store that persists through repo.
func New(repo store.ComponentRepository) *Store {
	return &Store{repo: repo}
}

// GetComponent retrieves a component record by canonical or full resource name.
func (s *Store) GetComponent(ctx context.Context, name string) (*store.ComponentRecord, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, err
	}

	var rec *store.ComponentRecord
	err = s.repo.ReadSnapshot(ctx, func(ctx context.Context, r store.ComponentReader) error {
		var err error
		rec, err = r.GetActiveComponent(ctx, canonicalName)
		return err
	})
	if err != nil {
		return nil, err
	}

	return store.CloneRecord(rec), nil
}

// GetComponentByID retrieves a component record by its unique database identifier.
func (s *Store) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	if componentID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid component ID")
	}

	var rec *store.ComponentRecord
	err := s.repo.ReadSnapshot(ctx, func(ctx context.Context, r store.ComponentReader) error {
		var err error
		rec, err = r.GetComponentByID(ctx, componentID)
		return err
	})
	if err != nil {
		return nil, err
	}

	return store.CloneRecord(rec), nil
}

// ListComponents returns all non-deleted components in the catalog sorted by name.
func (s *Store) ListComponents(ctx context.Context) ([]*store.ComponentRecord, error) {
	var catalog map[string]*store.ComponentRecord
	err := s.repo.ReadSnapshot(ctx, func(ctx context.Context, r store.ComponentReader) error {
		var err error
		catalog, err = r.ListActiveComponents(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}

	results := make([]*store.ComponentRecord, 0, len(catalog))
	for _, rec := range catalog {
		if !store.IsDeletedRecord(rec) {
			results = append(results, store.CloneRecord(rec))
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

	return results, nil
}

// ValidateComponentRequirement checks if a required component type and version exists and is active.
func (s *Store) ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*store.RequirementValidationResult, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, err
	}
	if minVersion != "" {
		if _, err := semver.Parse(minVersion); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid minimum version %q: %v", minVersion, err)
		}
	}

	rec, err := s.GetComponent(ctx, canonicalName)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return &store.RequirementValidationResult{
				IsSatisfied:             false,
				SatisfyingComponentName: canonicalName,
				Reason:                  fmt.Sprintf("component %q not found", canonicalName),
			}, nil
		}
		return nil, err
	}

	return lifecycle.EvaluateComponentRequirement(rec, canonicalName, reqType, minVersion)
}

// GetPlatformHealth retrieves overall system component health metrics and status.
func (s *Store) GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetPlatformHealth not implemented")
}
