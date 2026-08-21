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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

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

func (s *stubStore) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
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
			name: "SetComponentIntent_AIP122Name",
			call: func() error {
				_, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "components/comp-1", Spec: validSpec})
				return err
			},
			wantCode: codes.Unimplemented,
		},
		{
			name: "SetComponentIntent_ValidSemVer",
			call: func() error {
				spec := &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: "v1.2.3-alpha.1+build.10",
				}
				_, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "comp-1", Spec: spec})
				return err
			},
			wantCode: codes.Unimplemented,
		},
		{
			name: "SetComponentIntent_WithDependencies",
			call: func() error {
				spec := &componentspb.ComponentSpec{
					Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Manifests: []string{
						"apiVersion: v1\nkind: Service\nmetadata:\n  annotations:\n    xmc.dev/depends-on: dep-a",
					},
					Description: "@depends-on: dep-b",
				}
				_, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "comp-1", Spec: spec})
				return err
			},
			wantCode: codes.Unimplemented,
		},
		{
			name: "SetComponentIntent_InvalidSemVer",
			call: func() error {
				spec := &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: "invalid.semver.version",
				}
				_, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "comp-1", Spec: spec})
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "SetComponentIntent_InvalidName",
			call: func() error {
				_, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "-invalid-name-", Spec: validSpec})
				return err
			},
			wantCode: codes.InvalidArgument,
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
			name: "PrepareDeactivation_InvalidName",
			call: func() error {
				_, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{Name: "INVALID NAME"})
				return err
			},
			wantCode: codes.InvalidArgument,
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
			name: "ValidateComponentRequirement_ValidMinVersion",
			call: func() error {
				_, err := server.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{Name: "comp-1", MinimumVersion: "v1.0.0"})
				return err
			},
			wantCode: codes.Unimplemented,
		},
		{
			name: "ValidateComponentRequirement_InvalidMinVersion",
			call: func() error {
				_, err := server.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{Name: "comp-1", MinimumVersion: "invalid.min.ver"})
				return err
			},
			wantCode: codes.InvalidArgument,
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

func TestCMSServer_SetComponentIntent_MemoryStore(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	server := NewServer(ms)

	spec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "A test component",
	}

	// 1. ValidateOnly test
	valResp, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name:         "components/test-validate-only",
		Spec:         spec,
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("SetComponentIntent(validate_only=true) unexpected error: %v", err)
	}
	wantVal := &componentspb.SetComponentIntentResponse{
		Component: &componentspb.Component{
			Name: "components/test-validate-only",
			Spec: proto.Clone(spec).(*componentspb.ComponentSpec),
			Status: &componentspb.ComponentAccessStatus{
				State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			},
		},
		StatusMessage: "Validation successful (validate_only)",
	}
	if diff := cmp.Diff(wantVal, valResp, protocmp.Transform()); diff != "" {
		t.Errorf("SetComponentIntent(validate_only=true) mismatch (-want +got):\n%s", diff)
	}

	// Ensure validate_only did not persist to the store
	if _, err := ms.GetComponent(ctx, "test-validate-only"); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(test-validate-only) got code %v, want NotFound after validate_only", status.Code(err))
	}

	// Ensure validate_only did not persist to the store
	if _, err := ms.GetComponent(ctx, "test-validate-only"); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(test-validate-only) got code %v, want NotFound after validate_only", status.Code(err))
	}

	// 2. Normal creation test
	resp, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "components/test-comp",
		Spec: spec,
	})
	if err != nil {
		t.Fatalf("SetComponentIntent() unexpected error: %v", err)
	}
	if resp.GetComponent().GetName() != "components/test-comp" {
		t.Errorf("GetName() = %q, want 'components/test-comp'", resp.GetComponent().GetName())
	}
	if resp.GetComponent().GetSpec().GetGeneration() != 1 {
		t.Errorf("GetSpec().GetGeneration() = %d, want 1", resp.GetComponent().GetSpec().GetGeneration())
	}
	if resp.GetComponent().GetStatus().GetState() != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("GetStatus().GetState() = %v, want PENDING_RECONCILIATION", resp.GetComponent().GetStatus().GetState())
	}

	// 3. Update existing component test
	updatedSpec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.1.0",
		Description: "Updated description",
	}
	resp2, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "test-comp",
		Spec: updatedSpec,
	})
	if err != nil {
		t.Fatalf("SetComponentIntent(update) unexpected error: %v", err)
	}
	if resp2.GetComponent().GetSpec().GetGeneration() != 2 {
		t.Errorf("GetSpec().GetGeneration() = %d, want 2", resp2.GetComponent().GetSpec().GetGeneration())
	}
	if resp2.GetComponent().GetSpec().GetDescription() != "Updated description" {
		t.Errorf("GetSpec().GetDescription() = %q, want 'Updated description'", resp2.GetComponent().GetSpec().GetDescription())
	}
}

func TestCMSServer_SetComponentIntent_UpdateMask(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	server := NewServer(ms)

	// Create initial component
	initialSpec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Initial description",
	}
	_, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "comp-mask-test",
		Spec: initialSpec,
	})
	if err != nil {
		t.Fatalf("SetComponentIntent() initial create failed: %v", err)
	}

	// Partial update with update_mask only updating description
	updateSpec := &componentspb.ComponentSpec{
		Description: "Updated description only",
	}
	resp, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name:       "comp-mask-test",
		Spec:       updateSpec,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"description"}},
	})
	if err != nil {
		t.Fatalf("SetComponentIntent(update_mask) unexpected error: %v", err)
	}

	comp := resp.GetComponent()
	if comp.GetSpec().GetDescription() != "Updated description only" {
		t.Errorf("GetDescription() = %q, want 'Updated description only'", comp.GetSpec().GetDescription())
	}
	if comp.GetSpec().GetVersion() != "v1.0.0" {
		t.Errorf("GetVersion() preserved = %q, want 'v1.0.0'", comp.GetSpec().GetVersion())
	}
	if comp.GetSpec().GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
		t.Errorf("GetType() preserved = %v, want CUSTOM_1P_COMPONENT", comp.GetSpec().GetType())
	}

	// Non-existent component with update_mask returns NotFound (AIP-134 compliance)
	_, err = server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "comp-non-existent",
		Spec: &componentspb.ComponentSpec{
			Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Description: "Upserted description",
			Version:     "v2.0.0",
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"type", "description"}},
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("SetComponentIntent(update_mask non-existent) got code %v, want NotFound", status.Code(err))
	}
}
