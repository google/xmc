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
package statusdetector

import (
	"context"

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

// UnimplementedStatusDetector is a truthful baseline for environments where dynamic
// status detection has not been instantiated. It explicitly reports that detection is pending.
type UnimplementedStatusDetector struct{}

var _ StatusDetector = (*UnimplementedStatusDetector)(nil)

// NewUnimplementedStatusDetector constructs an UnimplementedStatusDetector.
func NewUnimplementedStatusDetector() *UnimplementedStatusDetector {
	return &UnimplementedStatusDetector{}
}

// DetectStatus truthfully reports that dynamic status detection is pending implementation.
func (u *UnimplementedStatusDetector) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	return &ResourceStatus{
		Found:   false,
		Ready:   false,
		Message: "3P dynamic status detection engine is pending implementation (Layer 4A-2)",
	}, nil
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
