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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/pkg/components/store"
)

// stubStore implements store.Store for unit testing CMS scaffolding.
type stubStore struct{}

func (s *stubStore) SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s *stubStore) SetSourcedComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, src store.RequestSource) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s *stubStore) DeclareClusterEpoch(ctx context.Context, cluster store.ClusterIdentity) (*store.DeclarationResult, error) {
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

func (s *stubStore) CompleteDeactivation(ctx context.Context, name string, force bool) error {
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
	server := NewServer(&stubStore{}, WithAuthorizer(NewDisabledAuthorizer()))

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
	uninitializedServer := NewServer(nil, WithAuthorizer(NewDisabledAuthorizer()))

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
	server := NewServer(ms, WithAuthorizer(NewDisabledAuthorizer()))

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

func TestCMSServer_SetComponentIntent_UnchangedIntentIsNoop(t *testing.T) {
	spec := func(version string, gen int64, deps ...string) *componentspb.ComponentSpec {
		return &componentspb.ComponentSpec{
			Type:         componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version:      version,
			Generation:   gen,
			Dependencies: deps,
		}
	}
	tests := []struct {
		name      string
		reapply   *componentspb.ComponentSpec
		wantGen   int64
		wantState componentspb.ComponentState
	}{
		{name: "same spec and generation", reapply: spec("v1.0.0", 3, "redpanda"), wantGen: 3, wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED},
		{name: "dependency names canonicalize to the stored spec", reapply: spec("v1.0.0", 3, "components/redpanda", "redpanda"), wantGen: 3, wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED},
		{name: "changed spec", reapply: spec("v1.1.0", 3, "redpanda"), wantGen: 4, wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION},
		{name: "newer generation", reapply: spec("v1.0.0", 4, "redpanda"), wantGen: 4, wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			server := NewServer(store.NewMemoryStore(), WithAuthorizer(NewDisabledAuthorizer()))
			for _, req := range []*componentspb.SetComponentIntentRequest{
				{Name: "redpanda", Spec: spec("v1.0.0", 1)},
				{Name: "gateway", Spec: spec("v1.0.0", 3, "redpanda")},
			} {
				if _, err := server.SetComponentIntent(ctx, req); err != nil {
					t.Fatalf("seed SetComponentIntent(%s) error = %v", req.GetName(), err)
				}
			}
			if _, err := server.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
				Name: "gateway", ObservedGeneration: 3, State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			}); err != nil {
				t.Fatalf("UpdateObservedGeneration() error = %v", err)
			}

			resp, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "gateway", Spec: tt.reapply})
			if err != nil {
				t.Fatalf("SetComponentIntent() error = %v", err)
			}
			got := resp.GetComponent()
			if got.GetSpec().GetGeneration() != tt.wantGen || got.GetStatus().GetState() != tt.wantState {
				t.Errorf("generation = %d, state = %v; want %d, %v", got.GetSpec().GetGeneration(), got.GetStatus().GetState(), tt.wantGen, tt.wantState)
			}
		})
	}
}

