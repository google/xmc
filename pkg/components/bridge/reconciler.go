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
	ConditionTypeReady    = "Ready"
	ConditionTypeDegraded = "Degraded"
	ConditionStatusTrue   = "True"
	ConditionStatusFalse  = "False"
)

// CMSClient defines the interface for communicating with the CMS control plane.
type CMSClient interface {
	// SetComponentIntent registers or updates declarative component intent in CMS.
	SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error)
	// UpdateObservedGeneration updates the observed generation, state, and status message of a component in CMS.
	UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error)
}

// StatusDetectorHealthProber defines the health checking interface for custom resources.
type StatusDetectorHealthProber interface {
	// ProbeHealth evaluates the runtime health of a component based on its spec, returning health status and message.
	ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec) (bool, string)
}

// WorkloadManager defines the lifecycle manager interface for 1P child workloads.
type WorkloadManager interface {
	// Ensure1PWorkload reconciles Kubernetes Deployment and Service workloads matching spec.
	Ensure1PWorkload(ctx context.Context, namespace string, name string, spec *componentspb.ComponentSpec) error
	// Delete1PWorkload deletes child workloads associated with the named component.
	Delete1PWorkload(ctx context.Context, namespace string, name string) error
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

// Condition represents a standardized Kubernetes custom resource condition.
type Condition struct {
	// Type is the condition identifier (e.g. Ready, Degraded, DeletionBlocked).
	Type string
	// Status is the condition state ("True" or "False").
	Status string
	// Reason is a single-word camelCase reason explaining the condition transition.
	Reason string
	// Message is a human-readable description of the condition details.
	Message string
	// ObservedGeneration records the metadata.generation at the time of evaluation.
	ObservedGeneration int64
	// LastTransitionTime records when the condition transitioned between status values.
	LastTransitionTime time.Time
}

// AccessStatus encapsulates network and access metadata (such as environment variables) for a component.
type AccessStatus struct {
	// EnvVars maps configuration and coordinate keys to their resolved values.
	EnvVars map[string]string
}

// ReconcileResult encapsulates the outcome of a single component reconciliation pass.
type ReconcileResult struct {
	// ObservedGeneration is the custom resource metadata.generation evaluated.
	ObservedGeneration int64
	// State is the resulting lifecycle state in CMS.
	State componentspb.ComponentState
	// Conditions is the updated list of Kubernetes status conditions.
	Conditions []Condition
	// Access contains resolved network access coordinates and environment variables.
	Access *AccessStatus
}

// Reconciler executes reconciliation cycles for XMC Component custom resources.
type Reconciler struct {
	cmsClient       CMSClient
	prober          StatusDetectorHealthProber
	workloadManager WorkloadManager
}

// NewReconciler constructs a new Reconciler instance.
// cms is the required gRPC client for CMS communication.
// prober is an optional health prober for component health checks.
// wm is an optional WorkloadManager for provisioning 1P child workloads.
func NewReconciler(cms CMSClient, prober StatusDetectorHealthProber, wm WorkloadManager) (*Reconciler, error) {
	if cms == nil {
		return nil, errors.New("cms client must not be nil")
	}
	return &Reconciler{cmsClient: cms, prober: prober, workloadManager: wm}, nil
}

// ReconcileComponentInput bundles parameters for a component reconciliation pass.
type ReconcileComponentInput struct {
	// Namespace is the Kubernetes namespace of the Component custom resource.
	Namespace string
	// Name is the custom resource name.
	Name string
	// Generation is the metadata.generation of the custom resource.
	Generation int64
	// Spec is the parsed protobuf ComponentSpec.
	Spec *componentspb.ComponentSpec
	// Enabled indicates whether the component should be active.
	Enabled bool
	// ExistingConditions contains conditions previously reported on the custom resource.
	ExistingConditions []Condition
}

// ReconcileComponent executes a single reconciliation pass for a Component.
// The flow executes the following steps:
// 1. Validates input name and spec.
// 2. Synchronizes declarative intent with CMS via applyIntent.
// 3. If !Enabled, handles deactivation via reconcileDisabled.
// 4. Ensures 1P child workloads via ensureWorkload.
// 5. Evaluates runtime health via evaluateHealth and updates observed generation in CMS.
func (r *Reconciler) ReconcileComponent(ctx context.Context, in ReconcileComponentInput) (*ReconcileResult, error) {
	if in.Name == "" || in.Spec == nil {
		return nil, errors.New("component name and spec must not be empty/nil")
	}
	logging.Debug(ctx, "Reconciling Component custom resource", "name", in.Name, "generation", in.Generation, "enabled", in.Enabled)
	in.Spec.Generation = in.Generation
	canonicalName := store.CanonicalComponentName(in.Name)
	res := &ReconcileResult{ObservedGeneration: in.Generation, State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION}

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

// applyIntent registers the desired ComponentSpec in CMS via SetComponentIntent RPC.
// If CMS intent registration fails, conditions are updated with Ready=False, Degraded=True
// and an error is returned without tearing down existing data-plane workloads.
func (r *Reconciler) applyIntent(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) error {
	setResp, err := r.cmsClient.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: canonicalName, Spec: in.Spec})
	if err != nil {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "IntentApplicationFailed", err.Error())
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "IntentApplicationFailed", err.Error())
		return fmt.Errorf("failed to apply component intent to CMS: %w", err)
	}
	if setResp != nil && setResp.GetComponent() != nil && setResp.GetComponent().GetStatus() != nil {
		res.State = setResp.GetComponent().GetStatus().GetState()
	}
	logging.Info(ctx, "Applied component intent to CMS", "name", canonicalName, "generation", in.Generation, "state", res.State.String())
	return nil
}

