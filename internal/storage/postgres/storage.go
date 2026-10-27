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
	"xmanager_cloud/internal/storage/postgres/clilogincode"
	"xmanager_cloud/internal/storage/postgres/dashboard"
	"xmanager_cloud/internal/storage/postgres/experiment"
	"xmanager_cloud/internal/storage/postgres/operation"
	"xmanager_cloud/internal/storage/postgres/pausedtask"
	"xmanager_cloud/internal/storage/postgres/pausestate"
	"xmanager_cloud/internal/storage/postgres/pgdb"
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
type cliLoginCodeRepository = clilogincode.Repository

var _ storage.Storage = (*Storage)(nil)

// Storage implements the storage.Storage interface using PostgreSQL.
type Storage struct {
	// beginner starts the transactions of RunInTransaction; NewStorage sets it to the pool.
	beginner txBeginner
	*experimentRepository
	*workUnitRepository
	*artifactRepository
	*xidRepository
	*statusMessageRepository
	*operationRepository
	*statusHistoryRepository
	*dashboardRepository
	pauseStateRepository   *pauseStateRepository
	pausedTaskRepository   *pausedTaskRepository
	cliLoginCodeRepository *cliLoginCodeRepository
}

// NewStorage creates a new instance of the PostgreSQL-backed storage.
func NewStorage(pool *pgxpool.Pool) (*Storage, error) {
	if pool == nil {
		return nil, pgdb.WrapStatusError("postgres pool must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	return &Storage{
		beginner:                pool,
		experimentRepository:    experiment.NewRepository(pool),
		workUnitRepository:      workunit.NewRepository(pool),
		artifactRepository:      artifact.NewRepository(pool),
		xidRepository:           xid.NewRepository(pool),
		statusMessageRepository: statusmessage.NewRepository(pool),
		operationRepository:     operation.NewRepository(pool),
		statusHistoryRepository: statushistory.NewRepository(pool),
		dashboardRepository:     dashboard.NewRepository(pool),
		pauseStateRepository:    pausestate.NewRepository(pool),
		pausedTaskRepository:    pausedtask.NewRepository(pool),
		cliLoginCodeRepository:  clilogincode.NewRepository(pool),
	}, nil
}

// RunInTransaction executes the given function in a PostgreSQL read-write transaction,
// passing a Transaction struct with repositories bound to the PostgreSQL transaction.
func (s *Storage) RunInTransaction(ctx context.Context, fn func(ctx context.Context, tx *storage.Transaction) error) error {
	if s == nil || s.beginner == nil {
		return pgdb.WrapStatusError("postgres pool must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	if fn == nil {
		return pgdb.WrapStatusError("transaction callback must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	return runInTx(ctx, s.beginner, defaultTxRetryConfig, func(txCtx context.Context, pgTx pgx.Tx) error {
		return fn(txCtx, s.newTransaction(pgTx))
	})
}

func (s *Storage) newTransaction(tx pgdb.DBTX) *storage.Transaction {
	return &storage.Transaction{
		WorkUnits:      s.workUnitRepository.WithTx(tx),
		Experiments:    s.experimentRepository.WithTx(tx),
		XIDs:           s.xidRepository.WithTx(tx),
		StatusMessages: s.statusMessageRepository.WithTx(tx),
		StatusHistory:  s.statusHistoryRepository.WithTx(tx),
		PauseState:     s.pauseStateRepository.WithTx(tx),
		PausedTasks:    s.pausedTaskRepository.WithTx(tx),
		Artifacts:      s.artifactRepository.WithTx(tx),
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

// CLILoginCodes returns the repository for one-time CLI login codes.
func (s *Storage) CLILoginCodes() storage.CLILoginCodeRepository {
	return s.cliLoginCodeRepository
}
