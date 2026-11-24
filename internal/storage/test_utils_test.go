// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package storage

import (
	"context"
	"errors"
	"testing"
	"time"
	lropb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/validator"
)

func TestMockStorage_RunInSerializableTransaction(t *testing.T) {
	ctx := context.Background()
	fixedTime := time.Date(2026, time.October, 29, 10, 0, 0, 0, time.UTC)
	wantErr := errors.New("tx failed")

	t.Run("returns configured CommitTime", func(t *testing.T) {
		m := &MockStorage{CommitTime: fixedTime}
		called := false
		got, err := m.RunInSerializableTransaction(ctx, func(context.Context, *Transaction) error {
			called = true
			return nil
		})
		if err != nil {
			t.Fatalf("RunInSerializableTransaction() error = %v", err)
		}
		if !called {
			t.Error("RunInSerializableTransaction() did not invoke callback")
		}
		if !got.Equal(fixedTime) {
			t.Errorf("CommitTime = %v, want %v", got, fixedTime)
		}
	})

	t.Run("defaults to current time when CommitTime is zero", func(t *testing.T) {
		before := time.Now()
		m := &MockStorage{}
		got, err := m.RunInSerializableTransaction(ctx, func(context.Context, *Transaction) error {
			return nil
		})
		after := time.Now()
		if err != nil {
			t.Fatalf("RunInSerializableTransaction() error = %v", err)
		}
		if got.Before(before) || got.After(after) {
			t.Errorf("CommitTime = %v, want within [%v, %v]", got, before, after)
		}
	})

	t.Run("propagates error with zero timestamp", func(t *testing.T) {
		m := &MockStorage{CommitTime: fixedTime, RunInTransactionErr: wantErr}
		got, err := m.RunInSerializableTransaction(ctx, func(context.Context, *Transaction) error {
			return nil
		})
		if !errors.Is(err, wantErr) {
			t.Errorf("RunInSerializableTransaction() error = %v, want %v", err, wantErr)
		}
		if !got.IsZero() {
			t.Errorf("CommitTime = %v, want zero time on error", got)
		}
	})
}

