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

package store

import (
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// ApplySpecUpdateMask merges fields from update into base according to field mask paths.
//
// Supported top-level paths (both snake_case and camelCase):
//   - "type"
//   - "version"
//   - "deletion_policy", "deletionPolicy"
//   - "wrapper_ref", "wrapperRef"
//   - "lifecycle_control", "lifecycleControl"
//   - "manifests"
//   - "description"
//   - "generation"
//   - "parameters"
//   - "dependencies"
//   - "stability"
//   - "acknowledge_experimental", "acknowledgeExperimental"
//
// Unmapped, unknown, or sub-nested paths are ignored without mutating other fields.
// If base is nil, an empty ComponentSpec is used as the base (useful for creation with update_mask).
// If update is nil, a cloned base is returned.
func ApplySpecUpdateMask(base, update *componentspb.ComponentSpec, paths []string) *componentspb.ComponentSpec {
	if base == nil && update == nil {
		return nil
	}
	var result *componentspb.ComponentSpec
	if base != nil {
		result = proto.Clone(base).(*componentspb.ComponentSpec)
	} else {
		result = &componentspb.ComponentSpec{}
	}
	if update == nil || len(paths) == 0 {
		return result
	}

	for _, p := range paths {
		applyMaskPath(result, update, p)
	}
	return result
}

func applyMaskPath(result, update *componentspb.ComponentSpec, path string) {
	if applyScalarOrEnumField(result, update, path) {
		return
	}
	applyComplexField(result, update, path)
}

func applyScalarOrEnumField(result, update *componentspb.ComponentSpec, path string) bool {
	switch path {
	case "type":
		result.Type = update.GetType()
		return true
	case "version":
		result.Version = update.GetVersion()
		return true
	case "deletion_policy", "deletionPolicy":
		result.DeletionPolicy = update.GetDeletionPolicy()
		return true
	case "description":
		result.Description = update.GetDescription()
		return true
	case "generation":
		result.Generation = update.GetGeneration()
		return true
	case "stability":
		result.Stability = update.GetStability()
		return true
	case "acknowledge_experimental", "acknowledgeExperimental":
		result.AcknowledgeExperimental = update.GetAcknowledgeExperimental()
		return true
	default:
		return false
	}
}

func applyComplexField(result, update *componentspb.ComponentSpec, path string) {
	switch path {
	case "wrapper_ref", "wrapperRef":
		if update.GetWrapperRef() != nil {
			result.WrapperRef = proto.Clone(update.GetWrapperRef()).(*componentspb.WrapperRef)
		} else {
			result.WrapperRef = nil
		}
	case "lifecycle_control", "lifecycleControl":
		if update.GetLifecycleControl() != nil {
			result.LifecycleControl = proto.Clone(update.GetLifecycleControl()).(*componentspb.LifecycleControl)
		} else {
			result.LifecycleControl = nil
		}
	case "manifests":
		if update.GetManifests() != nil {
			result.Manifests = append([]string(nil), update.GetManifests()...)
		} else {
			result.Manifests = nil
		}
	case "dependencies":
		result.Dependencies = append([]string(nil), update.GetDependencies()...)
	case "parameters":
		if update.GetParameters() != nil {
			params := make(map[string]string, len(update.GetParameters()))
			for k, v := range update.GetParameters() {
				params[k] = v
			}
			result.Parameters = params
		} else {
			result.Parameters = nil
		}
	}
}