// reconcileDisabled tears down 1P child workloads when the component is deactivated (enabled: false),
// sets state to COMPONENT_STATE_DISABLED, sets conditions to Ready=False, Degraded=False,
// and updates the observed generation in CMS.
func (r *Reconciler) reconcileDisabled(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
		if err := r.deleteDisabledWorkload(ctx, in, res); err != nil {
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

func (r *Reconciler) deleteDisabledWorkload(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult) error {
	if r.workloadManager == nil {
		logging.Warn(ctx, "WorkloadManager is nil for 1P component deletion", "name", in.Name, "namespace", in.Namespace)
		return nil
	}
	if err := r.workloadManager.Delete1PWorkload(ctx, in.Namespace, in.Name); err != nil {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "WorkloadDeletionFailed", err.Error())
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "WorkloadDeletionFailed", err.Error())
		return fmt.Errorf("failed to delete 1P workload: %w", err)
	}
	return nil
}

// ensureWorkload provisions or updates 1P child Kubernetes workloads (Deployment/Service)
// via WorkloadManager.Ensure1PWorkload.
func (r *Reconciler) ensureWorkload(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult) error {
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
		if r.workloadManager == nil {
			logging.Warn(ctx, "WorkloadManager is nil; skipping 1P child workload provisioning", "name", in.Name, "namespace", in.Namespace)
			return nil
		}
		if err := r.workloadManager.Ensure1PWorkload(ctx, in.Namespace, in.Name, in.Spec); err != nil {
			res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "WorkloadProvisionFailed", err.Error())
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "WorkloadProvisionFailed", err.Error())
			return fmt.Errorf("failed to ensure 1P workload: %w", err)
		}
		logging.Debug(ctx, "Ensured 1P child workload", "name", in.Name, "namespace", in.Namespace)
	}
	return nil
}

// evaluateHealth probes the runtime health of an active component (in.Enabled == true), sets the Ready and Degraded
// conditions, updates state to COMPONENT_STATE_ENABLED (or PENDING_RECONCILIATION), and reports to CMS.
// Note: Disabled components (in.Enabled == false) are processed exclusively by reconcileDisabled and do not reach evaluateHealth.
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
	logging.Info(ctx, "Evaluated component health", "name", canonicalName, "generation", in.Generation, "state", res.State.String(), "healthy", isHealthy)
	return res, nil
}

