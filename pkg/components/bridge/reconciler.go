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

// Package bridge implements the GitOps Bridge Reconciler, dynamic 3P status detection,
// JSONPath evaluation, and active network health probing for XMC components.
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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/retry"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

const (
	// ConditionTypeReady indicates overall component operational readiness.
	ConditionTypeReady = "Ready"
	// ConditionTypeDegraded indicates component failure or unhealthy state.
	ConditionTypeDegraded = "Degraded"

	// Condition reason constants.
	ReasonComponentReady    = "ComponentReady"
	ReasonComponentDisabled = "ComponentDisabled"
	ReasonResourceNotFound  = "ResourceNotFound"
	ReasonResourceNotReady  = "ResourceNotReady"
	ReasonProbeFailed       = "ProbeFailed"
	ReasonDetectionFailed   = "DetectionFailed"
	ReasonCRDNotFound       = "CRDNotFound"
	ReasonHealthy           = "Healthy"
	ReasonDisabled          = "Disabled"
	ReasonDegraded          = "Degraded"
	ReasonResourceMissing   = "ResourceMissing"
	ReasonProbeUnhealthy    = "ProbeUnhealthy"
	ReasonDetectionError    = "DetectionError"
	ReasonCRDMissing        = "CRDMissing"

	defaultComponentProbeTimeout = 5 * time.Second
)

// ComponentGVR identifies the Kubernetes GroupVersionResource for Component CRs.
var ComponentGVR = schema.GroupVersionResource{
	Group:    "xmc.dev",
	Version:  "v1alpha1",
	Resource: "components",
}

// CMSClient defines the subset of ComponentManagerServiceClient methods used by Reconciler.
type CMSClient interface {
	SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error)
	UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error)
	CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error)
	ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error)
}

// StatusDetectorHealthProber abstracts status detection and active health probing.
type StatusDetectorHealthProber interface {
	DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error)
	Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error)
}

// ReconcileResult contains the outcome of a component reconciliation.
type ReconcileResult struct {
	Name               string                                     `json:"name"`
	Namespace          string                                     `json:"namespace"`
	ObservedGeneration int64                                      `json:"observedGeneration"`
	State              componentspb.ComponentState                `json:"state"`
	Conditions         []metav1.Condition                         `json:"conditions"`
	Coordinates        *componentspb.ResolvedComponentCoordinates `json:"coordinates,omitempty"`
	Message            string                                     `json:"message,omitempty"`
	RequeueAfter       time.Duration                              `json:"requeueAfter,omitempty"`
}

// ReconcilerOption configures a Reconciler instance.
type ReconcilerOption func(*Reconciler)

// WithReconcilerProbeTimeout sets the health probe timeout for the reconciler.
func WithReconcilerProbeTimeout(timeout time.Duration) ReconcilerOption {
	return func(r *Reconciler) {
		if timeout > 0 {
			r.probeTimeout = timeout
		}
	}
}

// Reconciler synchronizes Kubernetes Component CRs with the ComponentManagerService and dynamic 3P CRDs.
type Reconciler struct {
	cmsClient     CMSClient
	prober        StatusDetectorHealthProber
	dynamicClient dynamic.Interface
	probeTimeout  time.Duration
}

// NewReconciler creates a new Reconciler with validated dependencies.
func NewReconciler(cmsClient CMSClient, prober StatusDetectorHealthProber, dynamicClient dynamic.Interface, opts ...ReconcilerOption) (*Reconciler, error) {
	if cmsClient == nil {
		return nil, errors.New("cmsClient is required")
	}
	if prober == nil {
		return nil, errors.New("prober is required")
	}

	r := &Reconciler{
		cmsClient:     cmsClient,
		prober:        prober,
		dynamicClient: dynamicClient,
		probeTimeout:  defaultComponentProbeTimeout,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r, nil
}

// CanonicalComponentName ensures the resource name follows components/{name} format.
func CanonicalComponentName(name string) string {
	trimmed := strings.TrimSpace(name)
	if strings.HasPrefix(trimmed, "components/") {
		return trimmed
	}
	return "components/" + trimmed
}

// TrimComponentName strips the components/ prefix if present.
func TrimComponentName(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), "components/")
}

// ReconcileComponent reconciles a component with default enabled state.
func (r *Reconciler) ReconcileComponent(
	ctx context.Context,
	namespace, name string,
	generation int64,
	spec *componentspb.ComponentSpec,
	existingConditions ...[]metav1.Condition,
) (*ReconcileResult, error) {
	return r.ReconcileComponentEnabled(ctx, namespace, name, generation, spec, true, existingConditions...)
}

