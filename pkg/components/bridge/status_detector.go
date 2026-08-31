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

// Package bridge implements dynamic 3P status detection for XMC components.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

const (
	defaultReadyValue = "True"
)

// WrapperRef configures reference metadata for third-party CRDs.
type WrapperRef struct {
	// Group is the API group of the target CRD (e.g. "ray.io", "vllm.ai").
	Group string `json:"group,omitempty"`
	// Version is the API version of the target CRD (e.g. "v1", "v1alpha1").
	Version string `json:"version,omitempty"`
	// Kind is the Kind of the target CRD (e.g. "RayCluster", "VLLMService").
	Kind string `json:"kind,omitempty"`
	// Name is the metadata.name of the target custom resource.
	Name string `json:"name,omitempty"`
	// Namespace is the metadata.namespace of the target custom resource.
	Namespace string `json:"namespace,omitempty"`
	// ObservedGenerationPath is an optional JSONPath expression pointing to status.observedGeneration.
	ObservedGenerationPath string `json:"observedGenerationPath,omitempty"`
	// ReadyConditionPath is an optional JSONPath expression evaluating readiness status.
	ReadyConditionPath string `json:"readyConditionPath,omitempty"`
	// ExpectedReadyValue is the expected value of the Ready condition (defaults to "True").
	ExpectedReadyValue string `json:"expectedReadyValue,omitempty"`
	// EndpointJSONPath is an optional JSONPath expression pointing to the access endpoint.
	EndpointJSONPath string `json:"endpointJsonPath,omitempty"`
	// ConnectionSecretJSONPath is an optional JSONPath expression pointing to connection secret coordinates.
	ConnectionSecretJSONPath string `json:"connectionSecretJsonPath,omitempty"`
}

// ResourceStatus holds the runtime status extracted from a dynamic 3P resource.
type ResourceStatus struct {
	// Found indicates whether the target custom resource exists in the cluster.
	Found bool `json:"found"`
	// Ready indicates whether the target resource is healthy and operational.
	Ready bool `json:"ready"`
	// ObservedGeneration is the custom resource generation evaluated.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Endpoint is the resolved access URL or host:port coordinate.
	Endpoint string `json:"endpoint,omitempty"`
	// ConnectionSecret is the name of the Kubernetes Secret containing access credentials.
	ConnectionSecret string `json:"connectionSecret,omitempty"`
	// Message provides human-readable context regarding the current readiness state.
	Message string `json:"message,omitempty"`
	// RawStatus captures the raw .status map from the unstructured resource.
	RawStatus map[string]any `json:"rawStatus,omitempty"`
}

// StatusDetector inspects third-party Kubernetes CRD status dynamically.
type StatusDetector interface {
	// DetectStatus queries the target 3P resource and extracts its runtime health, endpoint, and secret coordinates.
	DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error)
}

// K8sStatusDetector implements StatusDetector using client-go dynamic client and RESTMapper.
type K8sStatusDetector struct {
	dynClient  dynamic.Interface
	restMapper meta.RESTMapper
}

var _ StatusDetector = (*K8sStatusDetector)(nil)

// NewK8sStatusDetector creates a new K8sStatusDetector instance.
func NewK8sStatusDetector(dynClient dynamic.Interface, restMapper meta.RESTMapper) *K8sStatusDetector {
	return &K8sStatusDetector{
		dynClient:  dynClient,
		restMapper: restMapper,
	}
}

// DetectStatus queries the target 3P resource via the dynamic client and evaluates its status.
// It gracefully handles uninstalled CRDs (meta.IsNoMatchError) and missing resources (apierrors.IsNotFound).
func (d *K8sStatusDetector) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	if d.dynClient == nil {
		return nil, errors.New("dynamic client is uninitialized")
	}
	if ref.Name == "" || ref.Kind == "" {
		return nil, errors.New("wrapperRef name and kind are required")
	}
	gvr, isNamespaced, err := ResolveGVR(ref, d.restMapper)
	if err != nil {
		if meta.IsNoMatchError(err) {
			return &ResourceStatus{Found: false, Ready: false, Message: fmt.Sprintf("CRD not installed: %v", err)}, nil
		}
		return nil, fmt.Errorf("failed to resolve GVR: %w", err)
	}
	nri := d.dynClient.Resource(gvr)
	var ri dynamic.ResourceInterface = nri
	if isNamespaced && ref.Namespace != "" {
		ri = nri.Namespace(ref.Namespace)
	}
	obj, err := ri.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return &ResourceStatus{Found: false, Ready: false, Message: fmt.Sprintf("resource %s/%s not found", ref.Namespace, ref.Name)}, nil
		}
		if meta.IsNoMatchError(err) {
			return &ResourceStatus{Found: false, Ready: false, Message: fmt.Sprintf("CRD not installed: %v", err)}, nil
		}
		return nil, fmt.Errorf("failed to fetch dynamic resource: %w", err)
	}
	return ExtractResourceStatus(obj, ref)
}

// ResolveGVR resolves the GroupVersionResource for a WrapperRef using RESTMapper or fallback pluralization.
func ResolveGVR(ref WrapperRef, restMapper meta.RESTMapper) (schema.GroupVersionResource, bool, error) {
	gvk := schema.GroupVersionKind{Group: ref.Group, Version: ref.Version, Kind: ref.Kind}
	if restMapper != nil {
		var versions []string
		if gvk.Version != "" {
			versions = []string{gvk.Version}
		}
		mapping, err := restMapper.RESTMapping(gvk.GroupKind(), versions...)
		if err != nil {
			return schema.GroupVersionResource{}, false, err
		}
		isNamespaced := mapping.Scope.Name() == meta.RESTScopeNameNamespace
		return mapping.Resource, isNamespaced, nil
	}
	gvr, _ := meta.UnsafeGuessKindToResource(gvk)
	return gvr, true, nil
}