func TestCMSServer_SetComponentIntent_Source(t *testing.T) {
	baseReq := &componentspb.SetComponentIntentRequest{
		Name: "redpanda",
		Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.0.0", Generation: 7},
		Source: &componentspb.RequestSource{
			Uid: "uid-a", Generation: 7, Namespace: "xmc",
			Cluster: &componentspb.ClusterIdentity{Uid: "11111111-1111-4111-8111-111111111111", Epoch: 1},
		},
	}
	tests := []struct {
		name       string
		seed       bool // Apply baseReq first.
		req        *componentspb.SetComponentIntentRequest
		wantCode   codes.Code
		wantReason string
		wantGen    int64
		wantOwner  *store.SourceOwner
	}{
		{
			name:      "source orders by CMS generation and records the owner",
			req:       baseReq,
			wantGen:   1,
			wantOwner: &store.SourceOwner{UID: "uid-a", Generation: 7, Namespace: "xmc", ClusterUID: "11111111-1111-4111-8111-111111111111", Epoch: 1},
		},
		{name: "no source orders by spec generation", req: prototest.Modify(baseReq, func(r *componentspb.SetComponentIntentRequest) { r.Source = nil }), wantGen: 7},
		{
			name:     "source without a cluster is rejected",
			req:      prototest.Modify(baseReq, func(r *componentspb.SetComponentIntentRequest) { r.Source.Cluster = nil }),
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "source without a namespace is rejected in validate_only",
			req:      prototest.Modify(baseReq, func(r *componentspb.SetComponentIntentRequest) { r.Source.Namespace, r.ValidateOnly = "", true }),
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "source with a cluster uid that is not a UUID is rejected",
			req:      prototest.Modify(baseReq, func(r *componentspb.SetComponentIntentRequest) { r.Source.Cluster.Uid = "cluster-1" }),
			wantCode: codes.InvalidArgument,
		},
		{
			name:       "expected_owner_uid without an active record carries the SOURCE_INCARNATION_MISMATCH ErrorInfo",
			req:        prototest.Modify(baseReq, func(r *componentspb.SetComponentIntentRequest) { r.Source.ExpectedOwnerUid = "uid-z" }),
			wantCode:   codes.FailedPrecondition,
			wantReason: store.ReasonSourceIncarnationMismatch,
		},
		{
			name: "changed intent from an older owner generation carries the STALE_GENERATION ErrorInfo",
			seed: true,
			req: prototest.Modify(baseReq, func(r *componentspb.SetComponentIntentRequest) {
				r.Spec.Version, r.Source.Generation = "v1.1.0", 6
			}),
			wantCode:   codes.FailedPrecondition,
			wantReason: store.ReasonStaleGeneration,
		},
		{
			name:       "source from another namespace carries the SOURCE_NAMESPACE_CONFLICT ErrorInfo",
			seed:       true,
			req:        prototest.Modify(baseReq, func(r *componentspb.SetComponentIntentRequest) { r.Source.Namespace = "other" }),
			wantCode:   codes.FailedPrecondition,
			wantReason: store.ReasonSourceNamespaceConflict,
		},
		{
			name: "another incarnation naming the owner in expected_owner_uid takes the component over",
			seed: true,
			req: prototest.Modify(baseReq, func(r *componentspb.SetComponentIntentRequest) {
				r.Source.Uid, r.Source.Generation, r.Source.ExpectedOwnerUid = "uid-b", 1, "uid-a"
			}),
			wantGen:   1,
			wantOwner: &store.SourceOwner{UID: "uid-b", Generation: 1, Namespace: "xmc", ClusterUID: "11111111-1111-4111-8111-111111111111", Epoch: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ms := store.NewMemoryStore()
			server := NewServer(ms, WithAuthorizer(NewDisabledAuthorizer()))
			if tt.seed {
				if _, err := server.SetComponentIntent(ctx, baseReq); err != nil {
					t.Fatalf("seed SetComponentIntent() error = %v", err)
				}
			}

			resp, err := server.SetComponentIntent(ctx, tt.req)
			if status.Code(err) != tt.wantCode || store.ErrorInfoReason(err) != tt.wantReason {
				t.Fatalf("SetComponentIntent() error = %v, want code %v and reason %q", err, tt.wantCode, tt.wantReason)
			}
			if err != nil {
				return
			}
			if got := resp.GetComponent().GetSpec().GetGeneration(); got != tt.wantGen {
				t.Errorf("generation = %d, want %d", got, tt.wantGen)
			}
			stored, err := ms.GetComponent(ctx, "redpanda")
			if err != nil {
				t.Fatalf("GetComponent() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantOwner, stored.Owner); diff != "" {
				t.Errorf("stored Owner mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCMSServer_DeclareClusterEpoch(t *testing.T) {
	const prodUID, drUID = "5a6b7c8d-1e2f-4a3b-8c4d-5e6f7a8b9c01", "5a6b7c8d-1e2f-4a3b-8c4d-5e6f7a8b9c02"
	prod := &componentspb.ClusterIdentity{Uid: prodUID, Name: "prod", Epoch: 2}
	dr := func(epoch int64) *componentspb.ClusterIdentity {
		return &componentspb.ClusterIdentity{Uid: drUID, Name: "dr", Epoch: epoch}
	}
	tests := []struct {
		name       string
		req        *componentspb.DeclareClusterEpochRequest
		wantCode   codes.Code
		wantReason string
		want       *componentspb.DeclareClusterEpochResponse
	}{
		{
			name: "active cluster refreshes its epoch",
			req:  &componentspb.DeclareClusterEpochRequest{Cluster: prod},
			want: &componentspb.DeclareClusterEpochResponse{ActiveCluster: prod, Outcome: componentspb.DeclareClusterEpochResponse_OUTCOME_REFRESHED},
		},
		{
			name: "active cluster raises its epoch",
			req:  &componentspb.DeclareClusterEpochRequest{Cluster: &componentspb.ClusterIdentity{Uid: prodUID, Name: "prod", Epoch: 3}},
			want: &componentspb.DeclareClusterEpochResponse{
				ActiveCluster: &componentspb.ClusterIdentity{Uid: prodUID, Name: "prod", Epoch: 3},
				Outcome:       componentspb.DeclareClusterEpochResponse_OUTCOME_RAISED,
			},
		},
		{
			name: "another cluster at a higher epoch takes the owned component over",
			req:  &componentspb.DeclareClusterEpochRequest{Cluster: dr(3)},
			want: &componentspb.DeclareClusterEpochResponse{ActiveCluster: dr(3), RecordsReassigned: 1, Outcome: componentspb.DeclareClusterEpochResponse_OUTCOME_TAKEOVER},
		},
		{
			name:       "another cluster at the active epoch is fenced",
			req:        &componentspb.DeclareClusterEpochRequest{Cluster: dr(2)},
			wantCode:   codes.FailedPrecondition,
			wantReason: store.ReasonClusterFenced,
		},
		{name: "cluster without a name is rejected", req: &componentspb.DeclareClusterEpochRequest{Cluster: &componentspb.ClusterIdentity{Uid: prodUID, Epoch: 2}}, wantCode: codes.InvalidArgument},
		{name: "cluster uid that is not a UUID is rejected", req: &componentspb.DeclareClusterEpochRequest{Cluster: &componentspb.ClusterIdentity{Uid: "cluster-1", Name: "prod", Epoch: 2}}, wantCode: codes.InvalidArgument},
		{name: "request without a cluster is rejected", req: &componentspb.DeclareClusterEpochRequest{}, wantCode: codes.InvalidArgument},
		{name: "nil request is rejected", wantCode: codes.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			server := NewServer(store.NewMemoryStore(), WithAuthorizer(NewDisabledAuthorizer()))
			if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
				Name:   "redpanda",
				Spec:   &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.0.0"},
				Source: &componentspb.RequestSource{Uid: "uid-a", Generation: 1, Namespace: "xmc", Cluster: prod},
			}); err != nil {
				t.Fatalf("seed SetComponentIntent() error = %v", err)
			}
			if _, err := server.DeclareClusterEpoch(ctx, &componentspb.DeclareClusterEpochRequest{Cluster: prod}); err != nil {
				t.Fatalf("seed DeclareClusterEpoch() error = %v", err)
			}

			got, err := server.DeclareClusterEpoch(ctx, tt.req)
			if status.Code(err) != tt.wantCode || store.ErrorInfoReason(err) != tt.wantReason {
				t.Fatalf("DeclareClusterEpoch() error = %v, want code %v and reason %q", err, tt.wantCode, tt.wantReason)
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("DeclareClusterEpoch() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCMSServer_SetComponentIntent_FencedCluster(t *testing.T) {
	ctx := context.Background()
	server := NewServer(store.NewMemoryStore())
	prod := &componentspb.ClusterIdentity{Uid: "5a6b7c8d-1e2f-4a3b-8c4d-5e6f7a8b9c01", Name: "prod", Epoch: 2}
	if _, err := server.DeclareClusterEpoch(ctx, &componentspb.DeclareClusterEpochRequest{Cluster: prod}); err != nil {
		t.Fatalf("DeclareClusterEpoch() error = %v", err)
	}

	_, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "redpanda",
		Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.0.0"},
		Source: &componentspb.RequestSource{Uid: "uid-a", Generation: 1, Namespace: "xmc", Cluster: &componentspb.ClusterIdentity{
			Uid: "5a6b7c8d-1e2f-4a3b-8c4d-5e6f7a8b9c02", Name: "dr", Epoch: 2,
		}},
	})
	if status.Code(err) != codes.FailedPrecondition || store.ErrorInfoReason(err) != store.ReasonClusterFenced {
		t.Fatalf("SetComponentIntent() error = %v, want FailedPrecondition with reason %q", err, store.ReasonClusterFenced)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, `cluster "dr"`) {
		t.Errorf("SetComponentIntent() message = %q, want it to name the source cluster \"dr\"", msg)
	}
}

func TestCMSServer_SetComponentIntent_SpecDependencies(t *testing.T) {
	ctx := context.Background()
	specWithDeps := func(deps ...string) *componentspb.ComponentSpec {
		return &componentspb.ComponentSpec{
			Type:         componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version:      "v1.0.0",
			Dependencies: deps,
		}
	}
	tests := []struct {
		name          string
		seedDependent bool
		req           *componentspb.SetComponentIntentRequest
		wantCode      codes.Code
		wantReason    string
		wantStoreDep  []string
	}{
		{
			name:         "dependencies pass through to the store graph",
			req:          &componentspb.SetComponentIntentRequest{Name: "ingestion-gateway", Spec: specWithDeps("redpanda")},
			wantCode:     codes.OK,
			wantStoreDep: []string{"redpanda"},
		},
		{
			name:         "prefixed and duplicate dependencies are persisted canonical",
			req:          &componentspb.SetComponentIntentRequest{Name: "ingestion-gateway", Spec: specWithDeps("components/redpanda", "redpanda")},
			wantCode:     codes.OK,
			wantStoreDep: []string{"redpanda"},
		},
		{
			name:     "invalid dependency name is rejected",
			req:      &componentspb.SetComponentIntentRequest{Name: "ingestion-gateway", Spec: specWithDeps("redpanda", "Bad_Name")},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "invalid dependency name is rejected in validate_only",
			req:      &componentspb.SetComponentIntentRequest{Name: "ingestion-gateway", Spec: specWithDeps(""), ValidateOnly: true},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "dependency with empty canonical name is rejected",
			req:      &componentspb.SetComponentIntentRequest{Name: "ingestion-gateway", Spec: specWithDeps("redpanda", "/")},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "self dependency is rejected",
			req:      &componentspb.SetComponentIntentRequest{Name: "components/ingestion-gateway", Spec: specWithDeps("redpanda", "components/ingestion-gateway")},
			wantCode: codes.InvalidArgument,
		},
		{
			name:          "cycle via spec.dependencies carries the DEPENDENCY_CYCLE ErrorInfo",
			seedDependent: true,
			req:           &componentspb.SetComponentIntentRequest{Name: "redpanda", Spec: specWithDeps("components/ingestion-gateway")},
			wantCode:      codes.FailedPrecondition,
			wantReason:    store.ReasonDependencyCycle,
		},
		{
			name:       "unregistered dependency carries the DEPENDENCY_NOT_REGISTERED ErrorInfo",
			req:        &componentspb.SetComponentIntentRequest{Name: "ingestion-gateway", Spec: specWithDeps("zookeeper")},
			wantCode:   codes.FailedPrecondition,
			wantReason: store.ReasonDependencyNotRegistered,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ms := store.NewMemoryStore()
			server := NewServer(ms, WithAuthorizer(NewDisabledAuthorizer()))
			if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "redpanda", Spec: specWithDeps()}); err != nil {
				t.Fatalf("SetComponentIntent(redpanda) unexpected error: %v", err)
			}
			if tc.seedDependent {
				if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "ingestion-gateway", Spec: specWithDeps("redpanda")}); err != nil {
					t.Fatalf("SetComponentIntent(ingestion-gateway) unexpected error: %v", err)
				}
			}
			_, err := server.SetComponentIntent(ctx, tc.req)
			if got := status.Code(err); got != tc.wantCode {
				t.Fatalf("SetComponentIntent() got code %v, want %v (err: %v)", got, tc.wantCode, err)
			}
			if got := store.ErrorInfoReason(err); got != tc.wantReason {
				t.Errorf("SetComponentIntent() ErrorInfo reason = %q, want %q", got, tc.wantReason)
			}
			if tc.wantStoreDep == nil {
				return
			}
			rec, err := ms.GetComponent(ctx, tc.req.GetName())
			if err != nil {
				t.Fatalf("GetComponent(%q) unexpected error: %v", tc.req.GetName(), err)
			}
			if diff := cmp.Diff(tc.wantStoreDep, rec.Dependencies); diff != "" {
				t.Errorf("store Dependencies mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantStoreDep, rec.Spec.GetDependencies()); diff != "" {
				t.Errorf("persisted spec.dependencies mismatch (-want +got):\n%s", diff)
			}
			if _, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{Name: "redpanda"}); status.Code(err) != codes.FailedPrecondition {
				t.Errorf("PrepareDeactivation(redpanda) got code %v, want FailedPrecondition (active dependent)", status.Code(err))
			}
		})
	}
}

func TestCMSServer_UpdateObservedGeneration_MemoryStore(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	server := NewServer(ms, WithAuthorizer(NewDisabledAuthorizer()))

	spec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "A component to observe",
	}

	// Create component first
	_, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "comp-observe",
		Spec: spec,
	})
	if err != nil {
		t.Fatalf("SetComponentIntent() unexpected error: %v", err)
	}

	// Update observed generation
	resp, err := server.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name:               "components/comp-observe",
		ObservedGeneration: 1,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		StatusMessage:      "Controller reconciliation succeeded",
	})
	if err != nil {
		t.Fatalf("UpdateObservedGeneration() unexpected error: %v", err)
	}

	comp := resp.GetComponent()
	if comp.GetName() != "components/comp-observe" {
		t.Errorf("GetName() = %q, want 'components/comp-observe'", comp.GetName())
	}
	if comp.GetStatus().GetObservedGeneration() != 1 {
		t.Errorf("GetStatus().GetObservedGeneration() = %d, want 1", comp.GetStatus().GetObservedGeneration())
	}
	if comp.GetStatus().GetState() != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("GetStatus().GetState() = %v, want ENABLED", comp.GetStatus().GetState())
	}
	if comp.GetStatus().GetHealthMessage() != "Controller reconciliation succeeded" {
		t.Errorf("GetStatus().GetHealthMessage() = %q, want 'Controller reconciliation succeeded'", comp.GetStatus().GetHealthMessage())
	}
	if comp.GetEtag() == "" {
		t.Error("GetEtag() is empty, expected updated etag")
	}

	// Update observed state to DELETED (verifying fresh record return without 404)
	delResp, err := server.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name:               "comp-observe",
		ObservedGeneration: 1,
		State:              componentspb.ComponentState_COMPONENT_STATE_DELETED,
		StatusMessage:      "Deletion completed",
	})
	if err != nil {
		t.Fatalf("UpdateObservedGeneration(DELETED) unexpected error: %v", err)
	}
	delComp := delResp.GetComponent()
	if delComp.GetStatus().GetState() != componentspb.ComponentState_COMPONENT_STATE_DELETED {
		t.Errorf("GetStatus().GetState() = %v, want DELETED", delComp.GetStatus().GetState())
	}
	if delComp.GetEtag() == "" || delComp.GetEtag() == comp.GetEtag() {
		t.Error("GetEtag() should be non-empty and refreshed upon deletion")
	}

	// Nonexistent component
	_, err = server.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name:               "components/comp-not-found",
		ObservedGeneration: 1,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("UpdateObservedGeneration(nonexistent) got code %v, want NotFound", status.Code(err))
	}

	// Negative observed generation
	_, err = server.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name:               "comp-observe",
		ObservedGeneration: -1,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateObservedGeneration(negative) got code %v, want InvalidArgument", status.Code(err))
	}

	// Invalid state enum
	_, err = server.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name:               "comp-observe",
		ObservedGeneration: 1,
		State:              componentspb.ComponentState(99),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateObservedGeneration(invalid state) got code %v, want InvalidArgument", status.Code(err))
	}
}

