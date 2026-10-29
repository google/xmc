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

package data

import (
	"fmt"
	"maps"

	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/naming"
)

// MarshalComponentPayloads serializes a Component config proto (name, spec, labels) and
// optional ComponentAccessStatus payload for database storage.
func MarshalComponentPayloads(canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, access *componentspb.ComponentAccessStatus) ([]byte, []byte, error) {
	configBytes, err := proto.Marshal(&componentspb.Component{
		Name:   naming.FullComponentName(canonicalName),
		Spec:   spec,
		Labels: maps.Clone(labels),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal component proto: %w", err)
	}
	accessBytes, err := MarshalAccessStatus(access)
	if err != nil {
		return nil, nil, err
	}
	return configBytes, accessBytes, nil
}

// MarshalAccessStatus serializes access to protobuf bytes, returning nil when access is nil.
func MarshalAccessStatus(access *componentspb.ComponentAccessStatus) ([]byte, error) {
	if access == nil {
		return nil, nil
	}
	accessBytes, err := proto.Marshal(access)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal access status proto: %w", err)
	}
	return accessBytes, nil
}

// DetermineComponentClass derives the stored ComponentClass string from spec.
func DetermineComponentClass(spec *componentspb.ComponentSpec) string {
	if spec == nil {
		return ""
	}
	if spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER && spec.GetWrapperRef().GetCrdKind() != "" {
		return spec.GetWrapperRef().GetCrdKind()
	}
	if spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_UNSPECIFIED {
		return spec.GetType().String()
	}
	return ""
}