func TestMockStorage_AddWorkUnitOperation(t *testing.T) {
	ctx := context.Background()
	metadata := &essmsgpb.OperationMetadata{Target: "experiments/123/workUnits/456", ActionType: essmsgpb.OperationMetadata_RESTART}

	t.Run("defaults to a pending operation named after xid and wid", func(t *testing.T) {
		m := &MockStorage{}
		op, err := m.AddWorkUnitOperation(ctx, 123, 456, metadata)
		if err != nil {
			t.Fatalf("AddWorkUnitOperation() error = %v", err)
		}
		xid, wid, _, err := validator.ValidateOperationName(op.GetName())
		if err != nil || xid != 123 || wid != 456 {
			t.Errorf("AddWorkUnitOperation() name = %q (err %v), want an operation of XID 123, WID 456", op.GetName(), err)
		}
		if op.GetDone() {
			t.Error("AddWorkUnitOperation() done = true, want false")
		}
		gotMetadata := &essmsgpb.OperationMetadata{}
		if err := op.GetMetadata().UnmarshalTo(gotMetadata); err != nil {
			t.Fatalf("AddWorkUnitOperation() metadata unmarshal error = %v", err)
		}
		if diff := cmp.Diff(metadata, gotMetadata, protocmp.Transform()); diff != "" {
			t.Errorf("AddWorkUnitOperation() metadata mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("rejects nil metadata like real storage", func(t *testing.T) {
		m := &MockStorage{}
		got, err := m.AddWorkUnitOperation(ctx, 123, 456, nil)
		if !errors.Is(err, grpcerr.ErrInvalidArgument) || got != nil {
			t.Errorf("AddWorkUnitOperation(nil metadata) = %v, %v, want nil, %v", got, err, grpcerr.ErrInvalidArgument)
		}
	})

	t.Run("returns LROOp when set", func(t *testing.T) {
		want := &lropb.Operation{Name: "experiments/1/workUnits/2/operations/fixed"}
		m := &MockStorage{LROOp: want}
		got, err := m.AddWorkUnitOperation(ctx, 123, 456, metadata)
		if err != nil || got != want {
			t.Errorf("AddWorkUnitOperation() = %v, %v, want %v, nil", got, err, want)
		}
	})

	t.Run("returns AddWorkUnitOperationErr when set", func(t *testing.T) {
		wantErr := errors.New("add failed")
		m := &MockStorage{AddWorkUnitOperationErr: wantErr, LROOp: &lropb.Operation{}}
		got, err := m.AddWorkUnitOperation(ctx, 123, 456, metadata)
		if !errors.Is(err, wantErr) || got != nil {
			t.Errorf("AddWorkUnitOperation() = %v, %v, want nil, %v", got, err, wantErr)
		}
	})
}

func TestMockStorage_FetchUpdatingWorkUnits(t *testing.T) {
	ctx := context.Background()
	stored := &essmsgpb.WorkUnit{
		Name:                     "experiments/123/workUnits/456",
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/1"},
	}
	written := &essmsgpb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/2"}

	testCases := []struct {
		name          string
		storage       *MockStorage
		wantExecution string
	}{
		{
			name:          "configured work unit",
			storage:       &MockStorage{WorkUnit: stored},
			wantExecution: "experiments/123/workUnits/456/executions/1",
		},
		{
			name:          "inserted work unit when none is configured",
			storage:       &MockStorage{InsertedWorkUnit: stored},
			wantExecution: "experiments/123/workUnits/456/executions/1",
		},
		{
			name:          "reads the last written execution",
			storage:       &MockStorage{WorkUnit: stored, UpdatedWorkUnitExecutions: map[int64]*essmsgpb.WorkUnitExecution{456: written}},
			wantExecution: "experiments/123/workUnits/456/executions/2",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.storage.FetchUpdatingWorkUnits(ctx, 123, []int64{456})
			if err != nil {
				t.Fatalf("FetchUpdatingWorkUnits() error = %v", err)
			}
			if name := got[456].GetCurrentWorkUnitExecution().GetName(); name != tc.wantExecution {
				t.Errorf("FetchUpdatingWorkUnits() current execution = %q, want %q", name, tc.wantExecution)
			}
		})
	}

	t.Run("returns FetchWorkUnitErr when set", func(t *testing.T) {
		wantErr := errors.New("fetch failed")
		m := &MockStorage{WorkUnit: stored, FetchWorkUnitErr: wantErr}
		if got, err := m.FetchUpdatingWorkUnits(ctx, 123, []int64{456}); !errors.Is(err, wantErr) || got != nil {
			t.Errorf("FetchUpdatingWorkUnits() = %v, %v, want nil, %v", got, err, wantErr)
		}
	})

	t.Run("returns nil when WorkUnit and InsertedWorkUnit are unset", func(t *testing.T) {
		m := &MockStorage{}
		if got, err := m.FetchUpdatingWorkUnits(ctx, 123, []int64{456}); err != nil || got != nil {
			t.Errorf("FetchUpdatingWorkUnits() = %v, %v, want nil, nil", got, err)
		}
	})

	t.Run("overlays UpdatedWorkUnitExecutions only for matching WIDs", func(t *testing.T) {
		m := &MockStorage{
			WorkUnit:                  stored,
			UpdatedWorkUnitExecutions: map[int64]*essmsgpb.WorkUnitExecution{456: written},
		}
		got, err := m.FetchUpdatingWorkUnits(ctx, 123, []int64{456, 789})
		if err != nil {
			t.Fatalf("FetchUpdatingWorkUnits() error = %v", err)
		}
		want := map[int64]string{
			456: written.GetName(),
			789: stored.GetCurrentWorkUnitExecution().GetName(),
		}
		gotNames := map[int64]string{
			456: got[456].GetCurrentWorkUnitExecution().GetName(),
			789: got[789].GetCurrentWorkUnitExecution().GetName(),
		}
		if diff := cmp.Diff(want, gotNames); diff != "" {
			t.Errorf("FetchUpdatingWorkUnits() execution names mismatch (-want +got):\n%s", diff)
		}
	})
}
