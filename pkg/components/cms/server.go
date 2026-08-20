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

// validateComponentRequest validates the component name format and verifies store initialization.
func (s *Server) validateComponentRequest(name string) (string, error) {
	if s.store == nil {
		return "", status.Error(codes.Unavailable, "component store is uninitialized")
	}
	canonical, err := validateComponentName(name)
	if err != nil {
		return "", err
	}
	return canonical, nil
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
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	canonicalName, err := s.validateComponentRequest(req.GetName())
	if err != nil {
		return nil, err
	}
	if req.GetSpec() == nil {
		return nil, status.Error(codes.InvalidArgument, "component spec is required")
	}
	if req.GetSpec().GetVersion() != "" {
		if _, err := store.Parse(req.GetSpec().GetVersion()); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid component version %q: %v", req.GetSpec().GetVersion(), err)
		}
	}
	// TODO(Task 2.5 / Task 2.7): Pass extracted dependencies to store.SetIntent to persist
	// into the ComponentDependencies relational table and validate against the DAG engine.
	_ = store.ExtractDependencies(req.GetSpec(), nil, canonicalName)
	return nil, status.Error(codes.Unimplemented, "method SetComponentIntent not implemented")
}

// PrepareDeactivation evaluates the safety of deactivating a component and calculates drain timeouts.
func (s *Server) PrepareDeactivation(ctx context.Context, req *componentspb.PrepareDeactivationRequest) (*componentspb.PrepareDeactivationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if _, err := s.validateComponentRequest(req.GetName()); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.Unimplemented, "method PrepareDeactivation not implemented")
}

// CompleteDeactivation finalizes component deactivation after draining is complete.
func (s *Server) CompleteDeactivation(ctx context.Context, req *componentspb.CompleteDeactivationRequest) (*componentspb.CompleteDeactivationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if _, err := s.validateComponentRequest(req.GetName()); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.Unimplemented, "method CompleteDeactivation not implemented")
}

// UpdateObservedGeneration records the controller-observed generation and access coordinates.
func (s *Server) UpdateObservedGeneration(ctx context.Context, req *componentspb.UpdateObservedGenerationRequest) (*componentspb.UpdateObservedGenerationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if _, err := s.validateComponentRequest(req.GetName()); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.Unimplemented, "method UpdateObservedGeneration not implemented")
}

// ExecuteDeletion executes component deletion according to policy and deletion protection.
func (s *Server) ExecuteDeletion(ctx context.Context, req *componentspb.ExecuteDeletionRequest) (*componentspb.ExecuteDeletionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if _, err := s.validateComponentRequest(req.GetName()); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.Unimplemented, "method ExecuteDeletion not implemented")
}

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
func (s *Server) ResolveComponentSnapshot(ctx context.Context, req *componentspb.ResolveComponentSnapshotRequest) (*componentspb.ResolveComponentSnapshotResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if _, err := s.validateComponentRequest(req.GetName()); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.Unimplemented, "method ResolveComponentSnapshot not implemented")
}

// ValidateComponentRequirement checks if a required component type and version exists and is active.
func (s *Server) ValidateComponentRequirement(ctx context.Context, req *componentspb.ValidateComponentRequirementRequest) (*componentspb.ValidateComponentRequirementResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if _, err := s.validateComponentRequest(req.GetName()); err != nil {
		return nil, err
	}
	if req.GetMinimumVersion() != "" {
		if _, err := store.Parse(req.GetMinimumVersion()); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid minimum version %q: %v", req.GetMinimumVersion(), err)
		}
	}
	return nil, status.Error(codes.Unimplemented, "method ValidateComponentRequirement not implemented")
}

// GetPlatformHealth retrieves overall system component health metrics and status.
func (s *Server) GetPlatformHealth(ctx context.Context, req *componentspb.GetPlatformHealthRequest) (*componentspb.GetPlatformHealthResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if err := s.checkStoreReady(); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.Unimplemented, "method GetPlatformHealth not implemented")
}
