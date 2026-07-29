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

// Package workunit provides domain-level operations for WorkUnit lifecycle management.
package workunit

import (
	"context"
	"fmt"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
)

// Store defines the storage operations required by the WorkUnit domain service.
// Satisfied by storage.Storage, Spanner repositories, or lightweight test stubs.
type Store interface {
	storage.WorkUnitStore
}

// Service provides domain-level operations for WorkUnit lifecycle management.
type Service struct {
	storage Store
}

// NewService creates a new WorkUnit domain service instance.
func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("storage must not be nil")
	}
	return &Service{storage: store}, nil
}

// CreateWorkUnit validates and creates a new WorkUnit via storage layer.
func (s *Service) CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	if wu == nil {
		return nil, fmt.Errorf("work unit must not be nil")
	}
	if wu.GetCurrentWorkUnitExecution() == nil {
		return nil, fmt.Errorf("current work unit execution must not be nil")
	}

	xid, _, err := validator.ValidateWorkUnitNameAllowZeroWID(wu.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid work unit name: %w", err)
	}

	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Domain: CreateWorkUnit", "work_unit", wu)

	createdWu, err := s.storage.CreateWorkUnit(ctx, wu)
	if err != nil {
		return nil, fmt.Errorf("failed to create work unit: %w", err)
	}
	if createdWu == nil {
		return nil, fmt.Errorf("storage returned nil work unit")
	}
	if createdWu.GetCurrentWorkUnitExecution() == nil {
		return nil, fmt.Errorf("created work unit has no current execution")
	}
	return createdWu, nil
}

// RestartWorkUnitExecutions validates and restarts executions for specified work unit IDs.
func (s *Service) RestartWorkUnitExecutions(ctx context.Context, requester string, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	if xid <= 0 {
		return nil, fmt.Errorf("invalid experiment ID: %d", xid)
	}
	if len(wids) == 0 {
		return nil, nil
	}

	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Domain: Restarting work unit executions", "wids", wids)

	newExecutions, err := s.storage.RestartWorkUnitExecutions(ctx, requester, xid, wids)
	if err != nil {
		return nil, fmt.Errorf("failed to restart work unit executions: %w", err)
	}
	return newExecutions, nil
}
