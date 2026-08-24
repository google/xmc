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

// Package bridge implements the GitOps Bridge Controller reconciliation loop.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/components/store"
)

const (
	FinalizerDeletionProtection      = "finalizers.xmc.dev/deletion-protection"
	ConditionTypeReady               = "Ready"
	ConditionTypeDegraded            = "Degraded"
	ConditionTypeDeletionBlocked     = "DeletionBlocked"
	ConditionTypeDeactivationBlocked = "DeactivationBlocked"
	ConditionStatusTrue              = "True"
	ConditionStatusFalse             = "False"
)

// CMSClient defines the interface for communicating with the CMS control plane.
type CMSClient interface {
	SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error)
	UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error)
	ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error)
}

// StatusDetectorHealthProber defines the health checking interface.
type StatusDetectorHealthProber interface {
	ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec) (bool, string)
}

// WorkloadManager defines the lifecycle manager interface for 1P child workloads.
type WorkloadManager interface {
	Ensure1PWorkload(ctx context.Context, namespace string, name string, spec *componentspb.ComponentSpec) error
	Delete1PWorkload(ctx context.Context, namespace string, name string) error
}

type serverClientAdapter struct {
	server componentspb.ComponentManagerServiceServer
}

// NewCMSClientAdapter creates a CMSClient adapter wrapping a gRPC server.
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

// Condition represents a standardized Kubernetes custom resource condition.
type Condition struct {
	Type, Status, Reason, Message string
	ObservedGeneration            int64
	LastTransitionTime            time.Time
}

// AccessStatus encapsulates network and access metadata.
type AccessStatus struct {
	EnvVars map[string]string
}

// ReconcileResult encapsulates the outcome of a reconciliation pass.
type ReconcileResult struct {
	ObservedGeneration int64
	State              componentspb.ComponentState
	Conditions         []Condition
	Access             *AccessStatus
	AddFinalizer       bool
	RemoveFinalizer    bool
}

// Reconciler executes reconciliation cycles for XMC Component custom resources.
type Reconciler struct {
	cmsClient       CMSClient
	prober          StatusDetectorHealthProber
	workloadManager WorkloadManager
}

// NewReconciler constructs a new Reconciler instance.
func NewReconciler(cms CMSClient, prober StatusDetectorHealthProber, wm WorkloadManager) (*Reconciler, error) {
	if cms == nil {
		return nil, errors.New("cms client must not be nil")
	}
	return &Reconciler{cmsClient: cms, prober: prober, workloadManager: wm}, nil
}

// ReconcileComponentInput bundles parameters for a component reconciliation pass.
type ReconcileComponentInput struct {
	Namespace, Name    string
	Generation         int64
	Spec               *componentspb.ComponentSpec
	IsDeleting         bool
	HasFinalizer       bool
	DeletionProtection bool
	Enabled            bool
	ExistingConditions []Condition
}

// ReconcileComponent executes a single reconciliation pass for a Component.
func (r *Reconciler) ReconcileComponent(ctx context.Context, in ReconcileComponentInput) (*ReconcileResult, error) {
	if in.Name == "" || in.Spec == nil {
		return nil, errors.New("component name and spec must not be empty/nil")
	}
	logging.Debug(ctx, "Reconciling Component custom resource", "name", in.Name, "generation", in.Generation, "enabled", in.Enabled, "isDeleting", in.IsDeleting)
	in.Spec.Generation = in.Generation
	canonicalName := store.CanonicalComponentName(in.Name)
	res := &ReconcileResult{ObservedGeneration: in.Generation, State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION}

	if in.IsDeleting {
		return r.reconcileDeletion(ctx, in, canonicalName, res)
	}

	if !in.HasFinalizer && in.DeletionProtection {
		res.AddFinalizer = true
		logging.Info(ctx, "Attaching deletion protection finalizer", "name", in.Name)
	}
	if err := r.applyIntent(ctx, in, canonicalName, res); err != nil {
		return res, err
	}
	if !in.Enabled {
		return r.reconcileDisabled(ctx, in, canonicalName, res)
	}
	if err := r.ensureWorkload(ctx, in, res); err != nil {
		return res, err
	}
	return r.evaluateHealth(ctx, in, canonicalName, res)
}

