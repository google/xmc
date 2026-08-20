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

package cms

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// stubStore implements store.Store for unit testing CMS scaffolding.
type stubStore struct{}

func (s *stubStore) SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s *stubStore) GetComponent(ctx context.Context, name string) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s *stubStore) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s *stubStore) ListComponents(ctx context.Context) ([]*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s *stubStore) PrepareDeactivation(ctx context.Context, name string, requestCascade bool) (*store.PrepareDeactivationResult, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s *stubStore) CompleteDeactivation(ctx context.Context, name string) error {
	return status.Error(codes.Unimplemented, "not implemented")
}

func (s *stubStore) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string) error {
	return status.Error(codes.Unimplemented, "not implemented")
}

func (s *stubStore) ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool) error {
	return status.Error(codes.Unimplemented, "not implemented")
}

func (s *stubStore) ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	return nil, nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s *stubStore) ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*store.RequirementValidationResult, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s *stubStore) GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestCMSServer_StoreAccessor(t *testing.T) {
	stub := &stubStore{}
	server := NewServer(stub)
	if server.Store() != stub {
		t.Errorf("Store() = %v, want %v", server.Store(), stub)
	}
}

func TestCMSServer_Scaffold_Handlers(t *testing.T) {
	ctx := context.Background()
	server := NewServer(&stubStore{})

	validSpec := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}

	tests := []struct {
		name     string
		call     func() error
		wantCode codes.Code
	}{
		// SetComponentIntent
		{
			name: "SetComponentIntent_Valid",
			call: func() error {
				_, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "comp-1", Spec: validSpec})
				return err
			},
			wantCode: codes.Unimplemented,
		},
		{
			name: "SetComponentIntent_NilRequest",
			call: func() error {
				_, err := server.SetComponentIntent(ctx, nil)
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "SetComponentIntent_EmptyName",
			call: func() error {
				_, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "", Spec: validSpec})
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "SetComponentIntent_NilSpec",
			call: func() error {
				_, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "comp-1", Spec: nil})
				return err
			},
			wantCode: codes.InvalidArgument,
		},

		// PrepareDeactivation
		{
			name: "PrepareDeactivation_Valid",
			call: func() error {
				_, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{Name: "comp-1"})
				return err
			},
			wantCode: codes.Unimplemented,
		},
		{
			name: "PrepareDeactivation_NilRequest",
			call: func() error {
				_, err := server.PrepareDeactivation(ctx, nil)
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "PrepareDeactivation_EmptyName",
			call: func() error {
				_, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{Name: ""})
				return err
			},
			wantCode: codes.InvalidArgument,
		},

		// CompleteDeactivation
		{
			name: "CompleteDeactivation_Valid",
			call: func() error {
				_, err := server.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{Name: "comp-1"})
				return err
			},
			wantCode: codes.Unimplemented,
		},
		{
			name: "CompleteDeactivation_NilRequest",
			call: func() error {
				_, err := server.CompleteDeactivation(ctx, nil)
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "CompleteDeactivation_EmptyName",
			call: func() error {
				_, err := server.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{Name: ""})
				return err
			},
			wantCode: codes.InvalidArgument,
		},

		// UpdateObservedGeneration
		{
			name: "UpdateObservedGeneration_Valid",
			call: func() error {
				_, err := server.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{Name: "comp-1", ObservedGeneration: 1})
				return err
			},
			wantCode: codes.Unimplemented,
		},
		{
			name: "UpdateObservedGeneration_NilRequest",
			call: func() error {
				_, err := server.UpdateObservedGeneration(ctx, nil)
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "UpdateObservedGeneration_EmptyName",
			call: func() error {
				_, err := server.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{Name: ""})
				return err
			},
			wantCode: codes.InvalidArgument,
		},

		// ExecuteDeletion
		{
			name: "ExecuteDeletion_Valid",
			call: func() error {
				_, err := server.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{Name: "comp-1"})
				return err
			},
			wantCode: codes.Unimplemented,
		},
		{
			name: "ExecuteDeletion_NilRequest",
			call: func() error {
				_, err := server.ExecuteDeletion(ctx, nil)
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "ExecuteDeletion_EmptyName",
			call: func() error {
				_, err := server.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{Name: ""})
				return err
			},
			wantCode: codes.InvalidArgument,
		},

		// ResolveComponentSnapshot
		{
			name: "ResolveComponentSnapshot_Valid",
			call: func() error {
				_, err := server.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{Name: "comp-1"})
				return err
			},
			wantCode: codes.Unimplemented,
		},
		{
			name: "ResolveComponentSnapshot_NilRequest",
			call: func() error {
				_, err := server.ResolveComponentSnapshot(ctx, nil)
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "ResolveComponentSnapshot_EmptyName",
			call: func() error {
				_, err := server.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{Name: ""})
				return err
			},
			wantCode: codes.InvalidArgument,
		},

		// ValidateComponentRequirement
		{
			name: "ValidateComponentRequirement_Valid",
			call: func() error {
				_, err := server.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{Name: "comp-1"})
				return err
			},
			wantCode: codes.Unimplemented,
		},
		{
			name: "ValidateComponentRequirement_NilRequest",
			call: func() error {
				_, err := server.ValidateComponentRequirement(ctx, nil)
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "ValidateComponentRequirement_EmptyName",
			call: func() error {
				_, err := server.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{Name: ""})
				return err
			},
			wantCode: codes.InvalidArgument,
		},

		// GetPlatformHealth
		{
			name: "GetPlatformHealth_Valid",
			call: func() error {
				_, err := server.GetPlatformHealth(ctx, &componentspb.GetPlatformHealthRequest{})
				return err
			},
			wantCode: codes.Unimplemented,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if status.Code(err) != tc.wantCode {
				t.Errorf("%s got code %v, want %v", tc.name, status.Code(err), tc.wantCode)
			}
		})
	}
}

func TestCMSServer_UninitializedStore(t *testing.T) {
	ctx := context.Background()
	uninitializedServer := NewServer(nil)

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "SetComponentIntent",
			call: func() error {
				_, err := uninitializedServer.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "foo", Spec: &componentspb.ComponentSpec{}})
				return err
			},
		},
		{
			name: "PrepareDeactivation",
			call: func() error {
				_, err := uninitializedServer.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{Name: "foo"})
				return err
			},
		},
		{
			name: "CompleteDeactivation",
			call: func() error {
				_, err := uninitializedServer.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{Name: "foo"})
				return err
			},
		},
		{
			name: "UpdateObservedGeneration",
			call: func() error {
				_, err := uninitializedServer.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{Name: "foo"})
				return err
			},
		},
		{
			name: "ExecuteDeletion",
			call: func() error {
				_, err := uninitializedServer.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{Name: "foo"})
				return err
			},
		},
		{
			name: "ResolveComponentSnapshot",
			call: func() error {
				_, err := uninitializedServer.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{Name: "foo"})
				return err
			},
		},
		{
			name: "ValidateComponentRequirement",
			call: func() error {
				_, err := uninitializedServer.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{Name: "foo"})
				return err
			},
		},
		{
			name: "GetPlatformHealth",
			call: func() error {
				_, err := uninitializedServer.GetPlatformHealth(ctx, &componentspb.GetPlatformHealthRequest{})
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if status.Code(err) != codes.Unavailable {
				t.Errorf("%s got code %v, want %v", tc.name, status.Code(err), codes.Unavailable)
			}
		})
	}
}
