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

package dbstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dbstore"
)

type missingComponentRepo struct {
	storage.ComponentRepository
}

func (missingComponentRepo) GetComponentWithFence(context.Context, string) (*store.ComponentRecord, *store.ClusterIdentity, error) {
	return nil, nil, store.ErrComponentNotFound
}

// serializableTxRecorder records whether each write operation on dbstore.Store runs through
// RunInSerializableTransaction rather than RunInTransaction.
type serializableTxRecorder struct {
	storage.Storage
	serializableCalls    int
	nonSerializableCalls int
}

var errTransactionRecorded = errors.New("transaction recorded")

func (r *serializableTxRecorder) Components() storage.ComponentRepository {
	return missingComponentRepo{}
}

func (r *serializableTxRecorder) RunInTransaction(context.Context, func(ctx context.Context, tx *storage.Transaction) error) error {
	r.nonSerializableCalls++
	return errTransactionRecorded
}

func (r *serializableTxRecorder) RunInSerializableTransaction(context.Context, func(ctx context.Context, tx *storage.Transaction) error) (time.Time, error) {
	r.serializableCalls++
	return time.Time{}, errTransactionRecorded
}

func TestStore_RunsTransactionsSerializable(t *testing.T) {
	ctx := context.Background()
	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}

	tests := []struct {
		name string
		call func(s *dbstore.Store) error
	}{
		{
			name: "SetComponentIntent",
			call: func(s *dbstore.Store) error {
				_, err := s.SetComponentIntent(ctx, "serializable-comp", spec, nil, 0)
				return err
			},
		},
		{
			name: "PrepareDeactivation",
			call: func(s *dbstore.Store) error {
				_, err := s.PrepareDeactivation(ctx, "serializable-comp", false)
				return err
			},
		},
		{
			name: "CompleteDeactivation",
			call: func(s *dbstore.Store) error {
				return s.CompleteDeactivation(ctx, "serializable-comp", false)
			},
		},
		{
			name: "UpdateObservedGeneration",
			call: func(s *dbstore.Store) error {
				_, err := s.UpdateObservedGeneration(ctx, "serializable-comp", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "")
				return err
			},
		},
		{
			name: "ExecuteDeletion",
			call: func(s *dbstore.Store) error {
				return s.ExecuteDeletion(ctx, "serializable-comp", componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, false)
			},
		},
		{
			name: "DeclareClusterEpoch",
			call: func(s *dbstore.Store) error {
				_, err := s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 1})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &serializableTxRecorder{}
			s, err := dbstore.New(recorder)
			if err != nil {
				t.Fatalf("dbstore.New() error = %v", err)
			}
			if err := tt.call(s); !errors.Is(err, errTransactionRecorded) {
				t.Fatalf("%s() error = %v, want %v", tt.name, err, errTransactionRecorded)
			}
			if recorder.serializableCalls != 1 || recorder.nonSerializableCalls != 0 {
				t.Errorf("serializableCalls=%d nonSerializableCalls=%d, want 1/0", recorder.serializableCalls, recorder.nonSerializableCalls)
			}
		})
	}
}
