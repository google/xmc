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

package workunit_test

import (
	"context"
	"errors"
	"testing"

	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/workunit"
)

func TestRepository_SaveWorkUnitExecution_ValidationAndPreconditions(t *testing.T) {
	ctx := context.Background()
	repo := &workunit.Repository{}

	if err := repo.SaveWorkUnitExecution(ctx, nil); !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("SaveWorkUnitExecution(nil) returned %v, want %v", err, status.ErrInvalidArgument)
	}

	exec := &essmsgpb.WorkUnitExecution{Name: "experiments/1/workUnits/1/executions/1"}
	if err := repo.SaveWorkUnitExecution(ctx, exec); !errors.Is(err, status.ErrFailedPrecondition) {
		t.Errorf("SaveWorkUnitExecution outside transaction returned %v, want %v", err, status.ErrFailedPrecondition)
	}
}

func TestRepository_UpdateCurrentWorkUnitExecution_ValidationAndPreconditions(t *testing.T) {
	ctx := context.Background()
	repo := &workunit.Repository{}

	if err := repo.UpdateCurrentWorkUnitExecution(ctx, 1, 1, 1, nil); !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("UpdateCurrentWorkUnitExecution(nil) returned %v, want %v", err, status.ErrInvalidArgument)
	}

	exec := &essmsgpb.WorkUnitExecution{Name: "experiments/1/workUnits/1/executions/1"}
	if err := repo.UpdateCurrentWorkUnitExecution(ctx, 1, 1, 1, exec); !errors.Is(err, status.ErrFailedPrecondition) {
		t.Errorf("UpdateCurrentWorkUnitExecution outside transaction returned %v, want %v", err, status.ErrFailedPrecondition)
	}
}