func TestCMSServer_SetComponentIntent_UpdateMask(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	server := NewServer(ms, WithAuthorizer(NewDisabledAuthorizer()))

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

func TestCMSServer_PrepareDeactivation_MemoryStore(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	server := NewServer(ms, WithAuthorizer(NewDisabledAuthorizer()))

	// Create root component
	specRoot := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Root",
	}
	if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "components/cms-root",
		Spec: specRoot,
	}); err != nil {
		t.Fatalf("SetComponentIntent(cms-root) failed: %v", err)
	}

	// Create dependent component
	specDep := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "@depends-on: cms-root",
	}
	if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "components/cms-dep",
		Spec: specDep,
	}); err != nil {
		t.Fatalf("SetComponentIntent(cms-dep) failed: %v", err)
	}

	// 1. Prepare deactivation without cascade -> fails with FailedPrecondition
	_, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{
		Name:  "components/cms-root",
		Force: false,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("PrepareDeactivation(no force) got code %v, want FailedPrecondition", status.Code(err))
	}

	// 2. Prepare deactivation with force (cascade) -> succeeds
	resp, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{
		Name:  "components/cms-root",
		Force: true,
	})
	if err != nil {
		t.Fatalf("PrepareDeactivation(force) unexpected error: %v", err)
	}
	if resp.GetState() != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
		t.Errorf("GetState() = %v, want DISABLING", resp.GetState())
	}
	if resp.GetDeactivationStartTime() == nil {
		t.Error("GetDeactivationStartTime() is nil, want timestamp")
	}
	if resp.GetMessage() == "" {
		t.Error("GetMessage() is empty, want informative message")
	}

	// 3. Prepare with custom draining timeout
	respTimeout, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{
		Name:            "components/cms-root",
		Force:           true,
		DrainingTimeout: durationpb.New(120 * time.Second),
	})
	if err != nil {
		t.Fatalf("PrepareDeactivation with timeout failed: %v", err)
	}
	if respTimeout.GetMessage() != "Component cms-root and 1 dependent(s) deactivation prepared successfully (drain: 120s)" {
		t.Errorf("GetMessage() = %q, want drain: 120s", respTimeout.GetMessage())
	}

	// 4. Prepare with sub-second draining timeout (rounds up to 1s)
	respSubSec, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{
		Name:            "components/cms-root",
		Force:           true,
		DrainingTimeout: durationpb.New(500 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("PrepareDeactivation with subsecond timeout failed: %v", err)
	}
	if respSubSec.GetMessage() != "Component cms-root and 1 dependent(s) deactivation prepared successfully (drain: 1s)" {
		t.Errorf("GetMessage() = %q, want drain: 1s", respSubSec.GetMessage())
	}

	// 5. Prepare with negative draining timeout -> InvalidArgument
	_, err = server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{
		Name:            "components/cms-root",
		DrainingTimeout: durationpb.New(-10 * time.Second),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("PrepareDeactivation(negative timeout) got code %v, want InvalidArgument", status.Code(err))
	}

	// 6. Prepare with malformed draining timeout (invalid nanos) -> InvalidArgument
	_, err = server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{
		Name: "components/cms-root",
		DrainingTimeout: &durationpb.Duration{
			Seconds: 10,
			Nanos:   -1,
		},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("PrepareDeactivation(malformed timeout) got code %v, want InvalidArgument", status.Code(err))
	}

	// 7. Prepare with huge draining timeout -> capped at MaxInt32 (2147483647s)
	respHuge, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{
		Name:  "components/cms-root",
		Force: true,
		DrainingTimeout: &durationpb.Duration{
			Seconds: 5000000000,
			Nanos:   500000000,
		},
	})
	if err != nil {
		t.Fatalf("PrepareDeactivation with huge timeout failed: %v", err)
	}
	if respHuge.GetMessage() != "Component cms-root and 1 dependent(s) deactivation prepared successfully (drain: 2147483647s)" {
		t.Errorf("GetMessage() = %q, want drain: 2147483647s", respHuge.GetMessage())
	}

	// 8. Nonexistent component -> NotFound
	_, err = server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{
		Name: "components/nonexistent",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("PrepareDeactivation(nonexistent) got code %v, want NotFound", status.Code(err))
	}
}

