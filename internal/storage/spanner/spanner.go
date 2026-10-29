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
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner/artifact"
	"xmanager_cloud/internal/storage/spanner/clilogincode"
	"xmanager_cloud/internal/storage/spanner/component"
	"xmanager_cloud/internal/storage/spanner/dashboard"
	"xmanager_cloud/internal/storage/spanner/experiment"
	"xmanager_cloud/internal/storage/spanner/operation"
	"xmanager_cloud/internal/storage/spanner/pausedtask"
	"xmanager_cloud/internal/storage/spanner/pausestate"
	"xmanager_cloud/internal/storage/spanner/statushistory"
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
type StatusHistoryRepository = statushistory.Repository
type DashboardRepository = dashboard.Repository
type PauseStateRepository = pausestate.Repository
type PausedTaskRepository = pausedtask.Repository
type CLILoginCodeRepository = clilogincode.Repository

var _ storage.Storage = (*Storage)(nil)

// Storage implements the storage.Storage interface using Google Cloud Spanner.
type Storage struct {
	client *spanner.Client
	*ExperimentRepository
	*WorkUnitRepository
	*ArtifactRepository
	*XIDRepository
	*StatusMessageRepository
	*OperationRepository
	*StatusHistoryRepository
	*DashboardRepository
	pauseStateRepository   *PauseStateRepository
	pausedTaskRepository   *PausedTaskRepository
	cliLoginCodeRepository *CLILoginCodeRepository
	componentRepository    *component.Repository
}

// RunInTransaction executes the given function in a Spanner read-write transaction,
// passing a Transaction struct with repositories bound to the Spanner transaction.
func (s *Storage) RunInTransaction(ctx context.Context, fn func(ctx context.Context, tx *storage.Transaction) error) error {
	_, err := s.RunInTransactionWithCommitTime(ctx, fn)
	return err
}

// RunInTransactionWithCommitTime executes fn in a Spanner read-write transaction and returns the
// commit timestamp assigned by Spanner.
func (s *Storage) RunInTransactionWithCommitTime(ctx context.Context, fn func(ctx context.Context, tx *storage.Transaction) error) (time.Time, error) {
	if s == nil || s.client == nil {
		return time.Time{}, fmt.Errorf("spanner client must not be nil: %w", grpcerr.ErrFailedPrecondition)
	}
	if fn == nil {
		return time.Time{}, fmt.Errorf("transaction callback must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	ts, err := s.client.ReadWriteTransaction(ctx, func(txCtx context.Context, spannerTx *spanner.ReadWriteTransaction) error {
		return fn(txCtx, s.newTransaction(spannerTx))
	})
	if status.Code(err) == codes.AlreadyExists && !errors.Is(err, grpcerr.ErrAlreadyExists) {
		return time.Time{}, fmt.Errorf("%w: %v", grpcerr.ErrAlreadyExists, err)
	}
	if err != nil {
		return time.Time{}, err
	}
	return ts, nil
}

func (s *Storage) newTransaction(spannerTx *spanner.ReadWriteTransaction) *storage.Transaction {
	return &storage.Transaction{
		WorkUnits:      s.WorkUnitRepository.WithTx(spannerTx),
		Experiments:    s.ExperimentRepository.WithTx(spannerTx),
		XIDs:           s.XIDRepository.WithTx(spannerTx),
		StatusMessages: s.StatusMessageRepository.WithTx(spannerTx),
		StatusHistory:  s.StatusHistoryRepository.WithTx(spannerTx),
		PauseState:     s.pauseStateRepository.WithTx(spannerTx),
		PausedTasks:    s.pausedTaskRepository.WithTx(spannerTx),
		Components:     s.componentRepository.WithTx(spannerTx),
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

// Components returns the repository for component catalog persistence.
func (s *Storage) Components() storage.ComponentRepository {
	return s.componentRepository
}

// NewStorage creates a new instance of the Spanner-backed storage.
func NewStorage(client *spanner.Client) (*Storage, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil: %w", grpcerr.ErrInvalidArgument)
	}

	expRepo, err := experiment.NewRepository(client)
	if err != nil {
		return nil, fmt.Errorf("failed to create experiment repository: %w", err)
	}

	wuRepo, err := workunit.NewRepository(client)
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

	statusHistoryRepo := statushistory.NewRepository(client)

	dashboardRepo, err := dashboard.NewRepository(client)
	if err != nil {
		return nil, fmt.Errorf("failed to create dashboard repository: %w", err)
	}

	pauseStateRepo, err := pausestate.NewRepository(client)
	if err != nil {
		return nil, fmt.Errorf("failed to create pausestate repository: %w", err)
	}

	pausedTaskRepo, err := pausedtask.NewRepository(client)
	if err != nil {
		return nil, fmt.Errorf("failed to create pausedtask repository: %w", err)
	}

	cliLoginCodeRepo, err := clilogincode.NewRepository(client)
	if err != nil {
		return nil, fmt.Errorf("failed to create clilogincode repository: %w", err)
	}

	return &Storage{
		client:                  client,
		ExperimentRepository:    expRepo,
		WorkUnitRepository:      wuRepo,
		ArtifactRepository:      artifactRepo,
		XIDRepository:           xidRepo,
		StatusMessageRepository: statusMsgRepo,
		OperationRepository:     opRepo,
		StatusHistoryRepository: statusHistoryRepo,
		DashboardRepository:     dashboardRepo,
		pauseStateRepository:    pauseStateRepo,
		pausedTaskRepository:    pausedTaskRepo,
		cliLoginCodeRepository:  cliLoginCodeRepo,
		componentRepository:     component.NewRepository(client),
	}, nil
}
