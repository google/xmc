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
	"time"

	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
)

// ComponentSnapshot holds the stored columns of one ExperimentComponentSnapshots row.
type ComponentSnapshot struct {
	ComponentID, LockedGeneration int64
	ConfigProto, AccessPayload    []byte
}

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

// DecodeComponentPayloads decodes the stored ConfigProto of a component into rec.Spec and
// rec.Labels, and its AccessPayload into rec.Status.
func DecodeComponentPayloads(configProto, accessPayload []byte, rec *store.ComponentRecord) error {
	if rec == nil {
		return nil
	}
	if err := unmarshalComponentConfig(configProto, rec); err != nil {
		return fmt.Errorf("failed to unmarshal config proto: %w", err)
	}
	if err := unmarshalComponentAccessStatus(accessPayload, rec); err != nil {
		return fmt.Errorf("failed to unmarshal access status: %w", err)
	}
	return nil
}

// FinalizeComponentRecord completes a record decoded from a Components row: a DELETED record gets
// its UpdateTime as DeleteTime, empty Dependencies become nil, and Etag is derived.
func FinalizeComponentRecord(rec *store.ComponentRecord) {
	if rec == nil {
		return
	}
	if rec.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		t := rec.UpdateTime
		rec.DeleteTime = &t
	}
	if len(rec.Dependencies) == 0 {
		rec.Dependencies = nil
	}
	rec.Etag = store.GenerateEtag(rec)
}

// DecodeComponentSnapshot builds the historical record of canonicalName stored in snap, stamped
// with createTime and updateTime because snapshots do not store their own. The record is built from
// scratch, with its state and observed generation taken from the snapshot status, so no active
// component state leaks into the historical view.
func DecodeComponentSnapshot(snap ComponentSnapshot, canonicalName string, createTime, updateTime time.Time) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	rec := &store.ComponentRecord{ComponentID: snap.ComponentID, Name: canonicalName, Generation: snap.LockedGeneration, CreateTime: createTime, UpdateTime: updateTime}
	if err := DecodeComponentPayloads(snap.ConfigProto, snap.AccessPayload, rec); err != nil {
		return nil, nil, fmt.Errorf("failed to decode historical snapshot: %w", err)
	}
	if rec.Status != nil {
		rec.State = rec.Status.State
		obsGen := rec.Status.ObservedGeneration
		rec.ObservedGeneration = &obsGen
	}
	rec.Etag = store.GenerateEtag(rec)
	var coords *componentspb.ResolvedComponentCoordinates
	if rec.Status != nil && rec.Status.Coordinates != nil {
		coords = store.CloneCoordinates(rec.Status.Coordinates)
	}
	return rec, coords, nil
}

// unmarshalComponentConfig decodes a serialized Component or ComponentSpec proto into rec.Spec and rec.Labels.
func unmarshalComponentConfig(configBytes []byte, rec *store.ComponentRecord) error {
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

// unmarshalComponentAccessStatus decodes a serialized ComponentAccessStatus proto into rec.Status.
func unmarshalComponentAccessStatus(accessBytes []byte, rec *store.ComponentRecord) error {
	if len(accessBytes) == 0 {
		rec.Status = nil
		return nil
	}
	rec.Status = &componentspb.ComponentAccessStatus{}
	return proto.Unmarshal(accessBytes, rec.Status)
}