// ReconcileComponentEnabled reconciles a component with explicit enabled flag and custom wrapper paths.
func (r *Reconciler) ReconcileComponentEnabled(
	ctx context.Context,
	namespace, name string,
	generation int64,
	spec *componentspb.ComponentSpec,
	enabled bool,
	existingConditions ...[]metav1.Condition,
) (*ReconcileResult, error) {
	if r == nil || r.cmsClient == nil || r.prober == nil {
		return nil, errors.New("reconciler or required dependencies are uninitialized")
	}
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("component name is required")
	}
	if spec == nil {
		return nil, errors.New("component spec is required")
	}

	canonicalName := CanonicalComponentName(name)
	shortName := TrimComponentName(name)
	specCopy := proto.Clone(spec).(*componentspb.ComponentSpec)
	if specCopy.Generation == 0 && generation > 0 {
		specCopy.Generation = generation
	}

	var conditions []metav1.Condition
	if len(existingConditions) > 0 && existingConditions[0] != nil {
		conditions = append(conditions, existingConditions[0]...)
	}

	_, err := r.cmsClient.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
		Name: canonicalName,
		Spec: specCopy,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set component intent in CMS: %w", err)
	}

	if !enabled {
		return r.reconcileDisabled(ctx, canonicalName, shortName, namespace, generation, conditions)
	}

	if is3PWrapper(specCopy) {
		return r.reconcile3PWrapper(ctx, canonicalName, shortName, namespace, generation, specCopy, conditions)
	}

	return r.reconcile1PComponent(ctx, canonicalName, shortName, namespace, generation, specCopy, conditions)
}

// Reconcile reconciles an unstructured Component.xmc.dev custom resource.
func (r *Reconciler) Reconcile(ctx context.Context, obj *unstructured.Unstructured) (*ReconcileResult, error) {
	if obj == nil {
		return nil, errors.New("unstructured object is nil")
	}

	name := obj.GetName()
	ns := obj.GetNamespace()
	gen := obj.GetGeneration()

	spec, enabled, err := UnstructuredToSpec(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to extract spec from unstructured component: %w", err)
	}

	existingConditions := ExtractConditions(obj)
	result, err := r.ReconcileComponentEnabled(ctx, ns, name, gen, spec, enabled, existingConditions)
	if err != nil {
		return nil, err
	}

	if r.dynamicClient != nil && ns != "" && name != "" {
		if err := r.updateCRStatus(ctx, ns, name, result); err != nil {
			return nil, fmt.Errorf("failed to update CR status: %w", err)
		}
	}

	return result, nil
}

func is3PWrapper(spec *componentspb.ComponentSpec) bool {
	if spec == nil {
		return false
	}
	if spec.Type == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER {
		return true
	}
	return spec.WrapperRef != nil && spec.WrapperRef.CrdKind != ""
}

func (r *Reconciler) reconcileDisabled(
	ctx context.Context,
	canonicalName, shortName, namespace string,
	generation int64,
	conditions []metav1.Condition,
) (*ReconcileResult, error) {
	_, err := r.cmsClient.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{
		Name: canonicalName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to complete deactivation in CMS: %w", err)
	}

	setOrUpdateCondition(&conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		Reason:             ReasonComponentDisabled,
		Message:            "Component is disabled",
	})
	setOrUpdateCondition(&conditions, metav1.Condition{
		Type:               ConditionTypeDegraded,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		Reason:             ReasonDisabled,
		Message:            "Component is disabled",
	})

	_, err = r.cmsClient.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name:               canonicalName,
		ObservedGeneration: generation,
		State:              componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		StatusMessage:      "Component is disabled",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}

	return &ReconcileResult{
		Name:               shortName,
		Namespace:          namespace,
		ObservedGeneration: generation,
		State:              componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		Conditions:         conditions,
		Message:            "Component disabled",
	}, nil
}

