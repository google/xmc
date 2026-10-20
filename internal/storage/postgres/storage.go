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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/artifact"
	"xmanager_cloud/internal/storage/postgres/dashboard"
	"xmanager_cloud/internal/storage/postgres/experiment"
	"xmanager_cloud/internal/storage/postgres/operation"
	"xmanager_cloud/internal/storage/postgres/pausedtask"
	"xmanager_cloud/internal/storage/postgres/pausestate"
	"xmanager_cloud/internal/storage/postgres/statushistory"
	"xmanager_cloud/internal/storage/postgres/statusmessage"
	"xmanager_cloud/internal/storage/postgres/workunit"
	"xmanager_cloud/internal/storage/postgres/xid"
)

type experimentRepository = experiment.Repository
type workUnitRepository = workunit.Repository
type artifactRepository = artifact.Repository
type xidRepository = xid.Repository
type statusMessageRepository = statusmessage.Repository
type operationRepository = operation.Repository
type statusHistoryRepository = statushistory.Repository
type dashboardRepository = dashboard.Repository
type pauseStateRepository = pausestate.Repository
type pausedTaskRepository = pausedtask.Repository

var _ storage.Storage = (*Storage)(nil)

// Storage implements the storage.Storage interface using PostgreSQL.
type Storage struct {
	pool *pgxpool.Pool
	*experimentRepository
	*workUnitRepository
	*artifactRepository
	*xidRepository
	*statusMessageRepository
	*operationRepository
	*statusHistoryRepository
	*dashboardRepository
	pauseStateRepository *pauseStateRepository
	pausedTaskRepository *pausedTaskRepository
}

// NewStorage creates a new instance of the PostgreSQL-backed storage.
func NewStorage(pool *pgxpool.Pool) (*Storage, error) {
	if pool == nil {
		return nil, wrapStatusError("postgres pool must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	return &Storage{
		pool:                    pool,
		experimentRepository:    experiment.NewRepository(),
		workUnitRepository:      workunit.NewRepository(),
		artifactRepository:      artifact.NewRepository(),
		xidRepository:           xid.NewRepository(),
		statusMessageRepository: statusmessage.NewRepository(),
		operationRepository:     operation.NewRepository(),
		statusHistoryRepository: statushistory.NewRepository(),
		dashboardRepository:     dashboard.NewRepository(),
		pauseStateRepository:    pausestate.NewRepository(),
		pausedTaskRepository:    pausedtask.NewRepository(),
	}, nil
}

// RunInTransaction executes the given function in a PostgreSQL read-write transaction.
func (s *Storage) RunInTransaction(ctx context.Context, fn func(ctx context.Context, tx *storage.Transaction) error) error {
	if s == nil || s.pool == nil {
		return wrapStatusError("postgres pool must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	if fn == nil {
		return wrapStatusError("transaction callback must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	return RunInTx(ctx, s.pool, func(txCtx context.Context, _ pgx.Tx) error {
		return fn(txCtx, s.newTransaction())
	})
}

func (s *Storage) newTransaction() *storage.Transaction {
	return &storage.Transaction{
		WorkUnits:      s.workUnitRepository.WithTx(),
		Experiments:    s.experimentRepository.WithTx(),
		XIDs:           s.xidRepository.WithTx(),
		StatusMessages: s.statusMessageRepository.WithTx(),
		StatusHistory:  s.statusHistoryRepository.WithTx(),
		PauseState:     s.pauseStateRepository.WithTx(),
		PausedTasks:    s.pausedTaskRepository.WithTx(),
	}
}

// PauseState returns the repository for global pause scheduling state.
func (s *Storage) PauseState() storage.PauseStateRepository {
	return s.pauseStateRepository
}

// PausedTasks returns the repository for pending paused actuation tasks.
func (s *Storage) PausedTasks() storage.PausedTaskRepository {
	return s.pausedTaskRepository
}
