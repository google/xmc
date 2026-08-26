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
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

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
	if req.GetUpdateMask() != nil && len(req.GetUpdateMask().GetPaths()) > 0 {
		existingRec, err := s.store.GetComponent(ctx, canonicalName)
		if err != nil {
			return nil, err
		}
		specToSet = store.ApplySpecUpdateMask(existingRec.Spec, req.GetSpec(), req.GetUpdateMask().GetPaths())
	}

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

// PrepareDeactivation evaluates the safety of deactivating a component and initiates draining.
func (s *Server) PrepareDeactivation(ctx context.Context, req *componentspb.PrepareDeactivationRequest) (*componentspb.PrepareDeactivationResponse, error) {
	canonicalName, err := validateNamedRequest(s, req)
	if err != nil {
		return nil, err
	}
	if dt := req.GetDrainingTimeout(); dt != nil && dt.IsValid() && dt.AsDuration() < 0 {
		return nil, status.Error(codes.InvalidArgument, "draining timeout cannot be negative")
	}

	res, err := s.store.PrepareDeactivation(ctx, canonicalName, req.GetForce())
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, status.Error(codes.Internal, "store returned nil result without error")
	}

	drainSec := res.RecommendedDrainSec
	if dt := req.GetDrainingTimeout(); dt != nil && dt.IsValid() && dt.AsDuration() > 0 {
		drainSec = int32(dt.AsDuration() / time.Second)
	}

	msg := fmt.Sprintf("Component %s deactivation prepared successfully (drain: %ds)", canonicalName, drainSec)
	if len(res.CascadedDeactivations) > 0 {
		msg = fmt.Sprintf("Component %s and %d dependent(s) deactivation prepared successfully (drain: %ds)", canonicalName, len(res.CascadedDeactivations), drainSec)
	}

	return &componentspb.PrepareDeactivationResponse{
		State:                 componentspb.ComponentState_COMPONENT_STATE_DISABLING,
		DeactivationStartTime: timestamppb.New(time.Now().UTC()),
		Message:               msg,
	}, nil
}

// CompleteDeactivation finalizes component deactivation after draining is complete.
func (s *Server) CompleteDeactivation(ctx context.Context, req *componentspb.CompleteDeactivationRequest) (*componentspb.CompleteDeactivationResponse, error) {
	canonicalName, err := validateNamedRequest(s, req)
	if err != nil {
		return nil, err
	}

	if err := s.store.CompleteDeactivation(ctx, canonicalName); err != nil {
		return nil, err
	}

	return &componentspb.CompleteDeactivationResponse{
		State:          componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		CompletionTime: timestamppb.New(time.Now().UTC()),
		Message:        fmt.Sprintf("Component %s deactivation completed successfully", canonicalName),
	}, nil
}

// UpdateObservedGeneration records the controller-observed generation and access coordinates.
func (s *Server) UpdateObservedGeneration(ctx context.Context, req *componentspb.UpdateObservedGenerationRequest) (*componentspb.UpdateObservedGenerationResponse, error) {
	canonicalName, err := validateNamedRequest(s, req)
	if err != nil {
		return nil, err
	}
	if req.GetObservedGeneration() < 0 {
		return nil, status.Error(codes.InvalidArgument, "observed generation cannot be negative")
	}
	if req.GetState() < componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED || req.GetState() > componentspb.ComponentState_COMPONENT_STATE_DELETED {
		return nil, status.Errorf(codes.InvalidArgument, "invalid component state %d", req.GetState())
	}

	rec, err := s.store.UpdateObservedGeneration(ctx, canonicalName, req.GetObservedGeneration(), nil, req.GetState(), req.GetStatusMessage())
	if err != nil {
		return nil, err
	}
	return &componentspb.UpdateObservedGenerationResponse{
		Component: store.ToProtoComponent(rec),
	}, nil
}

// ExecuteDeletion executes component deletion according to policy and deletion protection.
func (s *Server) ExecuteDeletion(ctx context.Context, req *componentspb.ExecuteDeletionRequest) (*componentspb.ExecuteDeletionResponse, error) {
	canonicalName, err := validateNamedRequest(s, req)
	if err != nil {
		return nil, err
	}

	policy := req.GetDeletionPolicy()
	if policy == componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED {
		policy = componentspb.DeletionPolicy_DELETION_POLICY_DELETE
	}

	if err := s.store.ExecuteDeletion(ctx, canonicalName, policy, req.GetForce()); err != nil {
		return nil, err
	}

	return &componentspb.ExecuteDeletionResponse{
		State:   componentspb.ComponentState_COMPONENT_STATE_DELETED,
		Message: fmt.Sprintf("Component %s deleted successfully", canonicalName),
	}, nil
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
