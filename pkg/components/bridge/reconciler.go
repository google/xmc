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

// Package bridge implements the GitOps Bridge Controller reconciliation loop and probers.
package bridge

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

const (
	// FinalizerDeletionProtection protects active component records from accidental pruning.
	FinalizerDeletionProtection = "finalizers.xmc.dev/deletion-protection"

	// Condition types
	ConditionReady           = "Ready"
	ConditionDegraded        = "Degraded"
	ConditionDeletionBlocked = "DeletionBlocked"
	ConditionDeactBlocked    = "DeactivationBlocked"
)

// CMSClient defines the interface for communicating with the CMS control plane.
type CMSClient interface {
	SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error)
	UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error)
	ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error)
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

func (a *serverClientAdapter) ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error) {
	return a.server.ExecuteDeletion(ctx, in)
}

// NewCMSClientAdapter adapts a ComponentManagerServiceServer to CMSClient for testing or in-process usage.
func NewCMSClientAdapter(server componentspb.ComponentManagerServiceServer) CMSClient {
	return &serverClientAdapter{server: server}
}

// ReconcileResult encapsulates the outcome of a reconciliation cycle.
type ReconcileResult struct {
	ObservedGeneration int64
	State              componentspb.ComponentState
	Access             *componentspb.ComponentAccessStatusProto
	Conditions         []*componentspb.ConditionProto
	AddFinalizer       bool
	RemoveFinalizer    bool
	RequeueAfter       time.Duration
}

// Reconciler executes the GitOps Bridge reconciliation logic.
type Reconciler struct {
	cmsClient       CMSClient
	prober          Prober
	workloadManager WorkloadManager
}

// NewReconciler creates a new Reconciler instance.
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

// ReconcileComponent executes a single reconciliation pass for a Component.
func (r *Reconciler) ReconcileComponent(
	ctx context.Context,
	namespace string,
	name string,
	generation int64,
	spec *componentspb.ComponentSpecProto,
	isDeleting bool,
	hasFinalizer bool,
	existingConditions ...[]*componentspb.ConditionProto,
) (*ReconcileResult, error) {
	if name == "" {
		return nil, fmt.Errorf("component name must not be empty")
	}
	if spec == nil {
		return nil, fmt.Errorf("spec must not be nil")
	}

	var existing []*componentspb.ConditionProto
	if len(existingConditions) > 0 {
		existing = existingConditions[0]
	}

	res := &ReconcileResult{
		ObservedGeneration: generation,
		State:              componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
	}

	// 1. Handle deletion flow.
	if isDeleting {
		if spec.GetDeletionProtection() {
			// Block deletion: Emit DeletionBlocked condition and retain finalizer.
			setCondition(&res.Conditions, existing, ConditionDeletionBlocked, "True", generation, "DeletionProtectionEnabled", fmt.Sprintf("Cannot delete %q: spec.deletionProtection is true", name))
			setCondition(&res.Conditions, existing, ConditionReady, "False", generation, "DeletingBlocked", "Resource deletion is blocked by deletion protection")
			return res, nil
		}

		// Deletion protection is false: Sync updated intent to CMS so store updates deletionProtection flag.
		_, _ = r.cmsClient.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
			ComponentName:        name,
			Intent:               spec,
			DesiredK8SGeneration: generation,
		})

		// Clean up 1P child workloads and execute deletion in CMS.
		if spec.GetComponentType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.workloadManager != nil {
			_ = r.workloadManager.Delete1PWorkload(ctx, namespace, name)
		}

		_, err := r.cmsClient.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{
			ComponentName: name,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to execute deletion in CMS: %w", err)
		}

		res.RemoveFinalizer = true
		res.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
		return res, nil
	}

	// 2. Ensure deletion protection finalizer is added.
	if !hasFinalizer && spec.GetDeletionProtection() {
		res.AddFinalizer = true
	}

	// 3. Apply desired intent to CMS SOT.
	setResp, err := r.cmsClient.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		ComponentName:        name,
		Intent:               spec,
		DesiredK8SGeneration: generation,
	})
	if err != nil {
		if spec.GetComponentType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.workloadManager != nil {
			_ = r.workloadManager.Delete1PWorkload(ctx, namespace, name)
		}
		setCondition(&res.Conditions, existing, ConditionDeactBlocked, "True", generation, "PreconditionFailed", err.Error())
		setCondition(&res.Conditions, existing, ConditionReady, "False", generation, "IntentApplicationFailed", err.Error())
		setCondition(&res.Conditions, existing, ConditionDegraded, "True", generation, "IntentApplicationFailed", err.Error())
		return res, nil
	}

	res.State = setResp.GetReconciliationState()

	// 4. If component is disabled, clean up 1P child workloads and report Ready=False.
	if !spec.GetEnabled() {
		if spec.GetComponentType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.workloadManager != nil {
			_ = r.workloadManager.Delete1PWorkload(ctx, namespace, name)
		}

		res.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
		setCondition(&res.Conditions, existing, ConditionReady, "False", generation, "ComponentDisabled", "Component is explicitly disabled in spec.enabled")
		setCondition(&res.Conditions, existing, ConditionDegraded, "False", generation, "ComponentDisabled", "Component is disabled")
		_ = r.updateObservedCMS(ctx, name, generation, nil, "Ready=False")
		return res, nil
	}

	// 5. If 1P native component, ensure child workloads (Deployment & Service).
	if spec.GetComponentType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.workloadManager != nil {
		if err := r.workloadManager.Ensure1PWorkload(ctx, namespace, name, spec); err != nil {
			setCondition(&res.Conditions, existing, ConditionReady, "False", generation, "WorkloadProvisionFailed", fmt.Sprintf("Failed to provision 1P workload: %v", err))
			setCondition(&res.Conditions, existing, ConditionDegraded, "True", generation, "WorkloadProvisionFailed", fmt.Sprintf("Workload provisioning error: %v", err))
			return res, nil
		}
	}

	// 6. Probe readiness and construct access payload.
	isHealthy, healthMsg := r.prober.ProbeHealth(ctx, spec)
	access := extractAccessStatus(name, spec)
	res.Access = access

	readyStatus := "False"
	readyReason := "HealthProbeFailed"
	degradedStatus := "True"
	degradedReason := "HealthProbeFailed"
	if isHealthy {
		readyStatus = "True"
		readyReason = "ComponentHealthy"
		degradedStatus = "False"
		degradedReason = "ComponentHealthy"
		res.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	}

	setCondition(&res.Conditions, existing, ConditionReady, readyStatus, generation, readyReason, healthMsg)
	setCondition(&res.Conditions, existing, ConditionDegraded, degradedStatus, generation, degradedReason, healthMsg)

	// 7. Update CMS observed generation & status.
	err = r.updateObservedCMS(ctx, name, generation, access, fmt.Sprintf("Ready=%s", readyStatus))
	if err != nil {
		return nil, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}

	return res, nil
}

