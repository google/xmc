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

// Package postgres contains the concrete implementation for the storage layer using PostgreSQL.
package postgres

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/artifact"
	"xmanager_cloud/internal/storage/postgres/experiment"
	"xmanager_cloud/internal/storage/postgres/operation"
	"xmanager_cloud/internal/storage/postgres/pausedtask"
	"xmanager_cloud/internal/storage/postgres/pausestate"
	"xmanager_cloud/internal/storage/postgres/statushistory"
	"xmanager_cloud/internal/storage/postgres/statusmessage"
	"xmanager_cloud/internal/storage/postgres/workunit"
	"xmanager_cloud/internal/storage/postgres/xid"
)

type ExperimentRepository = experiment.Repository
type WorkUnitRepository = workunit.Repository
type ArtifactRepository = artifact.Repository
type XIDRepository = xid.Repository
type StatusMessageRepository = statusmessage.Repository
type OperationRepository = operation.Repository
type StatusHistoryRepository = statushistory.Repository
type PauseStateRepository = pausestate.Repository
type PausedTaskRepository = pausedtask.Repository

var _ storage.Storage = (*Storage)(nil)

// Storage implements the storage.Storage interface using PostgreSQL.
type Storage struct {
	*ExperimentRepository
	*WorkUnitRepository
	*ArtifactRepository
	*XIDRepository
	*StatusMessageRepository
	*OperationRepository
	*StatusHistoryRepository
	pauseStateRepository *PauseStateRepository
	pausedTaskRepository *PausedTaskRepository
}

// NewStorage creates a new instance of the PostgreSQL-backed storage.
func NewStorage() *Storage {
	return &Storage{
		ExperimentRepository:    experiment.NewRepository(),
		WorkUnitRepository:      workunit.NewRepository(),
		ArtifactRepository:      artifact.NewRepository(),
		XIDRepository:           xid.NewRepository(),
		StatusMessageRepository: statusmessage.NewRepository(),
		OperationRepository:     operation.NewRepository(),
		StatusHistoryRepository: statushistory.NewRepository(),
		pauseStateRepository:    pausestate.NewRepository(),
		pausedTaskRepository:    pausedtask.NewRepository(),
	}
}

// RunInTransaction executes the given function in a PostgreSQL read-write transaction.
func (s *Storage) RunInTransaction(ctx context.Context, fn func(ctx context.Context, tx *storage.Transaction) error) error {
	return status.Error(codes.Unimplemented, "postgres storage: RunInTransaction is not implemented")
}

// PauseState returns the repository for global pause scheduling state.
func (s *Storage) PauseState() storage.PauseStateRepository {
	return s.pauseStateRepository
}

// PausedTasks returns the repository for pending paused actuation tasks.
func (s *Storage) PausedTasks() storage.PausedTaskRepository {
	return s.pausedTaskRepository
}