func TestCMSServer_CompleteDeactivation_MemoryStore(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	server := NewServer(ms, WithAuthorizer(NewDisabledAuthorizer()))

	spec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Deact test",
	}
	if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "components/cms-deact",
		Spec: spec,
	}); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	// 1. Complete without prepare -> FailedPrecondition
	_, err := server.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{
		Name: "components/cms-deact",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("CompleteDeactivation(unprepared) got code %v, want FailedPrecondition", status.Code(err))
	}

	// 2. Prepare first
	if _, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{
		Name: "components/cms-deact",
	}); err != nil {
		t.Fatalf("PrepareDeactivation failed: %v", err)
	}

	// 3. Complete deactivation -> succeeds
	resp, err := server.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{
		Name: "components/cms-deact",
	})
	if err != nil {
		t.Fatalf("CompleteDeactivation unexpected error: %v", err)
	}
	if resp.GetState() != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("GetState() = %v, want DISABLED", resp.GetState())
	}
	if resp.GetCompletionTime() == nil {
		t.Error("GetCompletionTime() is nil, want timestamp")
	}

	// 4. Test CompleteDeactivation with active dependent and force flag
	specParent := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Parent",
	}
	if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "components/cms-force-parent",
		Spec: specParent,
	}); err != nil {
		t.Fatalf("SetComponentIntent(parent) failed: %v", err)
	}
	specChild := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "@depends-on: cms-force-parent",
	}
	if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "components/cms-force-child",
		Spec: specChild,
	}); err != nil {
		t.Fatalf("SetComponentIntent(child) failed: %v", err)
	}
	if _, err := server.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{
		Name:  "components/cms-force-parent",
		Force: true,
	}); err != nil {
		t.Fatalf("PrepareDeactivation(parent) failed: %v", err)
	}
	// Complete without force while child is active -> FailedPrecondition
	_, err = server.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{
		Name:  "components/cms-force-parent",
		Force: false,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("CompleteDeactivation(parent without force) got code %v, want FailedPrecondition", status.Code(err))
	}
	// Complete with force=true -> succeeds
	respForce, err := server.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{
		Name:  "components/cms-force-parent",
		Force: true,
	})
	if err != nil {
		t.Fatalf("CompleteDeactivation(parent with force) unexpected error: %v", err)
	}
	if respForce.GetState() != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("GetState() = %v, want DISABLED", respForce.GetState())
	}

	// 5. Nonexistent component -> NotFound
	_, err = server.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{
		Name: "components/nonexistent",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("CompleteDeactivation(nonexistent) got code %v, want NotFound", status.Code(err))
	}
}

