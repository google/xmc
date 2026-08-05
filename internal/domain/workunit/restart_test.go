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
	"testing"
	"time"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	domainwu "xmanager_cloud/internal/domain/workunit"
)

func TestRestartExecution_Success(t *testing.T) {
	xid := int64(100)
	wid := int64(200)
	currentEID := int64(1)
	requester := "user@example.com"
	now := time.Now()

	currentExec := &essmsgpb.WorkUnitExecution{
		Name:  "experiments/100/workUnits/200/executions/1",
		State: essmsgpb.WorkUnitExecution_ACTIVE,
		Status: &essmsgpb.WorkUnitStatus{
			State: essmsgpb.WorkUnitStatus_RUNNING,
			SystemMessage: &essmsgpb.StructuredMessage{
				UserFacingText: "running message",
			},
		},
	}

	result, err := domainwu.RestartExecution(xid, wid, currentExec, requester, now)
	if err != nil {
		t.Fatalf("RestartExecution failed unexpectedly: %v", err)
	}

	if currentExec.State != essmsgpb.WorkUnitExecution_ACTIVE {
		t.Errorf("currentExec.State was mutated to %v, want %v", currentExec.State, essmsgpb.WorkUnitExecution_ACTIVE)
	}

	if result.NewEID != currentEID+1 {
		t.Errorf("NewEID = %d, want %d", result.NewEID, currentEID+1)
	}

	if result.ArchivedExecution.State != essmsgpb.WorkUnitExecution_INACTIVE {
		t.Errorf("ArchivedExecution.State = %v, want %v", result.ArchivedExecution.State, essmsgpb.WorkUnitExecution_INACTIVE)
	}

	if result.NewExecution.State != essmsgpb.WorkUnitExecution_ACTIVE {
		t.Errorf("NewExecution.State = %v, want %v", result.NewExecution.State, essmsgpb.WorkUnitExecution_ACTIVE)
	}

	if result.NewExecution.Status.State != essmsgpb.WorkUnitStatus_RESTARTING {
		t.Errorf("NewExecution.Status.State = %v, want %v", result.NewExecution.Status.State, essmsgpb.WorkUnitStatus_RESTARTING)
	}

	expectedMsg := "Work unit is being restarted by user@example.com"
	if got := result.NewExecution.Status.SystemMessage.UserFacingText; got != expectedMsg {
		t.Errorf("UserFacingText = %q, want %q", got, expectedMsg)
	}

	if result.NewExecution.Status.SystemMessage.CreateTime == nil {
		t.Errorf("NewExecution.Status.SystemMessage.CreateTime is nil")
	}

	if result.StateDelta == nil {
		t.Fatal("StateDelta is nil")
	}

	if result.StateDelta.CurrentState.Number() != essmsgpb.WorkUnitStatus_RUNNING.Number() {
		t.Errorf("StateDelta.CurrentState = %v, want %v", result.StateDelta.CurrentState, essmsgpb.WorkUnitStatus_RUNNING)
	}

	if result.StateDelta.NewState.Number() != essmsgpb.WorkUnitStatus_RESTARTING.Number() {
		t.Errorf("StateDelta.NewState = %v, want %v", result.StateDelta.NewState, essmsgpb.WorkUnitStatus_RESTARTING)
	}
}

func TestRestartExecution_NilExecution(t *testing.T) {
	_, err := domainwu.RestartExecution(100, 200, nil, "user@example.com", time.Now())
	if err == nil {
		t.Errorf("RestartExecution(nil) succeeded, want error")
	}
}

func TestRestartExecution_InvalidExecutionName(t *testing.T) {
	currentExec := &essmsgpb.WorkUnitExecution{
		Name: "invalid-name",
	}

	_, err := domainwu.RestartExecution(100, 200, currentExec, "user@example.com", time.Now())
	if err == nil {
		t.Errorf("RestartExecution(invalid-name) succeeded, want error")
	}
}

func TestRestartExecution_ZeroEID(t *testing.T) {
	currentExec := &essmsgpb.WorkUnitExecution{
		Name: "experiments/100/workUnits/200/executions/0",
	}

	_, err := domainwu.RestartExecution(100, 200, currentExec, "user@example.com", time.Now())
	if err == nil {
		t.Errorf("RestartExecution(zero-eid) succeeded, want error")
	}
}

func TestRestartExecution_NilStatus(t *testing.T) {
	currentExec := &essmsgpb.WorkUnitExecution{
		Name:  "experiments/100/workUnits/200/executions/1",
		State: essmsgpb.WorkUnitExecution_ACTIVE,
	}

	result, err := domainwu.RestartExecution(100, 200, currentExec, "user@example.com", time.Now())
	if err != nil {
		t.Fatalf("RestartExecution failed unexpectedly: %v", err)
	}

	if result.StateDelta.CurrentState != nil {
		t.Errorf("StateDelta.CurrentState = %v, want nil", result.StateDelta.CurrentState)
	}
}
