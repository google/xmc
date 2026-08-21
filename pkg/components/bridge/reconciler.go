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

// Package bridge implements the GitOps Bridge Controller reconciliation loop,
// health probers, and workload managers for the XMC Components System.
package bridge

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

const (
	// FinalizerCleanup is the primary finalizer ensuring child workloads and state are gracefully cleaned up.
	FinalizerCleanup = "xmc.dev/component-cleanup"

	// FinalizerDeletionProtection protects active component records from accidental pruning.
	FinalizerDeletionProtection = "finalizers.xmc.dev/deletion-protection"

	// ConditionReady indicates if the component is healthy and operational.
	ConditionReady = "Ready"

	// ConditionDegraded indicates if the component is experiencing errors or degraded health.
	ConditionDegraded = "Degraded"

	// ConditionDeletionBlocked indicates deletion is blocked due to deletionProtection.
	ConditionDeletionBlocked = "DeletionBlocked"

	// ConditionDeactivationBlocked indicates deactivation is blocked due to active dependents.
	ConditionDeactivationBlocked = "DeactivationBlocked"
)

// Condition represents an observed status condition on a Component resource.
type Condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	ObservedGeneration int64     `json:"observedGeneration,omitempty"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
	Reason             string    `json:"reason"`
	Message            string    `json:"message"`
}

// CMSClient defines the interface for communicating with the CMS control plane.
type CMSClient interface {
	SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error)
	UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error)
	PrepareDeactivation(ctx context.Context, in *componentspb.PrepareDeactivationRequest, opts ...grpc.CallOption) (*componentspb.PrepareDeactivationResponse, error)
	CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error)
	ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error)
	ResolveComponentSnapshot(ctx context.Context, in *componentspb.ResolveComponentSnapshotRequest, opts ...grpc.CallOption) (*componentspb.ResolveComponentSnapshotResponse, error)
	ValidateComponentRequirement(ctx context.Context, in *componentspb.ValidateComponentRequirementRequest, opts ...grpc.CallOption) (*componentspb.ValidateComponentRequirementResponse, error)
	GetPlatformHealth(ctx context.Context, in *componentspb.GetPlatformHealthRequest, opts ...grpc.CallOption) (*componentspb.GetPlatformHealthResponse, error)
}

type serverClientAdapter struct {
	server componentspb.ComponentManagerServiceServer
}

func (a *serverClientAdapter) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
	return a.server.SetComponentIntent(ctx, in)
}

func (a *serverClientAdapter) UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error) {
	return a.server.UpdateObservedGeneration(ctx, in)
}

func (a *serverClientAdapter) PrepareDeactivation(ctx context.Context, in *componentspb.PrepareDeactivationRequest, opts ...grpc.CallOption) (*componentspb.PrepareDeactivationResponse, error) {
	return a.server.PrepareDeactivation(ctx, in)
}

func (a *serverClientAdapter) CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error) {
	return a.server.CompleteDeactivation(ctx, in)
}

func (a *serverClientAdapter) ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error) {
	return a.server.ExecuteDeletion(ctx, in)
}

func (a *serverClientAdapter) ResolveComponentSnapshot(ctx context.Context, in *componentspb.ResolveComponentSnapshotRequest, opts ...grpc.CallOption) (*componentspb.ResolveComponentSnapshotResponse, error) {
	return a.server.ResolveComponentSnapshot(ctx, in)
}

func (a *serverClientAdapter) ValidateComponentRequirement(ctx context.Context, in *componentspb.ValidateComponentRequirementRequest, opts ...grpc.CallOption) (*componentspb.ValidateComponentRequirementResponse, error) {
	return a.server.ValidateComponentRequirement(ctx, in)
}

func (a *serverClientAdapter) GetPlatformHealth(ctx context.Context, in *componentspb.GetPlatformHealthRequest, opts ...grpc.CallOption) (*componentspb.GetPlatformHealthResponse, error) {
	return a.server.GetPlatformHealth(ctx, in)
}

// NewCMSClientAdapter adapts a ComponentManagerServiceServer to CMSClient for testing or in-process usage.
func NewCMSClientAdapter(server componentspb.ComponentManagerServiceServer) CMSClient {
	return &serverClientAdapter{server: server}
}

// FakeCMSClient provides an in-memory test double for CMSClient with customizable hooks.
type FakeCMSClient struct {
	mu                       sync.RWMutex
	SetIntentFunc            func(ctx context.Context, in *componentspb.SetComponentIntentRequest) (*componentspb.SetComponentIntentResponse, error)
	UpdateObservedGenFunc    func(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest) (*componentspb.UpdateObservedGenerationResponse, error)
	PrepareDeactivationFunc  func(ctx context.Context, in *componentspb.PrepareDeactivationRequest) (*componentspb.PrepareDeactivationResponse, error)
	CompleteDeactivationFunc func(ctx context.Context, in *componentspb.CompleteDeactivationRequest) (*componentspb.CompleteDeactivationResponse, error)
	ExecuteDeletionFunc      func(ctx context.Context, in *componentspb.ExecuteDeletionRequest) (*componentspb.ExecuteDeletionResponse, error)

	SetIntentCalls            []*componentspb.SetComponentIntentRequest
	UpdateObservedGenCalls    []*componentspb.UpdateObservedGenerationRequest
	PrepareDeactivationCalls  []*componentspb.PrepareDeactivationRequest
	CompleteDeactivationCalls []*componentspb.CompleteDeactivationRequest
	ExecuteDeletionCalls      []*componentspb.ExecuteDeletionRequest
}

// NewFakeCMSClient creates an initialized FakeCMSClient instance.
func NewFakeCMSClient() *FakeCMSClient {
	return &FakeCMSClient{}
}

// SetComponentIntent intercepts and records SetComponentIntent requests.
func (f *FakeCMSClient) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SetIntentCalls = append(f.SetIntentCalls, in)
	if f.SetIntentFunc != nil {
		return f.SetIntentFunc(ctx, in)
	}
	return &componentspb.SetComponentIntentResponse{
		Component: &componentspb.Component{
			Name: in.GetName(),
			Spec: in.GetSpec(),
			Status: &componentspb.ComponentAccessStatus{
				State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			},
		},
	}, nil
}

// UpdateObservedGeneration intercepts and records UpdateObservedGeneration requests.
func (f *FakeCMSClient) UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.UpdateObservedGenCalls = append(f.UpdateObservedGenCalls, in)
	if f.UpdateObservedGenFunc != nil {
		return f.UpdateObservedGenFunc(ctx, in)
	}
	return &componentspb.UpdateObservedGenerationResponse{
		Component: &componentspb.Component{
			Name: in.GetName(),
			Status: &componentspb.ComponentAccessStatus{
				ObservedGeneration: in.GetObservedGeneration(),
				State:              in.GetState(),
			},
		},
	}, nil
}

// PrepareDeactivation intercepts and records PrepareDeactivation requests.
func (f *FakeCMSClient) PrepareDeactivation(ctx context.Context, in *componentspb.PrepareDeactivationRequest, opts ...grpc.CallOption) (*componentspb.PrepareDeactivationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PrepareDeactivationCalls = append(f.PrepareDeactivationCalls, in)
	if f.PrepareDeactivationFunc != nil {
		return f.PrepareDeactivationFunc(ctx, in)
	}
	return &componentspb.PrepareDeactivationResponse{
		State: componentspb.ComponentState_COMPONENT_STATE_DISABLING,
	}, nil
}

// CompleteDeactivation intercepts and records CompleteDeactivation requests.
func (f *FakeCMSClient) CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CompleteDeactivationCalls = append(f.CompleteDeactivationCalls, in)
	if f.CompleteDeactivationFunc != nil {
		return f.CompleteDeactivationFunc(ctx, in)
	}
	return &componentspb.CompleteDeactivationResponse{
		State: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
	}, nil
}

// ExecuteDeletion intercepts and records ExecuteDeletion requests.
func (f *FakeCMSClient) ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ExecuteDeletionCalls = append(f.ExecuteDeletionCalls, in)
	if f.ExecuteDeletionFunc != nil {
		return f.ExecuteDeletionFunc(ctx, in)
	}
	return &componentspb.ExecuteDeletionResponse{
		State: componentspb.ComponentState_COMPONENT_STATE_DELETED,
	}, nil
}

// ResolveComponentSnapshot returns an empty response for fake client.
func (f *FakeCMSClient) ResolveComponentSnapshot(ctx context.Context, in *componentspb.ResolveComponentSnapshotRequest, opts ...grpc.CallOption) (*componentspb.ResolveComponentSnapshotResponse, error) {
	return &componentspb.ResolveComponentSnapshotResponse{}, nil
}

// ValidateComponentRequirement returns satisfied for fake client.
func (f *FakeCMSClient) ValidateComponentRequirement(ctx context.Context, in *componentspb.ValidateComponentRequirementRequest, opts ...grpc.CallOption) (*componentspb.ValidateComponentRequirementResponse, error) {
	return &componentspb.ValidateComponentRequirementResponse{Satisfied: true}, nil
}

// GetPlatformHealth returns empty response for fake client.
func (f *FakeCMSClient) GetPlatformHealth(ctx context.Context, in *componentspb.GetPlatformHealthRequest, opts ...grpc.CallOption) (*componentspb.GetPlatformHealthResponse, error) {
	return &componentspb.GetPlatformHealthResponse{}, nil
}

// ReconcileRequest encapsulates the parameters for a component reconciliation pass.
type ReconcileRequest struct {
	Namespace          string
	Name               string
	Generation         int64
	Spec               *componentspb.ComponentSpec
	Enabled            bool
	DeletionProtection bool
	IsDeleting         bool
	HasFinalizer       bool
	ExistingConditions []Condition
}

// ReconcileResult encapsulates the outcome of a reconciliation pass.
type ReconcileResult struct {
	ObservedGeneration int64
	State              componentspb.ComponentState
	Conditions         []Condition
	AddFinalizer       bool
	RemoveFinalizer    bool
	RequeueAfter       time.Duration
}

// Reconciler executes the GitOps Bridge reconciliation state machine.
type Reconciler struct {
	cmsClient       CMSClient
	prober          Prober
	workloadManager WorkloadManager
}

// NewReconciler creates an initialized Reconciler instance.
func NewReconciler(cmsClient CMSClient, prober Prober, workloadManager WorkloadManager) (*Reconciler, error) {
	if cmsClient == nil {
		return nil, fmt.Errorf("cmsClient must not be nil")
	}
	if prober == nil {
		prober = NewNetworkProber(3 * time.Second)
	}
	return &Reconciler{
		cmsClient:       cmsClient,
		prober:          prober,
		workloadManager: workloadManager,
	}, nil
}

// ReconcileComponent executes a single reconciliation cycle for a Component.
func (r *Reconciler) ReconcileComponent(ctx context.Context, req ReconcileRequest) (*ReconcileResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("component name must not be empty")
	}
	canonicalName := store.CanonicalComponentName(req.Name)
	if canonicalName == "" || !store.IsValidComponentName(canonicalName) {
		return nil, fmt.Errorf("invalid component name %q", req.Name)
	}
	if req.Spec == nil {
		return nil, fmt.Errorf("spec must not be nil")
	}

	if req.IsDeleting {
		return r.handleDeletion(ctx, canonicalName, req)
	}

	res := &ReconcileResult{
		ObservedGeneration: req.Generation,
		Conditions:         copyConditions(req.ExistingConditions),
	}

	if !req.HasFinalizer {
		res.AddFinalizer = true
	}

	setResp, err := r.cmsClient.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: store.FullComponentName(canonicalName),
		Spec: req.Spec,
	})
	if err != nil {
		setCondition(&res.Conditions, req.ExistingConditions, ConditionReady, "False", req.Generation, "IntentApplicationFailed", err.Error())
		setCondition(&res.Conditions, req.ExistingConditions, ConditionDegraded, "True", req.Generation, "IntentApplicationFailed", err.Error())
		return res, nil
	}

	if setResp != nil && setResp.GetComponent() != nil && setResp.GetComponent().GetStatus() != nil {
		res.State = setResp.GetComponent().GetStatus().GetState()
	}

	if !req.Enabled {
		return r.handleDisabled(ctx, canonicalName, req, res)
	}

	return r.handleEnabled(ctx, canonicalName, req, res)
}

func (r *Reconciler) handleDeletion(ctx context.Context, name string, req ReconcileRequest) (*ReconcileResult, error) {
	res := &ReconcileResult{
		ObservedGeneration: req.Generation,
		Conditions:         copyConditions(req.ExistingConditions),
	}

	if req.DeletionProtection {
		setCondition(&res.Conditions, req.ExistingConditions, ConditionDeletionBlocked, "True", req.Generation, "DeletionProtectionEnabled", fmt.Sprintf("Cannot delete %q: deletion protection is enabled", name))
		setCondition(&res.Conditions, req.ExistingConditions, ConditionReady, "False", req.Generation, "DeletingBlocked", "Resource deletion is blocked by deletion protection")
		return res, nil
	}

	if req.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.workloadManager != nil {
		if req.Spec.GetDeletionPolicy() != componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN {
			_ = r.workloadManager.Delete1PWorkload(ctx, req.Namespace, name)
		}
	}

	_, err := r.cmsClient.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{
		Name:           store.FullComponentName(name),
		DeletionPolicy: req.Spec.GetDeletionPolicy(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute deletion in CMS: %w", err)
	}

	res.RemoveFinalizer = true
	res.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	return res, nil
}

func (r *Reconciler) handleDisabled(ctx context.Context, name string, req ReconcileRequest, res *ReconcileResult) (*ReconcileResult, error) {
	if req.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.workloadManager != nil {
		_ = r.workloadManager.Delete1PWorkload(ctx, req.Namespace, name)
	}

	if _, err := r.cmsClient.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{
		Name: store.FullComponentName(name),
	}); err != nil {
		return nil, fmt.Errorf("failed to complete deactivation in CMS: %w", err)
	}

	res.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
	setCondition(&res.Conditions, req.ExistingConditions, ConditionReady, "False", req.Generation, "ComponentDisabled", "Component is disabled")
	setCondition(&res.Conditions, req.ExistingConditions, ConditionDegraded, "False", req.Generation, "ComponentDisabled", "Component is disabled")

	if err := r.updateCMSObserved(ctx, name, req.Generation, componentspb.ComponentState_COMPONENT_STATE_DISABLED, "Ready=False"); err != nil {
		return nil, fmt.Errorf("failed to sync observed generation to CMS: %w", err)
	}

	return res, nil
}

func (r *Reconciler) handleEnabled(ctx context.Context, name string, req ReconcileRequest, res *ReconcileResult) (*ReconcileResult, error) {
	if req.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.workloadManager != nil {
		if err := r.workloadManager.Ensure1PWorkload(ctx, req.Namespace, name, req.Spec); err != nil {
			setCondition(&res.Conditions, req.ExistingConditions, ConditionReady, "False", req.Generation, "WorkloadProvisionFailed", fmt.Sprintf("Failed to provision 1P workload: %v", err))
			setCondition(&res.Conditions, req.ExistingConditions, ConditionDegraded, "True", req.Generation, "WorkloadProvisionFailed", err.Error())
			res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
			return res, nil
		}
	}

	isHealthy, healthMsg := r.prober.ProbeComponentStatus(ctx, name, req.Spec)
	if isHealthy {
		res.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
		setCondition(&res.Conditions, req.ExistingConditions, ConditionReady, "True", req.Generation, "ComponentHealthy", healthMsg)
		setCondition(&res.Conditions, req.ExistingConditions, ConditionDegraded, "False", req.Generation, "ComponentHealthy", healthMsg)
	} else {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		setCondition(&res.Conditions, req.ExistingConditions, ConditionReady, "False", req.Generation, "HealthProbeFailed", healthMsg)
		setCondition(&res.Conditions, req.ExistingConditions, ConditionDegraded, "True", req.Generation, "HealthProbeFailed", healthMsg)
	}

	statusMsg := fmt.Sprintf("Ready=%v", isHealthy)
	if err := r.updateCMSObserved(ctx, name, req.Generation, res.State, statusMsg); err != nil {
		return nil, fmt.Errorf("failed to sync observed generation to CMS: %w", err)
	}

	return res, nil
}

func (r *Reconciler) updateCMSObserved(ctx context.Context, name string, gen int64, state componentspb.ComponentState, statusMsg string) error {
	_, err := r.cmsClient.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name:               store.FullComponentName(name),
		ObservedGeneration: gen,
		State:              state,
		StatusMessage:      statusMsg,
	})
	return err
}

func setCondition(conditions *[]Condition, existing []Condition, condType, status string, gen int64, reason, message string) {
	transitionTime := time.Now()
	for _, c := range existing {
		if c.Type == condType && c.Status == status && !c.LastTransitionTime.IsZero() {
			transitionTime = c.LastTransitionTime
			break
		}
	}

	newCond := Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: gen,
		LastTransitionTime: transitionTime,
		Reason:             reason,
		Message:            message,
	}

	for i, c := range *conditions {
		if c.Type == condType {
			(*conditions)[i] = newCond
			return
		}
	}
	*conditions = append(*conditions, newCond)
}

func copyConditions(in []Condition) []Condition {
	if in == nil {
		return nil
	}
	out := make([]Condition, len(in))
	copy(out, in)
	return out
}
