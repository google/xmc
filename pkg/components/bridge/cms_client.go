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

package bridge

import (
	"context"

	"google.golang.org/grpc"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// CMSClient defines the interface for communicating with the CMS control plane.
type CMSClient interface {
	// SetComponentIntent registers or updates declarative component intent in CMS.
	SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error)
	// UpdateObservedGeneration updates the observed generation, state, and status message of a component in CMS.
	UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error)
	// ExecuteDeletion executes component deletion in the CMS backend.
	ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error)
	// PrepareDeactivation validates deactivation safety (FailedPrecondition on active dependents) and marks the component DISABLING.
	PrepareDeactivation(ctx context.Context, in *componentspb.PrepareDeactivationRequest, opts ...grpc.CallOption) (*componentspb.PrepareDeactivationResponse, error)
	// CompleteDeactivation transitions a DISABLING component to DISABLED.
	CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error)
}

type serverClientAdapter struct {
	server componentspb.ComponentManagerServiceServer
}

// NewCMSClientAdapter creates a CMSClient adapter wrapping an in-process gRPC server.
func NewCMSClientAdapter(s componentspb.ComponentManagerServiceServer) CMSClient {
	return &serverClientAdapter{server: s}
}

func (a *serverClientAdapter) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
	return a.server.SetComponentIntent(ctx, in)
}
func (a *serverClientAdapter) UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error) {
	return a.server.UpdateObservedGeneration(ctx, in)
}
func (a *serverClientAdapter) ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error) {
	return a.server.ExecuteDeletion(ctx, in)
}
func (a *serverClientAdapter) PrepareDeactivation(ctx context.Context, in *componentspb.PrepareDeactivationRequest, opts ...grpc.CallOption) (*componentspb.PrepareDeactivationResponse, error) {
	return a.server.PrepareDeactivation(ctx, in)
}
func (a *serverClientAdapter) CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error) {
	return a.server.CompleteDeactivation(ctx, in)
}

// updateObservedCMS sends an UpdateObservedGeneration RPC to CMS.
func (r *Reconciler) updateObservedCMS(ctx context.Context, name string, gen int64, state componentspb.ComponentState, statusMsg string) error {
	_, err := r.cmsClient.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name: name, ObservedGeneration: gen, State: state, StatusMessage: statusMsg,
	})
	return err
}
