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

	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
)

// MarshalComponentPayloads serializes rec's Component config proto (name, spec, labels) and
// optional ComponentAccessStatus payload for database storage.
func MarshalComponentPayloads(rec *store.ComponentRecord) ([]byte, []byte, error) {
	if rec == nil {
		return nil, nil, fmt.Errorf("component record must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	configBytes, err := proto.Marshal(&componentspb.Component{Name: naming.FullComponentName(rec.Name), Spec: rec.Spec, Labels: store.CopyMap(rec.Labels)})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal component proto: %w", err)
	}
	accessBytes, err := MarshalAccessStatus(rec.Status)
	return configBytes, accessBytes, err
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

// UnmarshalConfigProto decodes a serialized Component or legacy ComponentSpec proto into rec.Spec and rec.Labels.
func UnmarshalConfigProto(configBytes []byte, rec *store.ComponentRecord) error {
	if len(configBytes) == 0 {
		return nil
	}
	var comp componentspb.Component
	if err := proto.Unmarshal(configBytes, &comp); err == nil && (comp.Spec != nil || len(comp.Labels) > 0) {
		rec.Spec, rec.Labels = comp.Spec, store.CopyMap(comp.Labels)
		return nil
	}
	rec.Spec = &componentspb.ComponentSpec{}
	return proto.Unmarshal(configBytes, rec.Spec)
}

// UnmarshalAccessStatus decodes a serialized ComponentAccessStatus proto into rec.Status.
func UnmarshalAccessStatus(accessBytes []byte, rec *store.ComponentRecord) error {
	rec.Status = nil
	if len(accessBytes) == 0 {
		return nil
	}
	rec.Status = &componentspb.ComponentAccessStatus{}
	return proto.Unmarshal(accessBytes, rec.Status)
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

// FinalizeComponentRecord populates DeleteTime (from UpdateTime when DELETED) and derives Etag.
func FinalizeComponentRecord(rec *store.ComponentRecord) {
	if rec.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		t := rec.UpdateTime
		rec.DeleteTime = &t
	}
	rec.Etag = store.GenerateEtag(rec)
}