func (r *Reconciler) reconcile3PWrapper(
	ctx context.Context,
	canonicalName, shortName, namespace string,
	generation int64,
	spec *componentspb.ComponentSpec,
	conditions []metav1.Condition,
) (*ReconcileResult, error) {
	wRef := toBridgeWrapperRef(spec.WrapperRef, namespace)

	resStatus, err := r.prober.DetectStatus(ctx, wRef)
	if err != nil {
		return r.handleDetectionError(ctx, canonicalName, shortName, namespace, generation, conditions, err)
	}
	if !resStatus.Found {
		return r.handleNotFound(ctx, canonicalName, shortName, namespace, generation, conditions, resStatus.Message)
	}
	if !resStatus.Ready {
		return r.handleNotReady(ctx, canonicalName, shortName, namespace, generation, conditions, resStatus.Message)
	}

	if resStatus.Endpoint != "" {
		if result, err := r.probeEndpoint(ctx, canonicalName, shortName, namespace, generation, conditions, resStatus.Endpoint); result != nil || err != nil {
			return result, err
		}
	}

	return r.handleReady(ctx, canonicalName, shortName, namespace, generation, conditions, resStatus)
}

func (r *Reconciler) handleDegraded(
	ctx context.Context,
	canonicalName, shortName, namespace string,
	generation int64,
	conditions []metav1.Condition,
	readyReason, degradedReason, message string,
) (*ReconcileResult, error) {
	setOrUpdateCondition(&conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		Reason:             readyReason,
		Message:            message,
	})
	setOrUpdateCondition(&conditions, metav1.Condition{
		Type:               ConditionTypeDegraded,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: generation,
		Reason:             degradedReason,
		Message:            message,
	})

	_, _ = r.cmsClient.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name:               canonicalName,
		ObservedGeneration: generation,
		State:              componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		StatusMessage:      message,
	})

	return &ReconcileResult{
		Name:               shortName,
		Namespace:          namespace,
		ObservedGeneration: generation,
		State:              componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Conditions:         conditions,
		Message:            message,
	}, nil
}

func (r *Reconciler) handleDetectionError(
	ctx context.Context,
	canonicalName, shortName, namespace string,
	generation int64,
	conditions []metav1.Condition,
	err error,
) (*ReconcileResult, error) {
	reason := ReasonDetectionError
	if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
		reason = ReasonCRDMissing
	}
	return r.handleDegraded(ctx, canonicalName, shortName, namespace, generation, conditions, ReasonDetectionFailed, reason, err.Error())
}

func (r *Reconciler) handleNotFound(
	ctx context.Context,
	canonicalName, shortName, namespace string,
	generation int64,
	conditions []metav1.Condition,
	message string,
) (*ReconcileResult, error) {
	if message == "" {
		message = "Underlying 3P resource not found"
	}
	return r.handleDegraded(ctx, canonicalName, shortName, namespace, generation, conditions, ReasonResourceNotFound, ReasonResourceMissing, message)
}

func (r *Reconciler) handleNotReady(
	ctx context.Context,
	canonicalName, shortName, namespace string,
	generation int64,
	conditions []metav1.Condition,
	message string,
) (*ReconcileResult, error) {
	if message == "" {
		message = "Underlying 3P resource is not ready"
	}
	return r.handleDegraded(ctx, canonicalName, shortName, namespace, generation, conditions, ReasonResourceNotReady, ReasonDegraded, message)
}

func (r *Reconciler) probeEndpoint(
	ctx context.Context,
	canonicalName, shortName, namespace string,
	generation int64,
	conditions []metav1.Condition,
	endpoint string,
) (*ReconcileResult, error) {
	timeout := r.probeTimeout
	if timeout <= 0 {
		timeout = defaultComponentProbeTimeout
	}
	probeRes, err := r.prober.Probe(ctx, endpoint, timeout)
	if err != nil || (probeRes != nil && !probeRes.Healthy) {
		msg := "Active health probe failed"
		if err != nil {
			msg = err.Error()
		} else if probeRes != nil && probeRes.Message != "" {
			msg = probeRes.Message
		}

		return r.handleDegraded(ctx, canonicalName, shortName, namespace, generation, conditions, ReasonProbeFailed, ReasonProbeUnhealthy, msg)
	}
	return nil, nil
}

func (r *Reconciler) handleReady(
	ctx context.Context,
	canonicalName, shortName, namespace string,
	generation int64,
	conditions []metav1.Condition,
	resStatus *ResourceStatus,
) (*ReconcileResult, error) {
	setOrUpdateCondition(&conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: generation,
		Reason:             ReasonComponentReady,
		Message:            "Component is ready and healthy",
	})
	setOrUpdateCondition(&conditions, metav1.Condition{
		Type:               ConditionTypeDegraded,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		Reason:             ReasonHealthy,
		Message:            "No degraded conditions detected",
	})

	var coords *componentspb.ResolvedComponentCoordinates
	if resStatus.Endpoint != "" {
		coords = ParseCoordinates(resStatus.Endpoint, namespace)
	}

	_, err := r.cmsClient.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name:               canonicalName,
		ObservedGeneration: generation,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		StatusMessage:      "Component is active and healthy",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}

	return &ReconcileResult{
		Name:               shortName,
		Namespace:          namespace,
		ObservedGeneration: generation,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Conditions:         conditions,
		Coordinates:        coords,
		Message:            "Component ready",
	}, nil
}

