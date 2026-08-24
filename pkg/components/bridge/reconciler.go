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
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

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
	DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error)
	ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec) (bool, string)
	Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error)
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
	WorkloadOpts       WorkloadOptions
	WrapperOpts        WrapperRef
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
	if in.HasFinalizer && !in.DeletionProtection {
		res.RemoveFinalizer = true
	}
	if err := r.applyIntent(ctx, in, canonicalName, res); err != nil {
		return res, nil
	}
	if !in.Enabled {
		return r.reconcileDisabled(ctx, in, canonicalName, res)
	}
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER {
		return r.reconcile3PWrapper(ctx, in, canonicalName, res)
	}
	if err := r.ensureWorkload(ctx, in, res); err != nil {
		return res, nil
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
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && in.Spec.GetDeletionPolicy() != componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN && r.workloadManager != nil {
		if err := r.workloadManager.Delete1PWorkload(ctx, in.Namespace, in.Name); err != nil {
			return res, fmt.Errorf("failed to delete 1P workload: %w", err)
		}
	}
	if _, err := r.cmsClient.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{Name: canonicalName, DeletionPolicy: in.Spec.GetDeletionPolicy()}); err != nil {
		return res, fmt.Errorf("failed to execute deletion in CMS: %w", err)
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
			return res, nil
		}
	}
	res.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "ComponentDisabled", "Component is disabled")
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusFalse, in.Generation, "ComponentDisabled", "Component is disabled")
	if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, "Ready=False"); err != nil {
		return nil, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
	logging.Info(ctx, "Component disabled", "name", canonicalName, "generation", in.Generation)
	return res, nil
}

func (r *Reconciler) ensureWorkload(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult) error {
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.workloadManager != nil {
		if err := r.workloadManager.Ensure1PWorkload(ctx, in.Namespace, in.Name, in.WorkloadOpts); err != nil {
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
		return nil, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
	logging.Debug(ctx, "Evaluated component health", "name", canonicalName, "healthy", isHealthy, "state", res.State.String(), "statusMsg", healthMsg)
	return res, nil
}

func (r *Reconciler) reconcile3PWrapper(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if in.Spec.GetWrapperRef() == nil {
		return r.reconcile3PWrapperFallback(ctx, in, canonicalName, res)
	}

	if r.prober == nil {
		return r.handle3PReadyStatus(ctx, in, canonicalName, res, nil)
	}

	wRef := in.WrapperOpts
	if wRef.Kind == "" || wRef.Name == "" {
		wRef = ToBridgeWrapperRef(in.Spec.GetWrapperRef(), in.Namespace)
	}

	resStatus, err := r.prober.DetectStatus(ctx, wRef)
	if err != nil {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, "DetectionFailed", err.Error())
	}
	if !resStatus.Found {
		msg := resStatus.Message
		if msg == "" {
			msg = "Underlying 3P resource not found"
		}
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, "ResourceNotFound", msg)
	}
	if !resStatus.Ready {
		msg := resStatus.Message
		if msg == "" {
			msg = "Underlying 3P resource not ready"
		}
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, "ResourceNotReady", msg)
	}

	if ok, reason, msg := r.verifyEndpointHealth(ctx, resStatus.Endpoint); !ok {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, reason, msg)
	}

	return r.handle3PReadyStatus(ctx, in, canonicalName, res, resStatus)
}

func (r *Reconciler) verifyEndpointHealth(ctx context.Context, endpoint string) (bool, string, string) {
	if endpoint == "" {
		return true, "", ""
	}
	probeRes, err := r.prober.Probe(ctx, endpoint, 5*time.Second)
	if err != nil {
		return false, "ProbeFailed", fmt.Sprintf("Health probe failed for %s: %v", endpoint, err)
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

func (r *Reconciler) reconcile3PWrapperFallback(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	isHealthy, healthMsg := true, "Component is active"
	if r.prober != nil {
		isHealthy, healthMsg = r.prober.ProbeHealth(ctx, in.Spec)
	}
	if !isHealthy {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, "HealthProbeFailed", healthMsg)
	}
	return r.handle3PReadyStatus(ctx, in, canonicalName, res, nil)
}

func (r *Reconciler) handle3PUnreadyStatus(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, reason, message string) (*ReconcileResult, error) {
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, reason, message)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, reason, message)
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, "Ready=False"); err != nil {
		return nil, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
	return res, nil
}

func populateAccessEnvVars(access *AccessStatus, resStatus *ResourceStatus, ns string) {
	if resStatus == nil {
		return
	}
	if resStatus.Endpoint != "" {
		if coords := ParseCoordinates(resStatus.Endpoint, ns); coords != nil {
			access.EnvVars["ENDPOINT"] = resStatus.Endpoint
			access.EnvVars["HOST"] = coords.GetHost()
			access.EnvVars["PORT"] = strconv.FormatUint(uint64(coords.GetPort()), 10)
			access.EnvVars["SCHEME"] = coords.GetScheme()
		}
	}
	if resStatus.ConnectionSecret != "" {
		access.EnvVars["SECRET_NAME"] = resStatus.ConnectionSecret
	}
}

