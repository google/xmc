// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package cms implements the ComponentManagerService gRPC control plane.
package cms

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

var _ componentspb.ComponentManagerServiceServer = (*Server)(nil)

// Server implements ComponentManagerServiceServer.
type Server struct {
	componentspb.UnimplementedComponentManagerServiceServer
	store store.Store
}

// NewServer creates a new Server instance backed by the provided Store.
func NewServer(s store.Store) (*Server, error) {
	if s == nil {
		return nil, status.Error(codes.InvalidArgument, "store must not be nil")
	}
	return &Server{store: s}, nil
}

// SetComponentIntent applies or updates component intent.
func (s *Server) SetComponentIntent(ctx context.Context, req *componentspb.SetComponentIntentRequest) (*componentspb.SetComponentIntentResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	if req.GetComponentName() == "" {
		return nil, status.Error(codes.InvalidArgument, "component_name must not be empty")
	}
	if req.GetIntent() == nil {
		return nil, status.Error(codes.InvalidArgument, "intent must not be nil")
	}

	rec, err := s.store.SetComponentIntent(ctx, req.GetComponentName(), req.GetIntent(), req.GetDesiredK8SGeneration())
	if err != nil {
		return nil, err
	}

	return &componentspb.SetComponentIntentResponse{
		CommittedGeneration: rec.Generation,
		ReconciliationState: rec.State,
		CommitTimestamp:     timestamppb.New(rec.UpdateTime),
	}, nil
}

// PrepareDeactivation verifies foreign-key and dependency safety before deactivation.
func (s *Server) PrepareDeactivation(ctx context.Context, req *componentspb.PrepareDeactivationRequest) (*componentspb.PrepareDeactivationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	if req.GetComponentName() == "" {
		return nil, status.Error(codes.InvalidArgument, "component_name must not be empty")
	}

	res, err := s.store.PrepareDeactivation(ctx, req.GetComponentName(), req.GetRequestCascade())
	if err != nil {
		return nil, err
	}

	return &componentspb.PrepareDeactivationResponse{
		IsSafeToDeactivate:      res.IsSafeToDeactivate,
		BlockingDependents:      res.BlockingDependents,
		RecommendedDrainSeconds: res.RecommendedDrainSec,
		RejectionReason:         res.RejectionReason,
		CascadedDeactivations:   res.CascadedDeactivations,
	}, nil
}

// CompleteDeactivation confirms GKE workloads are de-provisioned and transitions state to DISABLED.
func (s *Server) CompleteDeactivation(ctx context.Context, req *componentspb.CompleteDeactivationRequest) (*componentspb.CompleteDeactivationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	if req.GetComponentName() == "" {
		return nil, status.Error(codes.InvalidArgument, "component_name must not be empty")
	}

	if err := s.store.CompleteDeactivation(ctx, req.GetComponentName()); err != nil {
		return nil, err
	}

	return &componentspb.CompleteDeactivationResponse{
		Success:               true,
		DeactivationTimestamp: timestamppb.Now(),
	}, nil
}

// GetPlatformHealth exposes platform-wide telemetry and CDC projection lag.
func (s *Server) GetPlatformHealth(ctx context.Context, req *componentspb.GetPlatformHealthRequest) (*componentspb.GetPlatformHealthResponse, error) {
	return s.store.GetPlatformHealth(ctx)
}

// ValidateComponentRequirement checks if a requested component class is active and satisfied.
func (s *Server) ValidateComponentRequirement(ctx context.Context, req *componentspb.ValidateComponentRequirementRequest) (*componentspb.ValidateComponentRequirementResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	if req.GetRequiredComponentClass() == "" {
		return nil, status.Error(codes.InvalidArgument, "required_component_class must not be empty")
	}

	valRes, err := s.store.ValidateComponentRequirement(ctx, req.GetRequiredComponentClass(), req.GetMinimumParameters())
	if err != nil {
		return nil, err
	}

	return &componentspb.ValidateComponentRequirementResponse{
		IsSatisfied:             valRes.IsSatisfied,
		SatisfyingComponentName: valRes.SatisfyingComponentName,
		ErrorDetail:             valRes.ErrorDetail,
	}, nil
}

// ResolveComponentSnapshot locks and records immutable snapshots at experiment launch (T0).
func (s *Server) ResolveComponentSnapshot(ctx context.Context, req *componentspb.ResolveComponentSnapshotRequest) (*componentspb.ResolveComponentSnapshotResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	if req.GetExperimentXid() == 0 {
		return nil, status.Error(codes.InvalidArgument, "experiment_xid must not be 0")
	}

	bindings, err := s.store.ResolveComponentSnapshot(ctx, req.GetExperimentXid(), req.GetRequestedComponentClasses())
	if err != nil {
		return nil, err
	}

	return &componentspb.ResolveComponentSnapshotResponse{
		SnapshotBindings: bindings,
		LockedTimestamp:  timestamppb.Now(),
	}, nil
}

// UpdateObservedGeneration updates observed generation and access status in Spanner.
func (s *Server) UpdateObservedGeneration(ctx context.Context, req *componentspb.UpdateObservedGenerationRequest) (*componentspb.UpdateObservedGenerationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	if req.GetComponentName() == "" {
		return nil, status.Error(codes.InvalidArgument, "component_name must not be empty")
	}

	err := s.store.UpdateObservedGeneration(ctx, req.GetComponentName(), req.GetObservedGeneration(), req.GetAccessStatus(), req.GetStatusCondition())
	if err != nil {
		return nil, err
	}

	return &componentspb.UpdateObservedGenerationResponse{
		Success: true,
	}, nil
}

// ExecuteDeletion soft-deletes a component row in Spanner.
func (s *Server) ExecuteDeletion(ctx context.Context, req *componentspb.ExecuteDeletionRequest) (*componentspb.ExecuteDeletionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	if req.GetComponentName() == "" {
		return nil, status.Error(codes.InvalidArgument, "component_name must not be empty")
	}

	if err := s.store.ExecuteDeletion(ctx, req.GetComponentName()); err != nil {
		return nil, err
	}

	return &componentspb.ExecuteDeletionResponse{
		Success: true,
	}, nil
}
