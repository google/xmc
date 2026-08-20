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

func TestCMSServer_Scaffold_Handlers(t *testing.T) {
	ctx := context.Background()
	stub := &stubStore{}
	server := NewServer(stub)

	if server.Store() != stub {
		t.Errorf("Store() = %v, want %v", server.Store(), stub)
	}

	// 1. SetComponentIntent
	validSpec := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "comp-1", Spec: validSpec}); status.Code(err) != codes.Unimplemented {
		t.Errorf("SetComponentIntent(valid) got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := server.SetComponentIntent(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("SetComponentIntent(nil) got code %v, want InvalidArgument", status.Code(err))
	}
	if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: ""}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("SetComponentIntent(empty name) got code %v, want InvalidArgument", status.Code(err))
	}
	if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "comp-1", Spec: nil}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("SetComponentIntent(nil spec) got code %v, want InvalidArgument", status.Code(err))
	}

	// 2. PrepareDeactivation
	if _, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{Name: "comp-1"}); status.Code(err) != codes.Unimplemented {
		t.Errorf("PrepareDeactivation(valid) got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := server.PrepareDeactivation(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("PrepareDeactivation(nil) got code %v, want InvalidArgument", status.Code(err))
	}
	if _, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{Name: ""}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("PrepareDeactivation(empty name) got code %v, want InvalidArgument", status.Code(err))
	}

	// 3. CompleteDeactivation
	if _, err := server.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{Name: "comp-1"}); status.Code(err) != codes.Unimplemented {
		t.Errorf("CompleteDeactivation(valid) got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := server.CompleteDeactivation(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("CompleteDeactivation(nil) got code %v, want InvalidArgument", status.Code(err))
	}
	if _, err := server.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{Name: ""}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("CompleteDeactivation(empty name) got code %v, want InvalidArgument", status.Code(err))
	}

	// 4. UpdateObservedGeneration
	if _, err := server.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{Name: "comp-1", ObservedGeneration: 1}); status.Code(err) != codes.Unimplemented {
		t.Errorf("UpdateObservedGeneration(valid) got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := server.UpdateObservedGeneration(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateObservedGeneration(nil) got code %v, want InvalidArgument", status.Code(err))
	}
	if _, err := server.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{Name: ""}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateObservedGeneration(empty name) got code %v, want InvalidArgument", status.Code(err))
	}

	// 5. ExecuteDeletion
	if _, err := server.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{Name: "comp-1"}); status.Code(err) != codes.Unimplemented {
		t.Errorf("ExecuteDeletion(valid) got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := server.ExecuteDeletion(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("ExecuteDeletion(nil) got code %v, want InvalidArgument", status.Code(err))
	}
	if _, err := server.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{Name: ""}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("ExecuteDeletion(empty name) got code %v, want InvalidArgument", status.Code(err))
	}

	// 6. ResolveComponentSnapshot
	if _, err := server.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{Name: "comp-1"}); status.Code(err) != codes.Unimplemented {
		t.Errorf("ResolveComponentSnapshot(valid) got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := server.ResolveComponentSnapshot(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("ResolveComponentSnapshot(nil) got code %v, want InvalidArgument", status.Code(err))
	}
	if _, err := server.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{Name: ""}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("ResolveComponentSnapshot(empty name) got code %v, want InvalidArgument", status.Code(err))
	}

	// 7. ValidateComponentRequirement
	if _, err := server.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{Name: "comp-1"}); status.Code(err) != codes.Unimplemented {
		t.Errorf("ValidateComponentRequirement(valid) got code %v, want Unimplemented", status.Code(err))
	}
	if _, err := server.ValidateComponentRequirement(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("ValidateComponentRequirement(nil) got code %v, want InvalidArgument", status.Code(err))
	}
	if _, err := server.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{Name: ""}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("ValidateComponentRequirement(empty name) got code %v, want InvalidArgument", status.Code(err))
	}

	// 8. GetPlatformHealth
	if _, err := server.GetPlatformHealth(ctx, &componentspb.GetPlatformHealthRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("GetPlatformHealth(valid) got code %v, want Unimplemented", status.Code(err))
	}
}

func TestCMSServer_UninitializedStore(t *testing.T) {
	ctx := context.Background()
	uninitializedServer := NewServer(nil)

	if _, err := uninitializedServer.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "foo", Spec: &componentspb.ComponentSpec{}}); status.Code(err) != codes.Unavailable {
		t.Errorf("got %v, want Unavailable", status.Code(err))
	}
	if _, err := uninitializedServer.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{Name: "foo"}); status.Code(err) != codes.Unavailable {
		t.Errorf("got %v, want Unavailable", status.Code(err))
	}
	if _, err := uninitializedServer.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{Name: "foo"}); status.Code(err) != codes.Unavailable {
		t.Errorf("got %v, want Unavailable", status.Code(err))
	}
	if _, err := uninitializedServer.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{Name: "foo"}); status.Code(err) != codes.Unavailable {
		t.Errorf("got %v, want Unavailable", status.Code(err))
	}
	if _, err := uninitializedServer.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{Name: "foo"}); status.Code(err) != codes.Unavailable {
		t.Errorf("got %v, want Unavailable", status.Code(err))
	}
	if _, err := uninitializedServer.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{Name: "foo"}); status.Code(err) != codes.Unavailable {
		t.Errorf("got %v, want Unavailable", status.Code(err))
	}
	if _, err := uninitializedServer.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{Name: "foo"}); status.Code(err) != codes.Unavailable {
		t.Errorf("got %v, want Unavailable", status.Code(err))
	}
	if _, err := uninitializedServer.GetPlatformHealth(ctx, nil); status.Code(err) != codes.Unavailable {
		t.Errorf("got %v, want Unavailable", status.Code(err))
	}
}
