// Copyright 2026 Google LLC
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

package workunit

import (
	"context"
	"testing"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestInsertWorkUnitExecution_NilExecution(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}

	err := r.InsertWorkUnitExecution(ctx, nil, nil)
	if err == nil || err.Error() != "work unit execution must not be nil" {
		t.Errorf("InsertWorkUnitExecution(nil execution) returned error: %v, want 'work unit execution must not be nil'", err)
	}
}

func TestInsertWorkUnitExecution_NilTransaction(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}

	execution := &essmsgpb.WorkUnitExecution{
		Name: "experiments/1001/workUnits/1/executions/1",
	}

	err := r.InsertWorkUnitExecution(ctx, nil, execution)
	if err == nil || err.Error() != "transaction must not be nil" {
		t.Errorf("InsertWorkUnitExecution(nil tx) returned error: %v, want 'transaction must not be nil'", err)
	}
}

func TestUpdateCurrentWorkUnitExecution_NilExecution(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}

	err := r.UpdateCurrentWorkUnitExecution(ctx, nil, 1001, 1, 1, nil)
	if err == nil || err.Error() != "work unit execution must not be nil" {
		t.Errorf("UpdateCurrentWorkUnitExecution(nil execution) returned error: %v, want 'work unit execution must not be nil'", err)
	}
}

func TestUpdateCurrentWorkUnitExecution_NilTransaction(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}

	execution := &essmsgpb.WorkUnitExecution{
		Name: "experiments/1001/workUnits/1/executions/1",
	}

	err := r.UpdateCurrentWorkUnitExecution(ctx, nil, 1001, 1, 1, execution)
	if err == nil || err.Error() != "transaction must not be nil" {
		t.Errorf("UpdateCurrentWorkUnitExecution(nil tx) returned error: %v, want 'transaction must not be nil'", err)
	}
}
