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
	"fmt"

	"google.golang.org/grpc"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
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

// updateObservedCMS sends an UpdateObservedGeneration RPC to CMS reporting res.State at the CMS
// component generation res.ComponentGeneration, with the custom resource as its source. It is a
// no-op when the generation is 0 (unknown): before the first intent of a resource was committed, or
// after the resource stopped owning its CMS record. A rejection because another custom resource
// owns the record is handled by releaseCMSOwnership: it writes nothing to CMS and returns
// released=true with a nil error, so callers can skip logging their own outcome.
func (r *Reconciler) updateObservedCMS(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, statusMsg string) (released bool, err error) {
	if res.ComponentGeneration <= 0 {
		logging.Debug(ctx, "Skipping CMS observed generation update; component generation unknown", "name", canonicalName)
		return false, nil
	}
	_, err = r.cmsClient.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name: canonicalName, ObservedGeneration: res.ComponentGeneration, State: res.State, StatusMessage: statusMsg, Source: intentSource(in),
	})
	if stale := storeRejectionMessage(err, staleObservationMessagePrefix); stale != "" {
		releaseCMSOwnership(ctx, in, canonicalName, res, stale)
		return true, nil
	}
	return false, err
}

// releaseCMSOwnership records that the custom resource no longer owns its CMS record (CMS rejected
// its observed-state report with stale, naming the owner): it reports Ready=False, Degraded=True
// with reason StaleIntent, clears res.ComponentGeneration and res.Access, and keeps
// res.ComponentID. With status.componentGeneration gone, later passes skip updateObservedCMS until
// an intent from this resource commits again, and the Actuator's generation drift check falls back
// to status.observedGeneration. For components whose intent CMS rejects as stale, that is harmless:
// a Ready=False or Degraded resource already blocks placement.
func releaseCMSOwnership(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, stale string) {
	logging.Warn(ctx, "Component is owned by another custom resource; no longer reporting its CMS generation", "name", canonicalName, "generation", in.Generation, "previousComponentGeneration", res.ComponentGeneration, "detail", stale)
	res.ComponentGeneration = 0
	res.Access = nil
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	setReadiness(res, in, ConditionStatusFalse, ConditionStatusTrue, ReasonStaleIntent, stale)
}

// syncReadyToCMS reports res.State to CMS when shouldSyncToCMS allows it for readyStatus or the
// component generation changed, then restarts the CMS heartbeat by stamping the Ready condition's
// transition time.
func (r *Reconciler) syncReadyToCMS(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, readyStatus string) error {
	if !shouldSyncToCMS(in.ExistingConditions, readyStatus, in.Generation) && !componentGenerationChanged(in, res) {
		return nil
	}
	if _, err := r.updateObservedCMS(ctx, in, canonicalName, res, "Ready="+readyStatus); err != nil {
		return fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
	resetReadyTransitionTime(res.Conditions)
	return nil
}