func TestCMSServer_ExecuteDeletion_MemoryStore(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	server := NewServer(ms, WithAuthorizer(NewDisabledAuthorizer()))

	// Create protected component
	spec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Protected",
	}
	if _, err := ms.SetComponentIntent(ctx, "cms-prot", spec, map[string]string{"xmc.dev/deletion-protection": "true"}, 0); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	// 1. Delete protected without force -> FailedPrecondition
	_, err := server.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{
		Name:           "components/cms-prot",
		DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_DELETE,
		Force:          false,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("ExecuteDeletion(protected, no force) got code %v, want FailedPrecondition", status.Code(err))
	}

	// 2. Delete protected with force -> succeeds
	resp, err := server.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{
		Name:           "components/cms-prot",
		DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_DELETE,
		Force:          true,
	})
	if err != nil {
		t.Fatalf("ExecuteDeletion(protected, force) unexpected error: %v", err)
	}
	if resp.GetState() != componentspb.ComponentState_COMPONENT_STATE_DELETED {
		t.Errorf("GetState() = %v, want DELETED", resp.GetState())
	}

	// 3. Dependent component deletion tests
	specRoot := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Root for deletion",
	}
	if _, err := ms.SetComponentIntent(ctx, "cms-root", specRoot, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}
	specDep := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "@depends-on: cms-root",
	}
	if _, err := ms.SetComponentIntent(ctx, "cms-dep", specDep, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	// 3a. Delete root with non-cascading policy without force -> FailedPrecondition
	_, err = server.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{
		Name:           "components/cms-root",
		DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
		Force:          false,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("ExecuteDeletion(with dep, orphan policy) got code %v, want FailedPrecondition", status.Code(err))
	}

	// 3b. Delete root with cascading policy -> succeeds
	resp, err = server.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{
		Name:           "components/cms-root",
		DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_DELETE,
		Force:          false,
	})
	if err != nil {
		t.Fatalf("ExecuteDeletion(cascading) unexpected error: %v", err)
	}
	if resp.GetState() != componentspb.ComponentState_COMPONENT_STATE_DELETED {
		t.Errorf("GetState() = %v, want DELETED", resp.GetState())
	}

	// 3c. Delete with DELETION_POLICY_UNSPECIFIED -> defaults to cascading delete
	if _, err := ms.SetComponentIntent(ctx, "cms-root-unspec", specRoot, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}
	specDepUnspec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "@depends-on: cms-root-unspec",
	}
	if _, err := ms.SetComponentIntent(ctx, "cms-dep-unspec", specDepUnspec, nil, 0); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}
	respUnspec, err := server.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{
		Name:           "components/cms-root-unspec",
		DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED,
		Force:          false,
	})
	if err != nil {
		t.Fatalf("ExecuteDeletion(unspecified policy) unexpected error: %v", err)
	}
	if respUnspec.GetState() != componentspb.ComponentState_COMPONENT_STATE_DELETED {
		t.Errorf("GetState() = %v, want DELETED", respUnspec.GetState())
	}
	if _, err := ms.GetComponent(ctx, "cms-dep-unspec"); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(cms-dep-unspec) got code %v, want NotFound", status.Code(err))
	}

	// 4. Nonexistent component -> NotFound
	_, err = server.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{
		Name: "components/nonexistent",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("ExecuteDeletion(nonexistent) got code %v, want NotFound", status.Code(err))
	}
}

