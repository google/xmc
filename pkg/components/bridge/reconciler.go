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
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/components/naming"
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
	// SetComponentIntent registers or updates declarative component intent in CMS.
	SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error)
	// UpdateObservedGeneration updates the observed generation, state, and status message of a component in CMS.
	UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error)
	// ExecuteDeletion executes component deletion in the CMS backend.
	ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error)
}

// StatusDetectorHealthProber defines the status detection and active health checking interface for 3P custom resources.
type StatusDetectorHealthProber interface {
	// DetectStatus queries the target 3P custom resource to determine readiness and extract access coordinates.
	DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error)
	// Probe executes an active HTTP/TCP probe against target.
	Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error)
	// ProbeHealth evaluates the runtime health of a component based on its spec and default namespace, returning health status and message.
	ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec, defaultNamespace string) (bool, string)
}

// BridgeProber combines StatusDetector and HealthProber into StatusDetectorHealthProber.
type BridgeProber struct {
	detector     StatusDetector
	healthProber HealthProber
	probeTimeout time.Duration
}

var _ StatusDetectorHealthProber = (*BridgeProber)(nil)

// NewBridgeProber creates a BridgeProber combining detector and health prober.
func NewBridgeProber(detector StatusDetector, prober HealthProber, probeTimeout time.Duration) *BridgeProber {
	if probeTimeout <= 0 {
		probeTimeout = 5 * time.Second
	}
	return &BridgeProber{
		detector:     detector,
		healthProber: prober,
		probeTimeout: probeTimeout,
	}
}

// DetectStatus queries the target 3P resource via the underlying dynamic StatusDetector.
func (p *BridgeProber) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	if p.detector == nil {
		return nil, errors.New("status detector is uninitialized")
	}
	return p.detector.DetectStatus(ctx, ref)
}

// Probe executes an active HTTP/TCP health probe against target using the configured probe timeout.
func (p *BridgeProber) Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	if p.healthProber == nil {
		return nil, errors.New("health prober is uninitialized")
	}
	if timeout <= 0 {
		timeout = p.probeTimeout
	}
	return p.healthProber.Probe(ctx, target, timeout)
}

// ProbeHealth evaluates whether a component is healthy based on its spec and default namespace.
// For 3P wrappers, it queries the status detector and verifies endpoint health via active probing.
func (p *BridgeProber) ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec, defaultNamespace string) (bool, string) {
	if spec == nil {
		return false, "spec is nil"
	}
	if spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER || spec.GetWrapperRef() == nil {
		return true, "Component healthy"
	}
	wRef := ToBridgeWrapperRef(spec.GetWrapperRef(), defaultNamespace)
	if p.detector == nil {
		return false, "status detector is uninitialized"
	}
	res, err := p.detector.DetectStatus(ctx, wRef)
	if err != nil {
		return false, fmt.Sprintf("status detection error: %v", err)
	}
	if res == nil {
		return false, "status detector returned nil status"
	}
	if !res.Found {
		return false, res.Message
	}
	if !res.Ready {
		return false, res.Message
	}
	if ok, probeMsg := p.probe3PEndpoint(ctx, res.Endpoint); !ok {
		return false, probeMsg
	} else if probeMsg != "" {
		return true, probeMsg
	}
	return true, res.Message
}

func (p *BridgeProber) probe3PEndpoint(ctx context.Context, endpoint string) (bool, string) {
	if endpoint == "" || p.healthProber == nil {
		return true, ""
	}
	probeRes, err := p.healthProber.Probe(ctx, endpoint, p.probeTimeout)
	if err != nil {
		return false, fmt.Sprintf("health probe error: %v", err)
	}
	if probeRes == nil {
		return false, "health prober returned nil result"
	}
	if !probeRes.Healthy {
		return false, probeRes.Message
	}
	return true, probeRes.Message
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
	// AddFinalizer indicates whether the deletion protection finalizer should be attached.
	AddFinalizer bool
	// RemoveFinalizer indicates whether the deletion protection finalizer should be detached.
	RemoveFinalizer bool
}

// Reconciler executes reconciliation cycles for XMC Component custom resources.
type Reconciler struct {
	cmsClient   CMSClient
	prober      StatusDetectorHealthProber
	helmManager HelmManager
}