func setCondition(conditions *[]*componentspb.ConditionProto, existing []*componentspb.ConditionProto, condType string, status string, gen int64, reason, message string) {
	var prevTime *timestamppb.Timestamp
	for _, c := range existing {
		if c.GetType() == condType {
			if c.GetStatus() == status && c.GetLastTransitionTime() != nil {
				prevTime = c.GetLastTransitionTime()
			}
			break
		}
	}
	if prevTime == nil {
		prevTime = timestamppb.Now()
	}

	newCond := &componentspb.ConditionProto{
		Type:               condType,
		Status:             status,
		ObservedGeneration: gen,
		LastTransitionTime: prevTime,
		Reason:             reason,
		Message:            message,
	}

	for i, c := range *conditions {
		if c.GetType() == condType {
			(*conditions)[i] = newCond
			return
		}
	}
	*conditions = append(*conditions, newCond)
}

func (r *Reconciler) updateObservedCMS(ctx context.Context, name string, gen int64, access *componentspb.ComponentAccessStatusProto, cond string) error {
	_, err := r.cmsClient.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		ComponentName:      name,
		ObservedGeneration: gen,
		AccessStatus:       access,
		StatusCondition:    cond,
	})
	return err
}

func extractAccessStatus(name string, spec *componentspb.ComponentSpecProto) *componentspb.ComponentAccessStatusProto {
	envVars := make(map[string]string)
	params := spec.GetParameters()

	// Generic environment variable propagation based on standard component parameters.
	switch spec.GetComponentClass() {
	case "analytical-db":
		if ep, ok := params["clusterEndpoints"]; ok {
			envVars["CLICKHOUSE_HOST"] = ep
		}
		if port, ok := params["nativePort"]; ok {
			envVars["CLICKHOUSE_PORT"] = port
		}
		if db, ok := params["defaultDatabase"]; ok {
			envVars["CLICKHOUSE_DATABASE"] = db
		}
	case "event-stream":
		if brokers, ok := params["brokerEndpoints"]; ok {
			envVars["KAFKA_BROKERS"] = brokers
		}
		if topic, ok := params["defaultTopic"]; ok {
			envVars["METRICS_TOPIC"] = topic
		}
	case "metrics-ingestion":
		if port, ok := params["port"]; ok {
			envVars["INGESTION_GATEWAY_URL"] = fmt.Sprintf("http://%s.xmc.svc.cluster.local:%s", name, port)
		}
	default:
		for k, v := range params {
			envVars[k] = v
		}
	}

	return &componentspb.ComponentAccessStatusProto{
		EnvVars: envVars,
	}
}
