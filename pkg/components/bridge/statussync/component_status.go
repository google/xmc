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

package statussync

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"xmanager_cloud/pkg/components/bridge"
)

// ComponentGVR is the exported GroupVersionResource of the Component custom resource.
var ComponentGVR = schema.GroupVersionResource{
	Group:    "xmc.dev",
	Version:  "v1alpha1",
	Resource: "components",
}

// ComponentStatus defines the structured representation of .status in the Component CRD.
type ComponentStatus struct {
	ObservedGeneration int64                `json:"observedGeneration"`
	State              string               `json:"state"`
	Access             *ComponentAccess     `json:"access,omitempty"`
	Conditions         []ComponentCondition `json:"conditions,omitempty"`
}

// ComponentAccess defines the access configuration in .status.access.
type ComponentAccess struct {
	EnvVars map[string]string `json:"envVars,omitempty"`
}

// ComponentCondition defines a condition entry matching standard RFC3339 timestamps.
type ComponentCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	ObservedGeneration int64  `json:"observedGeneration"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	LastTransitionTime string `json:"lastTransitionTime"`
}

// BuildStatus converts a ReconcileResult into a typed ComponentStatus matching the Component CRD status schema,
// including observedGeneration, state string, access envVars (if present), and standard RFC3339 condition list.
//
// A nil res yields the zero ComponentStatus.
func BuildStatus(res *bridge.ReconcileResult) ComponentStatus {
	if res == nil {
		return ComponentStatus{}
	}
	status := ComponentStatus{
		ObservedGeneration: res.ObservedGeneration,
		State:              res.State.String(),
	}
	if res.Access != nil && len(res.Access.EnvVars) > 0 {
		status.Access = &ComponentAccess{EnvVars: res.Access.EnvVars}
	}
	if len(res.Conditions) > 0 {
		status.Conditions = make([]ComponentCondition, 0, len(res.Conditions))
		for _, c := range res.Conditions {
			status.Conditions = append(status.Conditions, ComponentCondition{
				Type:               c.Type,
				Status:             c.Status,
				ObservedGeneration: c.ObservedGeneration,
				Reason:             c.Reason,
				Message:            c.Message,
				LastTransitionTime: c.LastTransitionTime.Format(time.RFC3339),
			})
		}
	}
	return status
}

// BuildStatusMap converts a ReconcileResult into an unstructured map matching the Component CRD status schema.
//
// A nil res yields a nil map rather than a map of zero values. Serializing the zero ComponentStatus
// would produce observedGeneration 0 and an empty state, which Server-Side Apply would then write
// over a live status. Returning nil makes the caller apply nothing instead.
func BuildStatusMap(res *bridge.ReconcileResult) map[string]any {
	if res == nil {
		return nil
	}
	status := BuildStatus(res)
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&status)
	if err != nil {
		fallback := ComponentStatus{
			ObservedGeneration: res.ObservedGeneration,
			State:              res.State.String(),
		}
		fallbackMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&fallback)
		return fallbackMap
	}
	return m
}
