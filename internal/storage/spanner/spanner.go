// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package spanner contains the concrete implementation for the storage layer using Cloud Spanner.
package spanner

import (
	"fmt"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"task/go/status"
	"xmanager_cloud/internal/storage/spanner/artifact"
	"xmanager_cloud/internal/storage/spanner/experiment"
	"xmanager_cloud/internal/storage/spanner/operation"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
	"xmanager_cloud/internal/storage/spanner/workunit"
	"xmanager_cloud/internal/storage/spanner/xid"
)

type ExperimentRepository = experiment.Repository
type WorkUnitRepository = workunit.Repository
type ArtifactRepository = artifact.Repository
type XIDRepository = xid.Repository
type StatusMessageRepository = statusmessage.Repository
type OperationRepository = operation.Repository

// Storage implements the storage.Storage interface using Google Cloud Spanner.
type Storage struct {
	client *spanner.Client
	*ExperimentRepository
	*WorkUnitRepository
	*ArtifactRepository
	*XIDRepository
	*StatusMessageRepository
	*OperationRepository
}

// NewStorage creates a new instance of the Spanner-backed storage.
func NewStorage(client *spanner.Client) (*Storage, error) {
	return NewStorageWithConfig(client, StorageConfig{})
}

// StorageConfig holds configuration for the Storage.
type StorageConfig struct {
	StatusMessageHistoryEnabled bool
}

// NewStorageWithConfig creates a new instance of the Spanner-backed storage with the given configuration.
func NewStorageWithConfig(client *spanner.Client, cfg StorageConfig) (*Storage, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil: %w", status.ErrInvalidArgument)
	}

	expRepo, err := experiment.NewRepository(client, experiment.RepositoryConfig{
		StatusMessageHistoryEnabled: cfg.StatusMessageHistoryEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create experiment repository: %w", err)
	}

	wuRepo, err := workunit.NewRepository(client, expRepo, workunit.RepositoryConfig{
		StatusMessageHistoryEnabled: cfg.StatusMessageHistoryEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create workunit repository: %w", err)
	}

	artifactRepo, err := artifact.NewRepository(client)
	if err != nil {
		return nil, fmt.Errorf("failed to create artifact repository: %w", err)
	}

	xidRepo, err := xid.NewRepository(client)
	if err != nil {
		return nil, fmt.Errorf("failed to create xid repository: %w", err)
	}

	statusMsgRepo, err := statusmessage.NewRepository(client)
	if err != nil {
		return nil, fmt.Errorf("failed to create statusmessage repository: %w", err)
	}

	opRepo, err := operation.NewRepository(client)
	if err != nil {
		return nil, fmt.Errorf("failed to create operation repository: %w", err)
	}

	return &Storage{
		client:                  client,
		ExperimentRepository:    expRepo,
		WorkUnitRepository:      wuRepo,
		ArtifactRepository:      artifactRepo,
		XIDRepository:           xidRepo,
		StatusMessageRepository: statusMsgRepo,
		OperationRepository:     opRepo,
	}, nil
}