// Reconcile is the top-level reconciler entrypoint for an unstructured Kubernetes Component custom resource.
// It parses the spec, generation, and status conditions into ReconcileComponentInput and calls ReconcileComponent.
func (r *Reconciler) Reconcile(ctx context.Context, obj *unstructured.Unstructured) (*ReconcileResult, error) {
	if obj == nil {
		return nil, errors.New("unstructured object must not be nil")
	}
	spec, _, enabled := ParseSpecFromUnstructured(obj.Object["spec"])
	return r.ReconcileComponent(ctx, ReconcileComponentInput{
		Namespace:          obj.GetNamespace(),
		Name:               obj.GetName(),
		Generation:         obj.GetGeneration(),
		Spec:               spec,
		Enabled:            enabled,
		ExistingConditions: ParseConditionsFromUnstructured(obj.Object["status"]),
	})
}

// ParseSpecFromUnstructured converts an unstructured spec map into a typed protobuf ComponentSpec,
// alongside the boolean flags deletionProtection (default true) and enabled (default true).
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

// parseManifests converts an unstructured manifests slice into a slice of YAML string documents.
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

// extractBool extracts a boolean key from map m, returning def if missing or of invalid type.
func extractBool(m map[string]any, k string, def bool) bool {
	if v, ok := m[k].(bool); ok {
		return v
	}
	return def
}

// extractStr extracts a string key from map m, returning "" if missing or of invalid type.
func extractStr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// extractInt extracts an int64 integer key from map m. Handles standard Go integers (int64, int),
// float64 (produced by encoding/json unmarshaling in unstructured maps), and numeric strings.
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
	return 0
}

// parseComponentType parses the componentType string field into its corresponding protobuf enum.
func parseComponentType(m map[string]any) componentspb.ComponentType {
	if ct, ok := m["componentType"].(string); ok && ct == "COMPONENT_TYPE_CUSTOM_3P_WRAPPER" {
		return componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER
	}
	return componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT
}

// parseDeletionPolicy parses the deletionPolicy string field into its corresponding protobuf enum (Delete vs Orphan).
func parseDeletionPolicy(m map[string]any) componentspb.DeletionPolicy {
	if dp, ok := m["deletionPolicy"].(string); ok && (dp == "Orphan" || dp == "DELETION_POLICY_ORPHAN") {
		return componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN
	}
	return componentspb.DeletionPolicy_DELETION_POLICY_DELETE
}

// parseWrapperRef extracts a 3P wrapper reference from the unstructured spec map,
// supporting both camelCase and protobuf field names.
func parseWrapperRef(m map[string]any) *componentspb.WrapperRef {
	wr, ok := m["wrapperRef"].(map[string]any)
	if !ok || wr == nil {
		return nil
	}
	return &componentspb.WrapperRef{
		CrdGroup:        firstNonEmpty(extractStr(wr, "crdGroup"), extractStr(wr, "group")),
		CrdVersion:      firstNonEmpty(extractStr(wr, "crdVersion"), extractStr(wr, "version")),
		CrdKind:         firstNonEmpty(extractStr(wr, "crdKind"), extractStr(wr, "kind")),
		TargetName:      firstNonEmpty(extractStr(wr, "targetName"), extractStr(wr, "name")),
		TargetNamespace: firstNonEmpty(extractStr(wr, "targetNamespace"), extractStr(wr, "namespace")),
	}
}

// firstNonEmpty returns the first string in values that is not empty, or "" if all are empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ParseConditionsFromUnstructured extracts and parses a slice of Condition objects from the .status map,
// converting lastTransitionTime RFC3339 strings into time.Time values.
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

// updateObservedCMS sends an UpdateObservedGeneration RPC to CMS.
func (r *Reconciler) updateObservedCMS(ctx context.Context, name string, gen int64, state componentspb.ComponentState, statusMsg string) error {
	_, err := r.cmsClient.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name: name, ObservedGeneration: gen, State: state, StatusMessage: statusMsg,
	})
	return err
}

// setCondition inserts or updates a Condition in the conditions slice. If the condition already
// existed with the same status, its lastTransitionTime is preserved; otherwise, it is updated to time.Now().UTC().
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