func (r *Reconciler) reconcileDeletion(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if in.DeletionProtection {
		logging.Warn(ctx, "Component deletion blocked by deletion protection", "name", in.Name, "generation", in.Generation)
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDeletionBlocked, ConditionStatusTrue, in.Generation, "DeletionProtectionEnabled", fmt.Sprintf("Cannot delete %q: deletionProtection is enabled", in.Name))
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "DeletingBlocked", "Deletion blocked by deletion protection")
		return res, nil
	}
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.workloadManager != nil {
		if err := r.workloadManager.Delete1PWorkload(ctx, in.Namespace, in.Name); err != nil {
			return nil, fmt.Errorf("failed to delete 1P workload: %w", err)
		}
	}
	if _, err := r.cmsClient.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{Name: canonicalName, DeletionPolicy: in.Spec.GetDeletionPolicy()}); err != nil {
		return nil, fmt.Errorf("failed to execute deletion in CMS: %w", err)
	}
	logging.Info(ctx, "Executed component deletion in CMS", "name", canonicalName, "policy", in.Spec.GetDeletionPolicy().String())
	res.RemoveFinalizer = true
	res.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	return res, nil
}

func (r *Reconciler) applyIntent(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) error {
	setResp, err := r.cmsClient.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: canonicalName, Spec: in.Spec})
	if err != nil {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.workloadManager != nil {
			_ = r.workloadManager.Delete1PWorkload(ctx, in.Namespace, in.Name)
		}
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "IntentApplicationFailed", err.Error())
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "IntentApplicationFailed", err.Error())
		return err
	}
	if setResp != nil && setResp.GetComponent() != nil && setResp.GetComponent().GetStatus() != nil {
		res.State = setResp.GetComponent().GetStatus().GetState()
	}
	logging.Info(ctx, "Applied component intent to CMS", "name", canonicalName, "generation", in.Generation, "state", res.State.String())
	return nil
}

func (r *Reconciler) reconcileDisabled(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.workloadManager != nil {
		if err := r.workloadManager.Delete1PWorkload(ctx, in.Namespace, in.Name); err != nil {
			res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "WorkloadDeletionFailed", err.Error())
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "WorkloadDeletionFailed", err.Error())
			return res, err
		}
	}
	res.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "ComponentDisabled", "Component is disabled")
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusFalse, in.Generation, "ComponentDisabled", "Component is disabled")
	if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, "Ready=False"); err != nil {
		return res, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
	logging.Info(ctx, "Component disabled", "name", canonicalName, "generation", in.Generation)
	return res, nil
}

func (r *Reconciler) ensureWorkload(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult) error {
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.workloadManager != nil {
		if err := r.workloadManager.Ensure1PWorkload(ctx, in.Namespace, in.Name, in.Spec); err != nil {
			res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "WorkloadProvisionFailed", err.Error())
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "WorkloadProvisionFailed", err.Error())
			return err
		}
		logging.Debug(ctx, "Ensured 1P child workload", "name", in.Name, "namespace", in.Namespace)
	}
	return nil
}

func (r *Reconciler) evaluateHealth(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	isHealthy, healthMsg := true, "Component is active"
	if r.prober != nil {
		isHealthy, healthMsg = r.prober.ProbeHealth(ctx, in.Spec)
	}
	res.Access = &AccessStatus{EnvVars: make(map[string]string)}

	readyStatus, readyReason, degradedStatus, degradedReason := ConditionStatusFalse, "HealthProbeFailed", ConditionStatusTrue, "HealthProbeFailed"
	if isHealthy {
		readyStatus, readyReason, degradedStatus, degradedReason = ConditionStatusTrue, "ComponentHealthy", ConditionStatusFalse, "ComponentHealthy"
		res.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	} else {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	}
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, readyStatus, in.Generation, readyReason, healthMsg)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, degradedStatus, in.Generation, degradedReason, healthMsg)

	if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, fmt.Sprintf("Ready=%s", readyStatus)); err != nil {
		return res, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
	logging.Debug(ctx, "Evaluated component health", "name", canonicalName, "healthy", isHealthy, "state", res.State.String(), "statusMsg", healthMsg)
	return res, nil
}

// Reconcile executes reconciliation on an unstructured Kubernetes Component object.
func (r *Reconciler) Reconcile(ctx context.Context, obj *unstructured.Unstructured) (*ReconcileResult, error) {
	if obj == nil {
		return nil, errors.New("unstructured object must not be nil")
	}
	hasFinalizer := false
	for _, f := range obj.GetFinalizers() {
		if f == FinalizerDeletionProtection {
			hasFinalizer = true
			break
		}
	}
	spec, dp, enabled := ParseSpecFromUnstructured(obj.Object["spec"])
	return r.ReconcileComponent(ctx, ReconcileComponentInput{
		Namespace:          obj.GetNamespace(),
		Name:               obj.GetName(),
		Generation:         obj.GetGeneration(),
		Spec:               spec,
		IsDeleting:         obj.GetDeletionTimestamp() != nil,
		HasFinalizer:       hasFinalizer,
		DeletionProtection: dp,
		Enabled:            enabled,
		ExistingConditions: ParseConditionsFromUnstructured(obj.Object["status"]),
	})
}