// NewReconciler constructs a new Reconciler instance.
// cms is the required gRPC client for CMS communication.
// prober is an optional health prober for component health checks.
// helmMgr is an optional HelmManager for provisioning 1P component workloads via Helm releases.
func NewReconciler(cms CMSClient, prober StatusDetectorHealthProber, helmMgr HelmManager) (*Reconciler, error) {
	if cms == nil {
		return nil, errors.New("cms client must not be nil")
	}
	return &Reconciler{cmsClient: cms, prober: prober, helmManager: helmMgr}, nil
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
	// IsDeleting indicates whether the custom resource has a non-nil deletionTimestamp.
	IsDeleting bool
	// HasFinalizer indicates whether the deletion protection finalizer is already present on the resource.
	HasFinalizer bool
	// DeletionProtection indicates whether the custom resource is protected against deletion.
	DeletionProtection bool
	// Enabled indicates whether the component should be active.
	Enabled bool
	// ExistingConditions contains conditions previously reported on the custom resource.
	ExistingConditions []Condition
	HelmOpts           HelmComponentOptions
	WrapperOpts        WrapperRef
}

// ReconcileComponent executes a single reconciliation pass for a Component.
// The flow executes the following steps:
// 1. Validates input name and spec.
// 2. If IsDeleting, delegates to reconcileDeletion (gated by DeletionProtection).
// 3. Attaches deletion protection finalizer if not already present.
// 4. Synchronizes declarative intent with CMS via applyIntent.
// 5. If !Enabled, handles deactivation via reconcileDisabled.
// 6. Ensures 1P child workloads via ensure1PComponent or 3P wrapper reconciliation.
// 7. Evaluates runtime health via evaluateHealth and updates observed generation in CMS.
func (r *Reconciler) ReconcileComponent(ctx context.Context, in ReconcileComponentInput) (*ReconcileResult, error) {
	if in.Name == "" || in.Spec == nil {
		return nil, errors.New("component name and spec must not be empty/nil")
	}
	logging.Debug(ctx, "Reconciling Component custom resource", "name", in.Name, "generation", in.Generation, "enabled", in.Enabled, "isDeleting", in.IsDeleting)
	in.Spec.Generation = in.Generation
	canonicalName := naming.CanonicalComponentName(in.Name)
	res := &ReconcileResult{ObservedGeneration: in.Generation, State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION}

	if in.IsDeleting {
		return r.reconcileDeletion(ctx, in, canonicalName, res)
	}

	if !in.HasFinalizer {
		res.AddFinalizer = true
		logging.Info(ctx, "Attaching deletion protection finalizer", "name", in.Name)
	}
	if err := r.applyIntent(ctx, in, canonicalName, res); err != nil {
		return res, err
	}
	if !in.Enabled {
		return r.reconcileDisabled(ctx, in, canonicalName, res)
	}
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER {
		return r.reconcile3PWrapper(ctx, in, canonicalName, res)
	}
	if err := r.ensure1PComponent(ctx, in, res); err != nil {
		return res, err
	}
	return r.evaluateHealth(ctx, in, canonicalName, res)
}

// reconcileDeletion handles component teardown when a deletionTimestamp is present.
// If DeletionProtection is true, deletion is blocked, DeletionBlocked condition is set to True,
// and finalizers remain intact. Otherwise, CMS ExecuteDeletion is called first to validate
// deletion safety before mutating Kubernetes resources. If CMS reports NotFound, deletion
// is treated as idempotently completed in CMS and proceeds to 1P workload teardown.
func (r *Reconciler) reconcileDeletion(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if in.DeletionProtection {
		logging.Warn(ctx, "Component deletion blocked by deletion protection", "name", in.Name, "generation", in.Generation)
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDeletionBlocked, ConditionStatusTrue, in.Generation, "DeletionProtectionEnabled", fmt.Sprintf("Cannot delete %q: deletionProtection is enabled", in.Name))
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "DeletingBlocked", "Deletion blocked by deletion protection")
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusFalse, in.Generation, "DeletingBlocked", "Deletion blocked by deletion protection")
		return res, nil
	}
	if _, err := r.cmsClient.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{Name: canonicalName, DeletionPolicy: in.Spec.GetDeletionPolicy()}); err != nil {
		if status.Code(err) != codes.NotFound {
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "CMSDeletionFailed", err.Error())
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "CMSDeletionFailed", err.Error())
			return res, fmt.Errorf("failed to execute deletion in CMS: %w", err)
		}
		logging.Info(ctx, "Component already deleted in CMS, proceeding with workload teardown", "name", canonicalName)
	} else {
		logging.Info(ctx, "Executed component deletion in CMS", "name", canonicalName, "policy", in.Spec.GetDeletionPolicy().String())
	}
	if err := r.teardown1PComponent(ctx, in, res); err != nil {
		return res, err
	}
	res.RemoveFinalizer = true
	res.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	return res, nil
}

