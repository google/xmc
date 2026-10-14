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
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

const defaultCMSHeartbeatInterval = 5 * time.Minute

type cmsSyncEntry struct {
	syncedAt               time.Time
	state                  componentspb.ComponentState
	readyStatus            string
	generation             int64
	deactivationCheckedAt  time.Time
	deactivationCheckedGen int64
	deactivationBlockedMsg string
}

// cmsSyncTracker tracks in-memory per-component CMS synchronization and deactivation-block
// recheck timestamps so Kubernetes condition lastTransitionTime values only change when a
// condition's status transitions.
type cmsSyncTracker struct {
	mu      sync.Mutex
	entries map[string]cmsSyncEntry
	now     func() time.Time
}

func (t *cmsSyncTracker) currentTime() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *cmsSyncTracker) shouldSync(key string, state componentspb.ComponentState, readyStatus string, gen int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.entries[key]
	if !ok || entry.syncedAt.IsZero() {
		return true
	}
	if entry.generation != gen || entry.state != state || entry.readyStatus != readyStatus {
		return true
	}
	return t.currentTime().Sub(entry.syncedAt) >= defaultCMSHeartbeatInterval
}

func (t *cmsSyncTracker) recordSync(key string, state componentspb.ComponentState, readyStatus string, gen int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = make(map[string]cmsSyncEntry)
	}
	entry := t.entries[key]
	entry.syncedAt = t.currentTime()
	entry.state = state
	entry.readyStatus = readyStatus
	entry.generation = gen
	t.entries[key] = entry
}

func (t *cmsSyncTracker) recentDeactivationBlock(key string, in ReconcileComponentInput) (string, bool) {
	c := findCondition(in.ExistingConditions, ConditionTypeDeactivationBlocked)
	if c == nil || c.Status != ConditionStatusTrue || c.ObservedGeneration != in.Generation {
		return "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.entries[key]
	if !ok || entry.deactivationCheckedGen != in.Generation || entry.deactivationCheckedAt.IsZero() {
		return "", false
	}
	if t.currentTime().Sub(entry.deactivationCheckedAt) >= deactivationRecheckInterval {
		return "", false
	}
	if entry.deactivationBlockedMsg != "" {
		return entry.deactivationBlockedMsg, true
	}
	return c.Message, true
}

func (t *cmsSyncTracker) recordDeactivationBlock(key string, gen int64, msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = make(map[string]cmsSyncEntry)
	}
	entry := t.entries[key]
	entry.deactivationCheckedAt = t.currentTime()
	entry.deactivationCheckedGen = gen
	entry.deactivationBlockedMsg = msg
	t.entries[key] = entry
}

func (t *cmsSyncTracker) clearDeactivationBlock(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.entries[key]
	if !ok {
		return
	}
	entry.deactivationCheckedAt = time.Time{}
	entry.deactivationCheckedGen = 0
	entry.deactivationBlockedMsg = ""
	t.entries[key] = entry
}

func (t *cmsSyncTracker) forget(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}

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
	// DeclareClusterEpoch declares this cluster's ownership epoch to CMS.
	DeclareClusterEpoch(ctx context.Context, in *componentspb.DeclareClusterEpochRequest, opts ...grpc.CallOption) (*componentspb.DeclareClusterEpochResponse, error)
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
func (a *serverClientAdapter) DeclareClusterEpoch(ctx context.Context, in *componentspb.DeclareClusterEpochRequest, opts ...grpc.CallOption) (*componentspb.DeclareClusterEpochResponse, error) {
	return a.server.DeclareClusterEpoch(ctx, in)
}

// updateObservedCMS sends an UpdateObservedGeneration RPC to CMS.
func (r *Reconciler) updateObservedCMS(ctx context.Context, name string, gen int64, state componentspb.ComponentState, statusMsg string) error {
	_, err := r.cmsClient.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name: name, ObservedGeneration: gen, State: state, StatusMessage: statusMsg,
	})
	return err
}

// syncReadyToCMS reports res.State to CMS when syncTracker requires it for readyStatus.
func (r *Reconciler) syncReadyToCMS(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, readyStatus string) error {
	if in.skipCMSSync {
		return nil
	}
	return r.syncObservedToCMS(ctx, in, canonicalName, res, readyStatus, false)
}

func (r *Reconciler) syncObservedToCMS(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, readyStatus string, ignoreNotFound bool) error {
	key := componentKey(in.Namespace, canonicalName)
	// A CR without a Ready condition is new (possibly recreated under the same name), so it always syncs.
	if isCMSSynced(in.ExistingConditions) && findCondition(in.ExistingConditions, ConditionTypeReady) != nil && !r.syncTracker.shouldSync(key, res.State, readyStatus, in.Generation) {
		setSynced(res, in, ConditionStatusTrue, ReasonCMSSynced, cmsSyncedMessage)
		return nil
	}
	if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, "Ready="+readyStatus); err != nil {
		if !ignoreNotFound || status.Code(err) != codes.NotFound {
			setSynced(res, in, ConditionStatusFalse, ReasonCMSSyncFailed, err.Error())
			return fmt.Errorf("failed to update observed generation in CMS: %w", err)
		}
	}
	r.syncTracker.recordSync(key, res.State, readyStatus, in.Generation)
	setSynced(res, in, ConditionStatusTrue, ReasonCMSSynced, cmsSyncedMessage)
	return nil
}