// ParseSpecFromUnstructured parses an unstructured spec object.
func ParseSpecFromUnstructured(specObj any) (*componentspb.ComponentSpec, bool, bool) {
	specMap, ok := specObj.(map[string]any)
	if !ok || specMap == nil {
		return &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}, true, true
	}
	spec := &componentspb.ComponentSpec{
		Type:           parseComponentType(specMap),
		DeletionPolicy: parseDeletionPolicy(specMap),
		Version:        extractStr(specMap, "version"),
		Description:    extractStr(specMap, "description"),
		WrapperRef:     parseWrapperRef(specMap),
		Manifests:      parseManifests(specMap),
	}
	return spec, extractBool(specMap, "deletionProtection", true), extractBool(specMap, "enabled", true)
}

func parseManifests(m map[string]any) []string {
	raw, ok := m["manifests"].([]any)
	if !ok {
		return nil
	}
	var res []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			res = append(res, s)
		}
	}
	return res
}

func extractBool(m map[string]any, k string, def bool) bool {
	if v, ok := m[k].(bool); ok {
		return v
	}
	return def
}
func extractStr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}
func extractInt(m map[string]any, k string) int64 {
	v, ok := m[k]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	case string:
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
	}
	if s, ok := v.(interface{ String() string }); ok {
		if i, err := strconv.ParseInt(s.String(), 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(s.String(), 64); err == nil {
			return int64(f)
		}
	}
	return 0
}

func parseComponentType(m map[string]any) componentspb.ComponentType {
	if ct, ok := m["componentType"].(string); ok && ct == "COMPONENT_TYPE_CUSTOM_3P_WRAPPER" {
		return componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER
	}
	return componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT
}

func parseDeletionPolicy(m map[string]any) componentspb.DeletionPolicy {
	if dp, ok := m["deletionPolicy"].(string); ok && (dp == "Orphan" || dp == "DELETION_POLICY_ORPHAN") {
		return componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN
	}
	return componentspb.DeletionPolicy_DELETION_POLICY_DELETE
}

func parseWrapperRef(m map[string]any) *componentspb.WrapperRef {
	wr, ok := m["wrapperRef"].(map[string]any)
	if !ok || wr == nil {
		return nil
	}
	return &componentspb.WrapperRef{
		CrdGroup: extractStr(wr, "group"), CrdVersion: extractStr(wr, "version"),
		CrdKind: extractStr(wr, "kind"), TargetName: extractStr(wr, "name"), TargetNamespace: extractStr(wr, "namespace"),
	}
}

// ParseConditionsFromUnstructured parses unstructured conditions.
func ParseConditionsFromUnstructured(statusObj any) []Condition {
	statusMap, ok := statusObj.(map[string]any)
	if !ok || statusMap == nil {
		return nil
	}
	condsList, ok := statusMap["conditions"].([]any)
	if !ok {
		return nil
	}
	var res []Condition
	for _, cObj := range condsList {
		if cMap, ok := cObj.(map[string]any); ok {
			c := Condition{
				Type: extractStr(cMap, "type"), Status: extractStr(cMap, "status"),
				Reason: extractStr(cMap, "reason"), Message: extractStr(cMap, "message"),
				ObservedGeneration: extractInt(cMap, "observedGeneration"),
			}
			if ltt, ok := cMap["lastTransitionTime"].(string); ok {
				if t, err := time.Parse(time.RFC3339, ltt); err == nil {
					c.LastTransitionTime = t
				}
			}
			res = append(res, c)
		}
	}
	return res
}

func (r *Reconciler) updateObservedCMS(ctx context.Context, name string, gen int64, state componentspb.ComponentState, statusMsg string) error {
	_, err := r.cmsClient.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name: name, ObservedGeneration: gen, State: state, StatusMessage: statusMsg,
	})
	return err
}

func setCondition(conditions *[]Condition, existing []Condition, condType, status string, gen int64, reason, message string) {
	transitionTime := time.Now().UTC()
	for _, c := range existing {
		if c.Type == condType && c.Status == status && !c.LastTransitionTime.IsZero() {
			transitionTime = c.LastTransitionTime
			break
		}
	}
	newCond := Condition{Type: condType, Status: status, ObservedGeneration: gen, LastTransitionTime: transitionTime, Reason: reason, Message: message}
	for i, c := range *conditions {
		if c.Type == condType {
			(*conditions)[i] = newCond
			return
		}
	}
	*conditions = append(*conditions, newCond)
}