// resolveComponentRef returns the ComponentRef from HelmOpts with fallback to input Name and Namespace.
func resolveComponentRef(in ReconcileComponentInput) ComponentRef {
	ref := in.HelmOpts.Ref
	if ref.Name == "" {
		ref.Name = in.Name
	}
	if ref.Namespace == "" {
		ref.Namespace = in.Namespace
	}
	return ref
}

func (r *Reconciler) teardown1PComponent(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult) error {
	if in.Spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT || r.helmManager == nil {
		return nil
	}
	ref := resolveComponentRef(in)
	if in.Spec.GetDeletionPolicy() == componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN {
		if err := r.helmManager.Orphan1PComponent(ctx, ref); err != nil {
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "HelmOrphanFailed", err.Error())
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "HelmOrphanFailed", err.Error())
			return fmt.Errorf("failed to orphan 1P Helm component: %w", err)
		}
		return nil
	}
	if err := r.helmManager.Purge1PComponent(ctx, ref); err != nil {
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "HelmDeletionFailed", err.Error())
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "HelmDeletionFailed", err.Error())
		return fmt.Errorf("failed to delete 1P Helm component: %w", err)
	}
	return nil
}
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

// reconcileDisabled disables 1P components in Helm when deactivated (enabled: false),
// sets state to COMPONENT_STATE_DISABLED, sets conditions to Ready=False, Degraded=False,
// and updates the observed generation in CMS.
func (r *Reconciler) reconcileDisabled(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
		if err := r.deleteDisabledHelmComponent(ctx, in, res); err != nil {
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

func (r *Reconciler) deleteDisabledHelmComponent(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult) error {
	if r.helmManager == nil {
		logging.Warn(ctx, "HelmManager is nil for 1P component deletion", "name", in.Name, "namespace", in.Namespace)
		return nil
	}
	ref := resolveComponentRef(in)
	// TODO: Hydrate active state and observed generation from existing CR status (.status.observedGeneration)
	// and release storage on controller startup. This avoids redundant Helm metadata revision increments on
	// cold start and resolves the post-restart deactivation edge case.
	if !r.helmManager.IsComponentActive(ref) {
		return nil
	}
	if err := r.helmManager.Delete1PComponent(ctx, ref); err != nil {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "HelmDeletionFailed", err.Error())
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "HelmDeletionFailed", err.Error())
		return fmt.Errorf("failed to delete 1P Helm component: %w", err)
	}
	return nil
}

// ensure1PComponent sets component enablement and parameter values in the Helm release.
func (r *Reconciler) ensure1PComponent(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult) error {
	if in.Spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
		return nil
	}
	if r.helmManager == nil {
		logging.Warn(ctx, "HelmManager is nil; skipping 1P Helm component reconciliation", "name", in.Name, "namespace", in.Namespace)
		return nil
	}
	ref := resolveComponentRef(in)
	if r.helmManager.IsComponentActuated(ref, in.Generation) {
		logging.Debug(ctx, "1P component already actuated at current generation, skipping Helm upgrade", "name", in.Name, "generation", in.Generation)
		return nil
	}
	in.HelmOpts.Ref = ref
	in.HelmOpts.Enabled = in.Enabled
	if err := r.helmManager.Ensure1PComponent(ctx, ref, in.HelmOpts); err != nil {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "HelmUpgradeFailed", err.Error())
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "HelmUpgradeFailed", err.Error())
		return fmt.Errorf("failed to ensure 1P Helm component: %w", err)
	}
	r.helmManager.RecordActuation(ref, in.Generation)
	logging.Debug(ctx, "Ensured 1P Helm component", "name", in.Name, "namespace", in.Namespace, "generation", in.Generation)
	return nil
}