func TestCMSServer_ResolveComponentSnapshot_MemoryStore(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	server := NewServer(ms, WithAuthorizer(NewDisabledAuthorizer()))

	spec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v1.0.0",
		Description: "Snap CMS test",
	}
	if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "components/cms-snap",
		Spec: spec,
	}); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}

	coords := &componentspb.ResolvedComponentCoordinates{
		ClusterName: "gke-cluster",
		Namespace:   "default",
		ServiceName: "cms-snap-svc",
		Port:        8080,
	}
	if _, err := ms.UpdateObservedGeneration(ctx, "cms-snap", 1, &componentspb.ComponentAccessStatus{Coordinates: coords, Ready: true}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "OK"); err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	// 1. Success path
	resp, err := server.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{
		Name:       "components/cms-snap",
		Generation: proto.Int64(1),
	})
	if err != nil {
		t.Fatalf("ResolveComponentSnapshot unexpected error: %v", err)
	}
	if resp.GetComponent().GetName() != "components/cms-snap" {
		t.Errorf("GetName() = %q, want 'components/cms-snap'", resp.GetComponent().GetName())
	}
	if resp.GetState() != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("GetState() = %v, want ENABLED", resp.GetState())
	}
	if resp.GetCoordinates().GetServiceName() != "cms-snap-svc" {
		t.Errorf("Coordinates.ServiceName = %q, want 'cms-snap-svc'", resp.GetCoordinates().GetServiceName())
	}

	// 2. Negative generation -> InvalidArgument
	_, err = server.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{
		Name:       "components/cms-snap",
		Generation: proto.Int64(-1),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ResolveComponentSnapshot(negative gen) got code %v, want InvalidArgument", status.Code(err))
	}

	// 3. Nonexistent component -> NotFound
	_, err = server.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{
		Name: "components/nonexistent",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("ResolveComponentSnapshot(nonexistent) got code %v, want NotFound", status.Code(err))
	}
}

