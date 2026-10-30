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
package spanner

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/pkg/components/store"
)

func TestNewStorage_NilClient(t *testing.T) {
	if _, err := NewStorage(nil); err == nil {
		t.Errorf("NewStorage(nil) succeeded, want error")
	}
}

func TestStorage_RunInSerializableTransaction_MatchesStoredUpdateTime(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage() failed: %v", err)
	}

	rec := &store.ComponentRecord{
		ComponentID: 91001,
		Name:        "commit-time-comp",
		Spec: &componentspb.ComponentSpec{
			Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version:    "v1.0.0",
			Generation: 1,
		},
		Status: &componentspb.ComponentAccessStatus{
			State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		State:      componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Generation: 1,
	}

	commitTime, err := st.RunInSerializableTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		return tx.Components.InsertComponent(ctx, rec, nil)
	})
	if err != nil {
		t.Fatalf("RunInSerializableTransaction() failed: %v", err)
	}
	if commitTime.IsZero() {
		t.Fatal("RunInSerializableTransaction() returned zero timestamp")
	}

	stored, err := st.Components().GetComponentByID(ctx, rec.ComponentID)
	if err != nil {
		t.Fatalf("GetComponentByID() failed: %v", err)
	}
	if !commitTime.Equal(stored.UpdateTime) {
		t.Errorf("commitTime = %v, want stored UpdateTime %v", commitTime, stored.UpdateTime)
	}
	if !commitTime.Equal(stored.CreateTime) {
		t.Errorf("commitTime = %v, want stored CreateTime %v", commitTime, stored.CreateTime)
	}
}

func TestStorage_RunInTransaction_AlreadyExistsMapping(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	st, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage() failed: %v", err)
	}

	rec := &store.ComponentRecord{
		ComponentID: 91002,
		Name:        "duplicate-comp",
		Spec: &componentspb.ComponentSpec{
			Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version:    "v1.0.0",
			Generation: 1,
		},
		Status: &componentspb.ComponentAccessStatus{
			State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		State:      componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Generation: 1,
	}
	if err := st.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		return tx.Components.InsertComponent(ctx, rec, nil)
	}); err != nil {
		t.Fatalf("initial RunInTransaction() failed: %v", err)
	}

	// Inserting the same ComponentId again causes Spanner to fail commit with codes.AlreadyExists,
	// which RunInTransaction must wrap in grpcerr.ErrAlreadyExists.
	err = st.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		return tx.Components.InsertComponent(ctx, rec, nil)
	})
	if err == nil {
		t.Fatal("duplicate RunInTransaction() succeeded, want AlreadyExists error")
	}
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("status.Code(err) = %v, want %v", status.Code(err), codes.AlreadyExists)
	}
	if !errors.Is(err, grpcerr.ErrAlreadyExists) {
		t.Errorf("errors.Is(%v, grpcerr.ErrAlreadyExists) = false, want true", err)
	}
}

func TestStorage_RunInTransaction_Validation(t *testing.T) {
	ctx := context.Background()
	if _, err := (&Storage{}).RunInSerializableTransaction(ctx, func(context.Context, *storage.Transaction) error { return nil }); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("RunInSerializableTransaction() with nil client = %v, want ErrFailedPrecondition", err)
	}

	_, essClient := emulator.Setup(ctx, t)
	st, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage() failed: %v", err)
	}
	if _, err := st.RunInSerializableTransaction(ctx, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("RunInSerializableTransaction(nil) = %v, want ErrInvalidArgument", err)
	}
}