// evaluateHealth probes the runtime health of an active component (in.Enabled == true), sets the Ready and Degraded
// conditions, updates state to COMPONENT_STATE_ENABLED (or PENDING_RECONCILIATION), and reports to CMS.
// Note: Disabled components (in.Enabled == false) are processed exclusively by reconcileDisabled and do not reach evaluateHealth.
func (r *Reconciler) evaluateHealth(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	isHealthy, healthMsg := true, "Component is active"
	if r.prober != nil {
		isHealthy, healthMsg = r.prober.ProbeHealth(ctx, in.Spec, in.Namespace)
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
	hasFinalizer := false
	for _, f := range obj.GetFinalizers() {
		if f == FinalizerDeletionProtection {
			hasFinalizer = true
			break
		}
	}
	spec, dp, enabled := ParseSpecFromUnstructured(obj.Object["spec"])
	opts := ParseHelmComponentOptions(obj.GetName(), obj.GetNamespace(), obj.Object["spec"], spec)
	wopts := ParseWrapperRef(obj.Object["spec"], obj.GetNamespace())
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
		HelmOpts:           opts,
		WrapperOpts:        wopts,
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
// float64 (produced by encoding/json unmarshaling in unstructured maps), and numeric strings or Stringers.
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

// resolveAndValidateWrapperRef resolves WrapperRef from input and validates namespace isolation.
func resolveAndValidateWrapperRef(in ReconcileComponentInput) (WrapperRef, string, string) {
	ref := in.WrapperOpts
	if ref.Name == "" && in.Spec.GetWrapperRef() != nil {
		ref = ToBridgeWrapperRef(in.Spec.GetWrapperRef(), in.Namespace)
	}
	if ref.Name == "" {
		return ref, "InvalidWrapperRef", "3P wrapper target name is empty"
	}
	if in.Namespace != "" && ref.Namespace != "" && ref.Namespace != in.Namespace {
		return ref, "CrossNamespaceForbidden", fmt.Sprintf("cross-namespace wrapperRef is forbidden (component in %q cannot target %q)", in.Namespace, ref.Namespace)
	}
	return ref, "", ""
}

// reconcile3PWrapper executes the reconciliation lifecycle for a third-party custom resource wrapper,
// querying the dynamic StatusDetector to evaluate CR readiness and reporting state to CMS.
func (r *Reconciler) reconcile3PWrapper(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if r.prober == nil {
		return r.reconcile3PWrapperFallback(ctx, in, canonicalName, res)
	}
	ref, unreadyReason, unreadyMsg := resolveAndValidateWrapperRef(in)
	if unreadyReason != "" {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, unreadyReason, unreadyMsg)
	}

	resStatus, err := r.prober.DetectStatus(ctx, ref)
	if err != nil {
		if _, cmsErr := r.handle3PUnreadyStatus(ctx, in, canonicalName, res, "StatusDetectionFailed", err.Error()); cmsErr != nil {
			return res, fmt.Errorf("failed to detect 3P status for %s/%s: %w (CMS update failed: %v)", ref.Namespace, ref.Name, err, cmsErr)
		}
		return res, fmt.Errorf("failed to detect 3P status for %s/%s: %w", ref.Namespace, ref.Name, err)
	}
	if resStatus == nil {
		errmsg := "status detector returned nil status"
		if _, cmsErr := r.handle3PUnreadyStatus(ctx, in, canonicalName, res, "StatusDetectionFailed", errmsg); cmsErr != nil {
			return res, fmt.Errorf("failed to detect 3P status for %s/%s: %s (CMS update failed: %v)", ref.Namespace, ref.Name, errmsg, cmsErr)
		}
		return res, fmt.Errorf("failed to detect 3P status for %s/%s: %s", ref.Namespace, ref.Name, errmsg)
	}
	if !resStatus.Found {
		reason := "ResourceNotFound"
		if strings.Contains(resStatus.Message, "CRD not installed") {
			reason = "CRDNotFound"
		}
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, reason, resStatus.Message)
	}
	if !resStatus.Ready {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, "ResourceNotReady", resStatus.Message)
	}

	if ok, reason, msg := r.verifyEndpointHealth(ctx, resStatus.Endpoint); !ok {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, reason, msg)
	}

	return r.handle3PReadyStatus(ctx, in, canonicalName, res, resStatus)
}

// reconcile3PWrapperFallback performs health evaluation when no dynamic status detector is configured.
func (r *Reconciler) reconcile3PWrapperFallback(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	isHealthy, healthMsg := true, "Component is active"
	if r.prober != nil {
		isHealthy, healthMsg = r.prober.ProbeHealth(ctx, in.Spec, in.Namespace)
	}
	if !isHealthy {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, "HealthProbeFailed", healthMsg)
	}
	return r.handle3PReadyStatus(ctx, in, canonicalName, res, nil)
}