// ExtractResourceStatus inspects the unstructured Kubernetes object according to standard status conditions,
// extracting observedGeneration, readiness status, endpoints, and connection secrets.
func ExtractResourceStatus(obj *unstructured.Unstructured, ref WrapperRef) (*ResourceStatus, error) {
	if obj == nil {
		return &ResourceStatus{Found: false, Ready: false, Message: "object is nil"}, nil
	}
	rawStatus, _, _ := unstructured.NestedMap(obj.Object, "status")
	obsGen := obj.GetGeneration()
	if og, found, _ := unstructured.NestedInt64(obj.Object, "status", "observedGeneration"); found {
		obsGen = og
	}
	expectedReady := defaultReadyValue
	if ref.ExpectedReadyValue != "" {
		expectedReady = ref.ExpectedReadyValue
	}
	ready, readyMsg, err := evaluateDefaultReadiness(obj, expectedReady)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate readiness: %w", err)
	}
	msg := readyMsg
	if msg == "" {
		if ready {
			msg = "Resource is ready"
		} else {
			msg = "Resource is not ready"
		}
	}
	return &ResourceStatus{
		Found:              true,
		Ready:              ready,
		ObservedGeneration: obsGen,
		Message:            msg,
		RawStatus:          rawStatus,
	}, nil
}

// getConditionStatusAndMessage extracts the status and message strings for a given condition type from .status.conditions.
func getConditionStatusAndMessage(obj *unstructured.Unstructured, condType string) (string, string, bool) {
	if obj == nil {
		return "", "", false
	}
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return "", "", false
	}
	for _, c := range conditions {
		condMap, ok := c.(map[string]any)
		if !ok {
			continue
		}
		t, _ := condMap["type"].(string)
		if strings.EqualFold(t, condType) {
			var statusStr string
			switch s := condMap["status"].(type) {
			case string:
				statusStr = s
			case bool:
				if s {
					statusStr = "True"
				} else {
					statusStr = "False"
				}
			case fmt.Stringer:
				statusStr = s.String()
			default:
				if s != nil {
					statusStr = fmt.Sprintf("%v", s)
				}
			}
			message, _ := condMap["message"].(string)
			return statusStr, message, true
		}
	}
	return "", "", false
}

// evaluateDefaultReadiness evaluates readiness when no explicit JSONPath query is specified,
// checking status.conditions[type=Ready] or status.phase (Running/Ready/Active).
func evaluateDefaultReadiness(obj *unstructured.Unstructured, expected string) (bool, string, error) {
	status, message, found := getConditionStatusAndMessage(obj, "Ready")
	if found {
		if strings.EqualFold(strings.TrimSpace(status), strings.TrimSpace(expected)) {
			return true, "Readiness condition satisfied", nil
		}
		if message == "" {
			message = fmt.Sprintf("Expected ready value %q, got %q", expected, status)
		}
		return false, message, nil
	}
	if phase, found, _ := unstructured.NestedString(obj.Object, "status", "phase"); found &&
		(strings.EqualFold(phase, "Running") || strings.EqualFold(phase, "Ready") || strings.EqualFold(phase, "Active")) {
		return true, fmt.Sprintf("Phase is %s", phase), nil
	}
	return false, "Readiness status not reported", nil
}

// ToBridgeWrapperRef converts a protobuf WrapperRef to the internal bridge WrapperRef representation.
func ToBridgeWrapperRef(pbRef *componentspb.WrapperRef, defaultNamespace string) WrapperRef {
	if pbRef == nil {
		return WrapperRef{}
	}
	ns := pbRef.GetTargetNamespace()
	if ns == "" {
		ns = defaultNamespace
	}
	return WrapperRef{
		Group:     pbRef.GetCrdGroup(),
		Version:   pbRef.GetCrdVersion(),
		Kind:      pbRef.GetCrdKind(),
		Name:      pbRef.GetTargetName(),
		Namespace: ns,
	}
}

// FakeStatusDetector is an in-memory test double for StatusDetector.
type FakeStatusDetector struct {
	mu        sync.Mutex
	Statuses  map[string]*ResourceStatus
	DetectErr error
}

var _ StatusDetector = (*FakeStatusDetector)(nil)

// NewFakeStatusDetector creates a new FakeStatusDetector.
func NewFakeStatusDetector() *FakeStatusDetector {
	return &FakeStatusDetector{
		Statuses: make(map[string]*ResourceStatus),
	}
}

// SetStatus records the status to return for a given component key.
func (f *FakeStatusDetector) SetStatus(key string, status *ResourceStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Statuses == nil {
		f.Statuses = make(map[string]*ResourceStatus)
	}
	f.Statuses[key] = status
}

// SetError configures a synthetic error to return from DetectStatus.
func (f *FakeStatusDetector) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DetectErr = err
}

// DetectStatus returns the preconfigured status or error for the requested resource.
func (f *FakeStatusDetector) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DetectErr != nil {
		return nil, f.DetectErr
	}
	key := ref.Name
	if ref.Namespace != "" {
		key = ref.Namespace + "/" + ref.Name
	}
	if st, ok := f.Statuses[key]; ok {
		return st, nil
	}
	if st, ok := f.Statuses[ref.Name]; ok {
		return st, nil
	}
	return &ResourceStatus{Found: false, Ready: false, Message: "resource not found"}, nil
}