func (r *Reconciler) handle3PReadyStatus(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, resStatus *ResourceStatus) (*ReconcileResult, error) {
	msg := "3P wrapper resource is operational and ready"
	if resStatus != nil && resStatus.Message != "" {
		msg = resStatus.Message
	}
	res.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	res.Access = &AccessStatus{EnvVars: make(map[string]string)}
	if resStatus != nil && resStatus.ObservedGeneration > 0 {
		res.ObservedGeneration = resStatus.ObservedGeneration
	}

	populateAccessEnvVars(res.Access, resStatus, in.Namespace)

	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusTrue, res.ObservedGeneration, "ComponentHealthy", msg)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusFalse, res.ObservedGeneration, "ComponentHealthy", msg)

	if err := r.updateObservedCMS(ctx, canonicalName, res.ObservedGeneration, res.State, "Ready=True"); err != nil {
		return nil, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
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
	opts := ParseWorkloadOptions(obj.GetUID(), obj.Object["spec"], spec)
	wOpts := ParseWrapperRefOptions(obj.Object["spec"], obj.GetNamespace())
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
		WorkloadOpts:       opts,
		WrapperOpts:        wOpts,
	})
}

// ParseWorkloadOptions extracts WorkloadOptions from a Kubernetes custom resource spec and UID.
func ParseWorkloadOptions(uid types.UID, specObj any, spec *componentspb.ComponentSpec) WorkloadOptions {
	opts := WorkloadOptions{
		OwnerUID: uid,
	}
	if spec != nil && spec.GetLifecycleControl() != nil && spec.GetLifecycleControl().GetDrainingTimeout() != nil {
		opts.DrainTimeoutSeconds = int32(spec.GetLifecycleControl().GetDrainingTimeout().GetSeconds())
	}
	if spec != nil && spec.GetVersion() != "" {
		opts.ImageTag = spec.GetVersion()
	}
	specMap, ok := specObj.(map[string]any)
	if !ok || specMap == nil {
		return opts
	}
	if tag, ok := specMap["imageTag"].(string); ok && tag != "" {
		opts.ImageTag = tag
	}
	if pMap, ok := specMap["parameters"].(map[string]any); ok {
		opts.Parameters = make(map[string]string, len(pMap))
		for k, v := range pMap {
			if s, ok := v.(string); ok {
				opts.Parameters[k] = s
			} else {
				opts.Parameters[k] = fmt.Sprintf("%v", v)
			}
		}
	}
	return opts
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
	if v, ok := m[k].(int64); ok {
		return v
	}
	if v, ok := m[k].(float64); ok {
		return int64(v)
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

// ToBridgeWrapperRef converts a protobuf WrapperRef to the bridge.WrapperRef data model.
func ToBridgeWrapperRef(pRef *componentspb.WrapperRef, defaultNamespace string) WrapperRef {
	if pRef == nil {
		return WrapperRef{}
	}
	ns := pRef.GetTargetNamespace()
	if ns == "" {
		ns = defaultNamespace
	}
	return WrapperRef{
		Group:     pRef.GetCrdGroup(),
		Version:   pRef.GetCrdVersion(),
		Kind:      pRef.GetCrdKind(),
		Name:      pRef.GetTargetName(),
		Namespace: ns,
	}
}

// ParseWrapperRefOptions extracts a WrapperRef with optional JSONPath queries from a custom resource spec.
func ParseWrapperRefOptions(specObj any, defaultNamespace string) WrapperRef {
	specMap, ok := specObj.(map[string]any)
	if !ok || specMap == nil {
		return WrapperRef{Namespace: defaultNamespace}
	}
	wr, ok := specMap["wrapperRef"].(map[string]any)
	if !ok || wr == nil {
		return WrapperRef{Namespace: defaultNamespace}
	}
	ns := extractStr(wr, "namespace")
	if ns == "" {
		ns = defaultNamespace
	}
	return WrapperRef{
		Group:                    extractStr(wr, "group"),
		Version:                  extractStr(wr, "version"),
		Kind:                     extractStr(wr, "kind"),
		Name:                     extractStr(wr, "name"),
		Namespace:                ns,
		ObservedGenerationPath:   extractStr(wr, "observedGenerationPath"),
		ReadyConditionPath:       extractStr(wr, "readyConditionPath"),
		ExpectedReadyValue:       extractStr(wr, "expectedReadyValue"),
		EndpointJSONPath:         extractStr(wr, "endpointJsonPath"),
		ConnectionSecretJSONPath: extractStr(wr, "connectionSecretJsonPath"),
	}
}

// ParseCoordinates extracts scheme, host, port, service name, and namespace from an endpoint string.
func ParseCoordinates(endpoint, defaultNamespace string) *componentspb.ResolvedComponentCoordinates {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return nil
	}

	scheme := "http"
	hostPort := trimmed
	if strings.Contains(trimmed, "://") {
		if u, err := url.Parse(trimmed); err == nil {
			if u.Scheme != "" {
				scheme = u.Scheme
			}
			hostPort = u.Host
		}
	}

	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
		if scheme == "https" {
			portStr = "443"
		} else if scheme == "http" {
			portStr = "80"
		}
	}

	var port uint32
	if p, err := strconv.ParseUint(portStr, 10, 32); err == nil {
		port = uint32(p)
	}

	svcName, ns := parseServiceAndNamespace(host, defaultNamespace)

	return &componentspb.ResolvedComponentCoordinates{
		Scheme:      scheme,
		Host:        host,
		Port:        port,
		ServiceName: svcName,
		Namespace:   ns,
	}
}

func parseServiceAndNamespace(host, defaultNamespace string) (string, string) {
	if ip := net.ParseIP(host); ip != nil {
		return "", defaultNamespace
	}
	parts := strings.Split(host, ".")
	if len(parts) >= 2 && parts[1] != "svc" {
		return parts[0], parts[1]
	}
	return parts[0], defaultNamespace
}
