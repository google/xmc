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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

var componentTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("proto.Marshal(%T) error = %v", m, err)
	}
	return b
}

func TestMarshalComponentPayloads(t *testing.T) {
	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.2.0",
	}
	status := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 3,
	}
	labels := map[string]string{"env": "prod"}

	cfgBytes, accessBytes, err := MarshalComponentPayloads("app", spec, labels, status)
	if err != nil {
		t.Fatalf("MarshalComponentPayloads() failed: %v", err)
	}

	var gotComp componentspb.Component
	if err := proto.Unmarshal(cfgBytes, &gotComp); err != nil {
		t.Fatalf("proto.Unmarshal(cfgBytes) failed: %v", err)
	}
	if gotComp.GetName() != "components/app" {
		t.Errorf("gotComp.GetName() = %q, want %q", gotComp.GetName(), "components/app")
	}
	if diff := cmp.Diff(spec, gotComp.GetSpec(), protocmp.Transform()); diff != "" {
		t.Errorf("Spec mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(labels, gotComp.GetLabels()); diff != "" {
		t.Errorf("Labels mismatch (-want +got):\n%s", diff)
	}

	var gotStatus componentspb.ComponentAccessStatus
	if err := proto.Unmarshal(accessBytes, &gotStatus); err != nil {
		t.Fatalf("proto.Unmarshal(accessBytes) failed: %v", err)
	}
	if diff := cmp.Diff(status, &gotStatus, protocmp.Transform()); diff != "" {
		t.Errorf("Status mismatch (-want +got):\n%s", diff)
	}

	nilAccessBytes, err := MarshalAccessStatus(nil)
	if err != nil || nilAccessBytes != nil {
		t.Errorf("MarshalAccessStatus(nil) = (%v, %v), want (nil, nil)", nilAccessBytes, err)
	}
}

func TestDetermineComponentClass(t *testing.T) {
	tests := []struct {
		name string
		spec *componentspb.ComponentSpec
		want string
	}{
		{name: "nil spec", spec: nil, want: ""},
		{name: "unspecified type", spec: &componentspb.ComponentSpec{}, want: ""},
		{
			name: "1p component uses enum string",
			spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
			want: "COMPONENT_TYPE_CUSTOM_1P_COMPONENT",
		},
		{
			name: "3p wrapper with crd kind uses crd kind",
			spec: &componentspb.ComponentSpec{
				Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				WrapperRef: &componentspb.WrapperRef{CrdKind: "RayCluster"},
			},
			want: "RayCluster",
		},
		{
			name: "3p wrapper without crd kind falls back to enum string",
			spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER},
			want: "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetermineComponentClass(tt.spec); got != tt.want {
				t.Errorf("DetermineComponentClass() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeComponentPayloads(t *testing.T) {
	if err := DecodeComponentPayloads(nil, nil, nil); err != nil {
		t.Fatalf("DecodeComponentPayloads(nil) error = %v, want nil", err)
	}
	spec := &componentspb.ComponentSpec{Version: "v1.0.0"}
	status := &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED}
	tests := []struct {
		name    string
		config  []byte
		access  []byte
		want    *store.ComponentRecord
		wantErr bool
	}{
		{
			name:   "component with labels",
			config: mustMarshal(t, &componentspb.Component{Spec: spec, Labels: map[string]string{"env": "prod"}}),
			access: mustMarshal(t, status),
			want:   &store.ComponentRecord{Spec: spec, Labels: map[string]string{"env": "prod"}, Status: status},
		},
		{
			name:   "component without status",
			config: mustMarshal(t, &componentspb.Component{Spec: spec}),
			want:   &store.ComponentRecord{Spec: spec},
		},
		{
			name:    "invalid access payload",
			config:  mustMarshal(t, &componentspb.Component{Spec: spec}),
			access:  []byte{0xff},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := &store.ComponentRecord{}
			err := DecodeComponentPayloads(tt.config, tt.access, got)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("DecodeComponentPayloads() error = %v, wantErr %t", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("DecodeComponentPayloads() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFinalizeComponentRecord(t *testing.T) {
	FinalizeComponentRecord(nil)

	deleteTime := componentTime
	tests := []struct {
		name  string
		state componentspb.ComponentState
		deps  []string
		want  *store.ComponentRecord
	}{
		{
			name:  "active record keeps its dependencies",
			state: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			deps:  []string{"db"},
			want:  &store.ComponentRecord{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED, Dependencies: []string{"db"}},
		},
		{
			name:  "empty dependencies become nil",
			state: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			deps:  []string{},
			want:  &store.ComponentRecord{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED},
		},
		{
			name:  "deleted record is deleted at its update time",
			state: componentspb.ComponentState_COMPONENT_STATE_DELETED,
			want:  &store.ComponentRecord{State: componentspb.ComponentState_COMPONENT_STATE_DELETED, DeleteTime: &deleteTime},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := &store.ComponentRecord{ComponentID: 1, Name: "app", State: tt.state, Dependencies: tt.deps, UpdateTime: componentTime}
			FinalizeComponentRecord(got)
			tt.want.ComponentID, tt.want.Name, tt.want.UpdateTime = 1, "app", componentTime
			tt.want.Etag = store.GenerateEtag(tt.want)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("FinalizeComponentRecord() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDecodeComponentSnapshot(t *testing.T) {
	coords := &componentspb.ResolvedComponentCoordinates{ServiceName: "svc", Port: 80}
	status := &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_DISABLED, ObservedGeneration: 2, Coordinates: coords}
	spec := &componentspb.ComponentSpec{Version: "v0.9.0"}
	observedGen := int64(2)
	tests := []struct {
		name       string
		access     []byte
		want       *store.ComponentRecord
		wantCoords *componentspb.ResolvedComponentCoordinates
	}{
		{
			name:       "state and coordinates come from the snapshot status",
			access:     mustMarshal(t, status),
			wantCoords: coords,
			want:       &store.ComponentRecord{Status: status, State: status.State, ObservedGeneration: &observedGen},
		},
		{name: "snapshot without status", want: &store.ComponentRecord{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := ComponentSnapshot{ComponentID: 7, LockedGeneration: 3, ConfigProto: mustMarshal(t, &componentspb.Component{Spec: spec}), AccessPayload: tt.access}
			got, gotCoords, err := DecodeComponentSnapshot(snap, "app", componentTime, componentTime)
			if err != nil {
				t.Fatalf("DecodeComponentSnapshot() error = %v", err)
			}
			want := tt.want
			want.ComponentID, want.Name, want.Generation, want.Spec, want.CreateTime, want.UpdateTime = 7, "app", 3, spec, componentTime, componentTime
			want.Etag = store.GenerateEtag(want)
			if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
				t.Errorf("DecodeComponentSnapshot() record mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantCoords, gotCoords, protocmp.Transform()); diff != "" {
				t.Errorf("DecodeComponentSnapshot() coordinates mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
