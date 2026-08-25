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

// Package cms implements the ComponentManagerService gRPC server for XMC.
package cms

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// Server implements the componentspb.ComponentManagerServiceServer interface.
type Server struct {
	componentspb.UnimplementedComponentManagerServiceServer
	store store.Store
}

// NewServer creates a new ComponentManagerService gRPC server backed by the provided Store.
func NewServer(s store.Store) *Server {
	return &Server{
		store: s,
	}
}

// Store returns the underlying store instance.
func (s *Server) Store() store.Store {
	return s.store
}

// validateComponentName validates and returns the canonical component name.
func validateComponentName(name string) (string, error) {
	canonical := store.CanonicalComponentName(name)
	if canonical == "" {
		return "", status.Error(codes.InvalidArgument, "component name is required")
	}
	if !store.IsValidComponentName(canonical) {
		return "", status.Errorf(codes.InvalidArgument, "invalid component name %q", name)
	}
	return canonical, nil
}

// namedRequest defines a constraint for protobuf requests containing a resource name.
type namedRequest interface {
	comparable
	GetName() string
}

// validateNamedRequest checks that the request is non-nil, the store is initialized,
// and the component name is valid, returning the canonical component name.
func validateNamedRequest[T namedRequest](s *Server, req T) (string, error) {
	var zero T
	if req == zero {
		return "", status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if err := s.checkStoreReady(); err != nil {
		return "", err
	}
	return validateComponentName(req.GetName())
}

// checkStoreReady verifies that the component store is initialized.
func (s *Server) checkStoreReady() error {
	if s.store == nil {
		return status.Error(codes.Unavailable, "component store is uninitialized")
	}
	return nil
}

// SetComponentIntent applies or updates component intent within the store.
func (s *Server) SetComponentIntent(ctx context.Context, req *componentspb.SetComponentIntentRequest) (*componentspb.SetComponentIntentResponse, error) {
	canonicalName, err := validateNamedRequest(s, req)
	if err != nil {
		return nil, err
	}
	if req.GetSpec() == nil {
		return nil, status.Error(codes.InvalidArgument, "component spec is required")
	}

	specToSet := req.GetSpec()
	if version := specToSet.GetVersion(); version != "" {
		if _, err := store.Parse(version); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid component version %q: %v", version, err)
		}
	}
	if req.GetValidateOnly() {
		protoComp := &componentspb.Component{
			Name: store.FullComponentName(canonicalName),
			Spec: proto.Clone(specToSet).(*componentspb.ComponentSpec),
			Status: &componentspb.ComponentAccessStatus{
				State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			},
		}
		return &componentspb.SetComponentIntentResponse{
			Component:     protoComp,
			StatusMessage: "Validation successful (validate_only)",
		}, nil
	}

	rec, err := s.store.SetComponentIntent(ctx, canonicalName, specToSet, nil, specToSet.GetGeneration())
	if err != nil {
		return nil, err
	}
	return &componentspb.SetComponentIntentResponse{
		Component: store.ToProtoComponent(rec),
	}, nil
}

// PrepareDeactivation evaluates the safety of deactivating a component and calculates drain timeouts.
// Note: To be implemented in subsequent component lifecycle tasks.
func (s *Server) PrepareDeactivation(ctx context.Context, req *componentspb.PrepareDeactivationRequest) (*componentspb.PrepareDeactivationResponse, error) {
	if _, err := validateNamedRequest(s, req); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.Unimplemented, "method PrepareDeactivation not implemented")
}

// CompleteDeactivation finalizes component deactivation after draining is complete.
// Note: To be implemented in subsequent component lifecycle tasks.
func (s *Server) CompleteDeactivation(ctx context.Context, req *componentspb.CompleteDeactivationRequest) (*componentspb.CompleteDeactivationResponse, error) {
	if _, err := validateNamedRequest(s, req); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.Unimplemented, "method CompleteDeactivation not implemented")
}

// UpdateObservedGeneration records the controller-observed generation and access coordinates.
// Note: Implemented in subsequent stacked PR (PR 3: feat/components-observed-gen-reconciliation).
func (s *Server) UpdateObservedGeneration(ctx context.Context, req *componentspb.UpdateObservedGenerationRequest) (*componentspb.UpdateObservedGenerationResponse, error) {
	if _, err := validateNamedRequest(s, req); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.Unimplemented, "method UpdateObservedGeneration not implemented (deferred to stacked PR 3)")
}

// ExecuteDeletion executes component deletion according to policy and deletion protection.
// Note: To be implemented in subsequent component lifecycle tasks.
func (s *Server) ExecuteDeletion(ctx context.Context, req *componentspb.ExecuteDeletionRequest) (*componentspb.ExecuteDeletionResponse, error) {
	if _, err := validateNamedRequest(s, req); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.Unimplemented, "method ExecuteDeletion not implemented")
}

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
// Note: To be implemented in subsequent snapshot resolution tasks.
func (s *Server) ResolveComponentSnapshot(ctx context.Context, req *componentspb.ResolveComponentSnapshotRequest) (*componentspb.ResolveComponentSnapshotResponse, error) {
	if _, err := validateNamedRequest(s, req); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.Unimplemented, "method ResolveComponentSnapshot not implemented")
}

// ValidateComponentRequirement checks if a required component type and version exists and is active.
// Note: To be implemented in subsequent dependency validation tasks.
func (s *Server) ValidateComponentRequirement(ctx context.Context, req *componentspb.ValidateComponentRequirementRequest) (*componentspb.ValidateComponentRequirementResponse, error) {
	if _, err := validateNamedRequest(s, req); err != nil {
		return nil, err
	}
	if minVersion := req.GetMinimumVersion(); minVersion != "" {
		if _, err := store.Parse(minVersion); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid minimum version %q: %v", minVersion, err)
		}
	}
	return nil, status.Error(codes.Unimplemented, "method ValidateComponentRequirement not implemented")
}

// GetPlatformHealth retrieves overall system component health metrics and status.
// Note: To be implemented in subsequent platform health monitoring tasks.
func (s *Server) GetPlatformHealth(ctx context.Context, req *componentspb.GetPlatformHealthRequest) (*componentspb.GetPlatformHealthResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if err := s.checkStoreReady(); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.Unimplemented, "method GetPlatformHealth not implemented")
}