func (r *Reconciler) reconcile1PComponent(
	ctx context.Context,
	canonicalName, shortName, namespace string,
	generation int64,
	spec *componentspb.ComponentSpec,
	conditions []metav1.Condition,
) (*ReconcileResult, error) {
	setOrUpdateCondition(&conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: generation,
		Reason:             ReasonComponentReady,
		Message:            "1P Component is configured",
	})
	setOrUpdateCondition(&conditions, metav1.Condition{
		Type:               ConditionTypeDegraded,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		Reason:             ReasonHealthy,
		Message:            "No degraded conditions detected",
	})

	_, err := r.cmsClient.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name:               canonicalName,
		ObservedGeneration: generation,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		StatusMessage:      "1P Component reconciled",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}

	return &ReconcileResult{
		Name:               shortName,
		Namespace:          namespace,
		ObservedGeneration: generation,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Conditions:         conditions,
		Message:            "1P Component ready",
	}, nil
}

func setOrUpdateCondition(conditions *[]metav1.Condition, newCond metav1.Condition) {
	if conditions == nil {
		return
	}
	meta.SetStatusCondition(conditions, newCond)
}

func toBridgeWrapperRef(pRef *componentspb.WrapperRef, defaultNamespace string) WrapperRef {
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

// ParseCoordinates extracts hostname, service name, namespace, port, and scheme from endpoint.
func ParseCoordinates(endpoint, defaultNamespace string) *componentspb.ResolvedComponentCoordinates {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return nil
	}

	scheme := "http"
	hostPort := trimmed
	if strings.Contains(trimmed, "://") {
		if u, err := url.Parse(trimmed); err == nil {
			scheme = u.Scheme
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
		ServiceName: svcName,
		Namespace:   ns,
		Port:        port,
	}
}

func parseServiceAndNamespace(host, defaultNamespace string) (string, string) {
	if net.ParseIP(host) != nil {
		return "", defaultNamespace
	}
	parts := strings.Split(host, ".")
	svcName := parts[0]
	ns := defaultNamespace
	if len(parts) >= 2 && parts[1] != "" && parts[1] != "local" && parts[1] != "svc" {
		ns = parts[1]
	}
	return svcName, ns
}

// UnstructuredToSpec extracts ComponentSpec and enabled flag from an Unstructured Kubernetes object.
func UnstructuredToSpec(obj *unstructured.Unstructured) (*componentspb.ComponentSpec, bool, error) {
	if obj == nil {
		return nil, false, errors.New("unstructured object is nil")
	}

	specMap, found, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !found {
		return nil, false, fmt.Errorf("spec field missing or invalid: %w", err)
	}

	enabled := true
	if e, ok := specMap["enabled"].(bool); ok {
		enabled = e
	}

	compTypeStr, _ := specMap["componentType"].(string)
	delPolicyStr, _ := specMap["deletionPolicy"].(string)
	version, _ := specMap["version"].(string)
	desc, _ := specMap["description"].(string)

	var wRef *componentspb.WrapperRef
	if wMap, ok := specMap["wrapperRef"].(map[string]any); ok {
		wRef = parseWrapperRefMap(wMap)
	}

	compType := parseComponentType(compTypeStr, wRef != nil && wRef.CrdKind != "")
	delPolicy := parseDeletionPolicy(delPolicyStr)

	var lc *componentspb.LifecycleControl
	if lcMap, ok := specMap["lifecycleControl"].(map[string]any); ok {
		lc = parseLifecycleControlMap(lcMap)
	}

	manifests := extractManifests(specMap)

	spec := &componentspb.ComponentSpec{
		Type:             compType,
		Version:          version,
		DeletionPolicy:   delPolicy,
		WrapperRef:       wRef,
		LifecycleControl: lc,
		Manifests:        manifests,
		Generation:       obj.GetGeneration(),
		Description:      desc,
	}

	return spec, enabled, nil
}

func parseComponentType(raw string, hasWrapper bool) componentspb.ComponentType {
	if strings.Contains(strings.ToUpper(raw), "3P") || strings.Contains(strings.ToUpper(raw), "WRAPPER") || hasWrapper {
		return componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER
	}
	return componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT
}

func parseDeletionPolicy(raw string) componentspb.DeletionPolicy {
	if strings.EqualFold(raw, "Orphan") {
		return componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN
	}
	return componentspb.DeletionPolicy_DELETION_POLICY_DELETE
}

func parseWrapperRefMap(m map[string]any) *componentspb.WrapperRef {
	group, _ := m["group"].(string)
	if group == "" {
		group, _ = m["crdGroup"].(string)
	}
	version, _ := m["version"].(string)
	if version == "" {
		version, _ = m["crdVersion"].(string)
	}
	kind, _ := m["kind"].(string)
	if kind == "" {
		kind, _ = m["crdKind"].(string)
	}
	name, _ := m["name"].(string)
	if name == "" {
		name, _ = m["targetName"].(string)
	}
	ns, _ := m["namespace"].(string)
	if ns == "" {
		ns, _ = m["targetNamespace"].(string)
	}
	return &componentspb.WrapperRef{
		CrdGroup:        group,
		CrdVersion:      version,
		CrdKind:         kind,
		TargetName:      name,
		TargetNamespace: ns,
	}
}

func parseLifecycleControlMap(m map[string]any) *componentspb.LifecycleControl {
	lc := &componentspb.LifecycleControl{}
	if drainSec, ok := m["drainTimeoutSeconds"].(int64); ok && drainSec > 0 {
		lc.DrainingTimeout = durationpb.New(time.Duration(drainSec) * time.Second)
	} else if drainFloat, ok := m["drainTimeoutSeconds"].(float64); ok && drainFloat > 0 {
		lc.DrainingTimeout = durationpb.New(time.Duration(drainFloat) * time.Second)
	}
	if force, ok := m["forceUpgrade"].(bool); ok {
		lc.ForceDeletion = force
	}
	return lc
}

func extractManifests(specMap map[string]any) []string {
	var manifests []string
	if mList, ok := specMap["manifests"].([]any); ok {
		for _, item := range mList {
			if str, ok := item.(string); ok {
				manifests = append(manifests, str)
			}
		}
	}
	return manifests
}

// ExtractConditions reads conditions from .status.conditions in an unstructured object.
func ExtractConditions(obj *unstructured.Unstructured) []metav1.Condition {
	if obj == nil {
		return nil
	}
	rawConds, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return nil
	}
	var conditions []metav1.Condition
	for _, raw := range rawConds {
		if condMap, ok := raw.(map[string]any); ok {
			condType, _ := condMap["type"].(string)
			statusStr, _ := condMap["status"].(string)
			reason, _ := condMap["reason"].(string)
			msg, _ := condMap["message"].(string)
			obsGen, _, _ := unstructured.NestedInt64(condMap, "observedGeneration")
			conditions = append(conditions, metav1.Condition{
				Type:               condType,
				Status:             metav1.ConditionStatus(statusStr),
				ObservedGeneration: obsGen,
				Reason:             reason,
				Message:            msg,
			})
		}
	}
	return conditions
}

