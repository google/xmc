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

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/pkg/components/store"
)

func TestNewPostgresStore(t *testing.T) {
	t.Run("NilPoolReturnsInvalidArgument", func(t *testing.T) {
		s, err := NewPostgresStore(nil)
		if err == nil {
			t.Fatalf("NewPostgresStore(nil) = %v, want error", s)
		}
		if !errors.Is(err, grpcerr.ErrInvalidArgument) {
			t.Errorf("errors.Is(%v, grpcerr.ErrInvalidArgument) = false, want true", err)
		}
		if got := status.Code(err); got != codes.InvalidArgument {
			t.Errorf("status.Code(%v) = %v, want %v", err, got, codes.InvalidArgument)
		}
	})

	t.Run("BindsPool", func(t *testing.T) {
		pool := &pgxpool.Pool{}
		s, err := NewPostgresStore(pool)
		if err != nil {
			t.Fatalf("NewPostgresStore(pool) unexpected error: %v", err)
		}
		if s == nil || s.pool != pool {
			t.Errorf("NewPostgresStore(pool) = %+v, want pool %p", s, pool)
		}
	})
}

func TestPostgresStore_Unimplemented(t *testing.T) {
	s, err := NewPostgresStore(&pgxpool.Pool{})
	if err != nil {
		t.Fatalf("NewPostgresStore() failed: %v", err)
	}

	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "SetComponentIntent",
			call: func() error {
				_, err := s.SetComponentIntent(ctx, "comp-a", nil, nil, 1)
				return err
			},
		},
		{
			name: "SetSourcedComponentIntent",
			call: func() error {
				_, err := s.SetSourcedComponentIntent(ctx, "comp-a", nil, nil, store.RequestSource{})
				return err
			},
		},
		{
			name: "DeclareClusterEpoch",
			call: func() error {
				_, err := s.DeclareClusterEpoch(ctx, store.ClusterIdentity{})
				return err
			},
		},
		{
			name: "GetComponent",
			call: func() error {
				_, err := s.GetComponent(ctx, "comp-a")
				return err
			},
		},
		{
			name: "GetComponentByID",
			call: func() error {
				_, err := s.GetComponentByID(ctx, 1)
				return err
			},
		},
		{
			name: "ListComponents",
			call: func() error {
				_, err := s.ListComponents(ctx)
				return err
			},
		},
		{
			name: "PrepareDeactivation",
			call: func() error {
				_, err := s.PrepareDeactivation(ctx, "comp-a", false)
				return err
			},
		},
		{
			name: "CompleteDeactivation",
			call: func() error {
				return s.CompleteDeactivation(ctx, "comp-a", false)
			},
		},
		{
			name: "UpdateObservedGeneration",
			call: func() error {
				_, err := s.UpdateObservedGeneration(ctx, "comp-a", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "ok")
				return err
			},
		},
		{
			name: "ExecuteDeletion",
			call: func() error {
				return s.ExecuteDeletion(ctx, "comp-a", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false)
			},
		},
		{
			name: "ResolveComponentSnapshot",
			call: func() error {
				_, _, err := s.ResolveComponentSnapshot(ctx, "comp-a", 1)
				return err
			},
		},
		{
			name: "ValidateComponentRequirement",
			call: func() error {
				_, err := s.ValidateComponentRequirement(ctx, "comp-a", componentspb.ComponentType_COMPONENT_TYPE_UNSPECIFIED, "")
				return err
			},
		},
		{
			name: "GetPlatformHealth",
			call: func() error {
				_, err := s.GetPlatformHealth(ctx, "", 10, "")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if got := status.Code(err); got != codes.Unimplemented {
				t.Errorf("%s code = %v, want %v (err: %v)", tt.name, got, codes.Unimplemented, err)
			}
		})
	}
}
