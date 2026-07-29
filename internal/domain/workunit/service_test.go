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

package workunit

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
)

// mockStore is a lightweight, case-specific test double for Store interface.
type mockStore struct {
	createWorkUnitFn            func(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error)
	restartWorkUnitExecutionsFn func(ctx context.Context, requester string, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnitExecution, error)

	// Unused methods satisfying storage.WorkUnitStore interface
	fetchWorkUnitFn                 func(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error)
	listWorkUnitsFn                 func(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error)
	updateWorkUnitFn                func(ctx context.Context, update *storage.WorkUnitUpdate) (*essmsgpb.WorkUnit, *essmsgpb.WorkUnit, error)
	fetchCurrentWorkUnitExecutionFn func(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error)
	listWorkUnitExecutionsFn        func(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error)
	updateWorkUnitExecutionsFn      func(ctx context.Context, xid int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error)
}

func (m *mockStore) CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	if m.createWorkUnitFn != nil {
		return m.createWorkUnitFn(ctx, wu)
	}
	return wu, nil
}

func (m *mockStore) RestartWorkUnitExecutions(ctx context.Context, requester string, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	if m.restartWorkUnitExecutionsFn != nil {
		return m.restartWorkUnitExecutionsFn(ctx, requester, xid, wids)
	}
	return nil, nil
}

func (m *mockStore) FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error) {
	if m.fetchWorkUnitFn != nil {
		return m.fetchWorkUnitFn(ctx, xid, wid)
	}
	return nil, nil
}

func (m *mockStore) ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error) {
	if m.listWorkUnitsFn != nil {
		return m.listWorkUnitsFn(ctx, parent, pageSize, pageToken, filter, orderBy, staleness)
	}
	return nil, "", nil
}

func (m *mockStore) UpdateWorkUnit(ctx context.Context, update *storage.WorkUnitUpdate) (*essmsgpb.WorkUnit, *essmsgpb.WorkUnit, error) {
	if m.updateWorkUnitFn != nil {
		return m.updateWorkUnitFn(ctx, update)
	}
	return nil, nil, nil
}

func (m *mockStore) FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error) {
	if m.fetchCurrentWorkUnitExecutionFn != nil {
		return m.fetchCurrentWorkUnitExecutionFn(ctx, xid, wid)
	}
	return nil, nil
}

func (m *mockStore) ListWorkUnitExecutions(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error) {
	if m.listWorkUnitExecutionsFn != nil {
		return m.listWorkUnitExecutionsFn(ctx, xid, wid)
	}
	return nil, nil
}

func (m *mockStore) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	if m.updateWorkUnitExecutionsFn != nil {
		return m.updateWorkUnitExecutionsFn(ctx, xid, updates)
	}
	return nil, nil
}

func TestNewService_NilStore(t *testing.T) {
	svc, err := NewService(nil)
	if err == nil {
		t.Errorf("NewService(nil) succeeded, want error")
	}
	if svc != nil {
		t.Errorf("NewService(nil) returned non-nil service")
	}
}

func TestCreateWorkUnit_HappyPath(t *testing.T) {
	ctx := context.Background()
	inputWu := &essmsgpb.WorkUnit{
		Name:         "experiments/1001/workUnits/1",
		WorkUnitType: essmsgpb.WorkUnit_STANDARD,
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: "experiments/1001/workUnits/1/executions/1",
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RUNNING,
			},
		},
		Executable: &essmsgpb.Executable{},
	}

	mock := &mockStore{
		createWorkUnitFn: func(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
			return wu, nil
		},
	}

	svc, err := NewService(mock)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	created, err := svc.CreateWorkUnit(ctx, inputWu)
	if err != nil {
		t.Fatalf("CreateWorkUnit failed: %v", err)
	}

	if diff := cmp.Diff(inputWu, created, protocmp.Transform()); diff != "" {
		t.Errorf("CreateWorkUnit() returned unexpected diff (-want +got):\n%s", diff)
	}
}

func TestCreateWorkUnit_ValidationFailures(t *testing.T) {
	ctx := context.Background()
	mock := &mockStore{}
	svc, _ := NewService(mock)

	tests := []struct {
		name    string
		wu      *essmsgpb.WorkUnit
		wantErr bool
	}{
		{
			name:    "nil work unit",
			wu:      nil,
			wantErr: true,
		},
		{
			name: "nil current execution",
			wu: &essmsgpb.WorkUnit{
				Name: "experiments/1001/workUnits/1",
			},
			wantErr: true,
		},
		{
			name: "invalid work unit name",
			wu: &essmsgpb.WorkUnit{
				Name: "invalid-name",
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Name: "invalid-name/executions/1",
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateWorkUnit(ctx, tc.wu)
			if (err != nil) != tc.wantErr {
				t.Errorf("CreateWorkUnit() error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestCreateWorkUnit_StorageError(t *testing.T) {
	ctx := context.Background()
	inputWu := &essmsgpb.WorkUnit{
		Name: "experiments/1001/workUnits/1",
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: "experiments/1001/workUnits/1/executions/1",
		},
	}

	mock := &mockStore{
		createWorkUnitFn: func(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
			return nil, errors.New("db error")
		},
	}

	svc, _ := NewService(mock)
	_, err := svc.CreateWorkUnit(ctx, inputWu)
	if err == nil {
		t.Errorf("CreateWorkUnit() succeeded, want db error")
	}
}

func TestRestartWorkUnitExecutions_HappyPath(t *testing.T) {
	ctx := context.Background()
	xid := int64(1001)
	wids := []int64{1, 2}

	restartedExecs := map[int64]*essmsgpb.WorkUnitExecution{
		1: {
			Name: "experiments/1001/workUnits/1/executions/2",
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RESTARTING,
			},
		},
		2: {
			Name: "experiments/1001/workUnits/2/executions/2",
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RESTARTING,
			},
		},
	}

	mock := &mockStore{
		restartWorkUnitExecutionsFn: func(ctx context.Context, requester string, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnitExecution, error) {
			return restartedExecs, nil
		},
	}

	svc, err := NewService(mock)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	res, err := svc.RestartWorkUnitExecutions(ctx, "test@example.com", xid, wids)
	if err != nil {
		t.Fatalf("RestartWorkUnitExecutions failed: %v", err)
	}

	if len(res) != 2 {
		t.Errorf("RestartWorkUnitExecutions returned %d executions, want 2", len(res))
	}
}