func TestCMSServer_ValidateComponentRequirement_MemoryStore(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	server := NewServer(ms, WithAuthorizer(NewDisabledAuthorizer()))

	spec := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "v2.1.0",
		Description: "Req CMS test",
	}
	if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: "components/cms-req",
		Spec: spec,
	}); err != nil {
		t.Fatalf("SetComponentIntent failed: %v", err)
	}
	if _, err := ms.UpdateObservedGeneration(ctx, "cms-req", 1, &componentspb.ComponentAccessStatus{Ready: true}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "OK"); err != nil {
		t.Fatalf("UpdateObservedGeneration failed: %v", err)
	}

	// 1. Satisfied
	resp, err := server.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{
		Name:           "components/cms-req",
		RequiredType:   componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		MinimumVersion: "v2.0.0",
	})
	if err != nil {
		t.Fatalf("ValidateComponentRequirement unexpected error: %v", err)
	}
	if !resp.GetSatisfied() {
		t.Errorf("GetSatisfied() = false, want true (reason: %q)", resp.GetReason())
	}
	if resp.GetComponent() == nil || resp.GetComponent().GetName() != "components/cms-req" {
		t.Errorf("GetComponent() = %v, want 'components/cms-req'", resp.GetComponent())
	}

	// 2. Unsatisfied (higher version)
	respUnsat, err := server.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{
		Name:           "components/cms-req",
		RequiredType:   componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		MinimumVersion: "v3.0.0",
	})
	if err != nil {
		t.Fatalf("ValidateComponentRequirement unsatisfied check failed: %v", err)
	}
	if respUnsat.GetSatisfied() {
		t.Error("GetSatisfied() = true for v3.0.0 min version, want false")
	}

	// 3. Invalid minimum version -> InvalidArgument
	_, err = server.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{
		Name:           "components/cms-req",
		MinimumVersion: "invalid.semver.string",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ValidateComponentRequirement(invalid semver) got code %v, want InvalidArgument", status.Code(err))
	}

	// 4. Invalid required type enum -> InvalidArgument
	_, err = server.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{
		Name:         "components/cms-req",
		RequiredType: componentspb.ComponentType(99),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ValidateComponentRequirement(invalid type) got code %v, want InvalidArgument", status.Code(err))
	}
}