// verifyEndpointHealth performs an active probe against endpoint (if non-empty) to verify data-plane availability.
func (r *Reconciler) verifyEndpointHealth(ctx context.Context, endpoint string) (bool, string, string) {
	if endpoint == "" || r.prober == nil {
		return true, "", ""
	}
	probeRes, err := r.prober.Probe(ctx, endpoint, 0)
	if err != nil {
		return false, "ProbeFailed", fmt.Sprintf("Health probe failed for %s: %v", endpoint, err)
	}
	if probeRes == nil {
		return false, "ProbeFailed", fmt.Sprintf("Health probe returned nil result for %s", endpoint)
	}
	if !probeRes.Healthy {
		msg := probeRes.Message
		if msg == "" {
			msg = fmt.Sprintf("Health probe unhealthy for %s", endpoint)
		}
		return false, "ProbeUnhealthy", msg
	}
	return true, "", ""
}

// handle3PUnreadyStatus sets Ready=False and Degraded=True status conditions when 3P health or readiness fails.
func (r *Reconciler) handle3PUnreadyStatus(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, reason, message string) (*ReconcileResult, error) {
	if message == "" {
		message = fmt.Sprintf("3P wrapper %s is not ready: %s", in.Name, reason)
	}
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, reason, message)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, reason, message)
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, "Ready=False"); err != nil {
		return res, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
	return res, nil
}

// handle3PReadyStatus transitions component state to COMPONENT_STATE_ENABLED and populates Ready=True conditions.
func (r *Reconciler) handle3PReadyStatus(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, resStatus *ResourceStatus) (*ReconcileResult, error) {
	msg := "3P wrapper resource is operational and ready"
	if resStatus != nil && resStatus.Message != "" {
		msg = resStatus.Message
	}
	res.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	populateAccessEnvVars(res, resStatus)

	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusTrue, in.Generation, "ComponentHealthy", msg)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusFalse, in.Generation, "ComponentHealthy", msg)

	if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, "Ready=True"); err != nil {
		return res, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
	return res, nil
}

func populateAccessEnvVars(res *ReconcileResult, resStatus *ResourceStatus) {
	if res == nil || resStatus == nil {
		return
	}
	if res.Access == nil {
		res.Access = &AccessStatus{EnvVars: make(map[string]string)}
	}
	if res.Access.EnvVars == nil {
		res.Access.EnvVars = make(map[string]string)
	}

	coords := ParseCoordinates(resStatus.Endpoint, resStatus.ConnectionSecret)
	if coords.RawEndpoint != "" {
		res.Access.EnvVars["ENDPOINT"] = coords.RawEndpoint
	}
	if coords.Host != "" {
		res.Access.EnvVars["HOST"] = coords.Host
	}
	if coords.Port != "" {
		res.Access.EnvVars["PORT"] = coords.Port
	}
	if coords.Scheme != "" {
		res.Access.EnvVars["SCHEME"] = coords.Scheme
	}
	if coords.SecretName != "" {
		res.Access.EnvVars["SECRET_NAME"] = coords.SecretName
	}
}

// ParseWrapperRef extracts WrapperRef from unstructured custom resource spec map.
func ParseWrapperRef(specObj any, defaultNamespace string) WrapperRef {
	specMap, ok := specObj.(map[string]any)
	if !ok || specMap == nil {
		return WrapperRef{Namespace: defaultNamespace}
	}
	wrapperRaw, ok := specMap["wrapperRef"].(map[string]any)
	if !ok || wrapperRaw == nil {
		return WrapperRef{}
	}
	getString := func(keys ...string) string {
		for _, k := range keys {
			if v, exists := wrapperRaw[k].(string); exists && v != "" {
				return v
			}
		}
		return ""
	}
	ns := getString("targetNamespace", "namespace")
	if ns == "" {
		ns = defaultNamespace
	}
	return WrapperRef{
		Group:                    getString("crdGroup", "group"),
		Version:                  getString("crdVersion", "version"),
		Kind:                     getString("crdKind", "kind"),
		Name:                     getString("targetName", "name"),
		Namespace:                ns,
		ObservedGenerationPath:   getString("observedGenerationPath", "observed_generation_path"),
		ReadyConditionPath:       getString("readyConditionPath", "ready_condition_path", "readyPath"),
		ExpectedReadyValue:       getString("expectedReadyValue", "expected_ready_value"),
		EndpointJSONPath:         getString("endpointJsonPath", "endpoint_json_path", "endpointJsonpath"),
		ConnectionSecretJSONPath: getString("connectionSecretJsonPath", "connection_secret_json_path", "secretNameJsonpath"),
	}
}