func (r *Reconciler) updateCRStatus(ctx context.Context, ns, name string, result *ReconcileResult) error {
	if r.dynamicClient == nil || result == nil {
		return nil
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj, err := r.dynamicClient.Resource(ComponentGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		statusMap, _, _ := unstructured.NestedMap(obj.Object, "status")
		if statusMap == nil {
			statusMap = make(map[string]any)
		}

		statusMap["observedGeneration"] = result.ObservedGeneration
		statusMap["state"] = result.State.String()

		var condMaps []any
		for _, c := range result.Conditions {
			t := c.LastTransitionTime
			if t.IsZero() {
				t = metav1.Now()
			}
			condMaps = append(condMaps, map[string]any{
				"type":               c.Type,
				"status":             string(c.Status),
				"observedGeneration": c.ObservedGeneration,
				"lastTransitionTime": t.Format(time.RFC3339),
				"reason":             c.Reason,
				"message":            c.Message,
			})
		}
		statusMap["conditions"] = condMaps

		if result.Coordinates != nil {
			statusMap["access"] = map[string]any{
				"endpoint": fmt.Sprintf("%s://%s:%d", result.Coordinates.Scheme, result.Coordinates.Host, result.Coordinates.Port),
			}
		} else {
			delete(statusMap, "access")
		}

		obj.Object["status"] = statusMap
		_, err = r.dynamicClient.Resource(ComponentGVR).Namespace(ns).UpdateStatus(ctx, obj, metav1.UpdateOptions{})
		return err
	})
}