func TestCMSServer_GetPlatformHealth_MemoryStore(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	server := NewServer(ms, WithAuthorizer(NewDisabledAuthorizer()))

	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("cms-health-%d", i)
		spec := &componentspb.ComponentSpec{
			Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version: "v1.0.0",
		}
		if _, err := server.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
			Name: name,
			Spec: spec,
		}); err != nil {
			t.Fatalf("SetComponentIntent(%s) failed: %v", name, err)
		}
		if _, err := ms.UpdateObservedGeneration(ctx, name, 1, &componentspb.ComponentAccessStatus{Ready: true}, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "OK"); err != nil {
			t.Fatalf("UpdateObservedGeneration(%s) failed: %v", name, err)
		}
	}

	// 1. Success path
	resp, err := server.GetPlatformHealth(ctx, &componentspb.GetPlatformHealthRequest{
		PageSize: 2,
	})
	if err != nil {
		t.Fatalf("GetPlatformHealth failed: %v", err)
	}
	if len(resp.GetComponents()) != 2 {
		t.Errorf("len(Components) = %d, want 2", len(resp.GetComponents()))
	}
	if resp.GetTotalHealthy() != 3 {
		t.Errorf("TotalHealthy = %d, want 3", resp.GetTotalHealthy())
	}
	if resp.GetTotalUnhealthy() != 0 {
		t.Errorf("TotalUnhealthy = %d, want 0", resp.GetTotalUnhealthy())
	}
	if resp.GetNextPageToken() == "" {
		t.Error("NextPageToken is empty, want token")
	}

	// 2. Negative page size -> InvalidArgument
	_, err = server.GetPlatformHealth(ctx, &componentspb.GetPlatformHealthRequest{
		PageSize: -1,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetPlatformHealth(negative page size) got code %v, want InvalidArgument", status.Code(err))
	}

	// 3. Nil request -> InvalidArgument
	_, err = server.GetPlatformHealth(ctx, nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetPlatformHealth(nil request) got code %v, want InvalidArgument", status.Code(err))
	}

	// 4. Invalid filter -> InvalidArgument
	_, err = server.GetPlatformHealth(ctx, &componentspb.GetPlatformHealthRequest{
		Filter: "invalid_key=value",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetPlatformHealth(invalid filter) got code %v, want InvalidArgument", status.Code(err))
	}
}
