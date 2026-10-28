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
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/pkg/components/store"
)

func TestComponentPayloadRoundTrip(t *testing.T) {
	if _, _, err := MarshalComponentPayloads(nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Fatalf("MarshalComponentPayloads(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if err := UnmarshalConfigProto(nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Fatalf("UnmarshalConfigProto(nil, nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if err := UnmarshalAccessStatus(nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Fatalf("UnmarshalAccessStatus(nil, nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.2.0",
	}
	status := &componentspb.ComponentAccessStatus{
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ObservedGeneration: 3,
	}
	src := &store.ComponentRecord{
		Name:   "app",
		Spec:   spec,
		Status: status,
		Labels: map[string]string{"env": "prod"},
	}

	cfgBytes, accessBytes, err := MarshalComponentPayloads(src)
	if err != nil {
		t.Fatalf("MarshalComponentPayloads() failed: %v", err)
	}

	dst := &store.ComponentRecord{Status: &componentspb.ComponentAccessStatus{HealthMessage: "stale"}}
	if err := UnmarshalConfigProto(cfgBytes, dst); err != nil {
		t.Fatalf("UnmarshalConfigProto() failed: %v", err)
	}
	if err := UnmarshalAccessStatus(accessBytes, dst); err != nil {
		t.Fatalf("UnmarshalAccessStatus() failed: %v", err)
	}
	if diff := cmp.Diff(spec, dst.Spec, protocmp.Transform()); diff != "" {
		t.Errorf("Spec mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(status, dst.Status, protocmp.Transform()); diff != "" {
		t.Errorf("Status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(src.Labels, dst.Labels); diff != "" {
		t.Errorf("Labels mismatch (-want +got):\n%s", diff)
	}

	// Empty access bytes must clear any pre-existing Status field on the target record.
	if err := UnmarshalAccessStatus(nil, dst); err != nil {
		t.Fatalf("UnmarshalAccessStatus(nil) failed: %v", err)
	}
	if dst.Status != nil {
		t.Errorf("UnmarshalAccessStatus(nil) left Status = %v, want nil", dst.Status)
	}
}

func TestUnmarshalConfigProto_LegacyAndInvalid(t *testing.T) {
	legacy := &componentspb.ComponentSpec{
		Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
	}
	legacyBytes, err := proto.Marshal(legacy)
	if err != nil {
		t.Fatalf("proto.Marshal(legacy) failed: %v", err)
	}
	got := &store.ComponentRecord{}
	if err := UnmarshalConfigProto(legacyBytes, got); err != nil {
		t.Fatalf("UnmarshalConfigProto(legacy) failed: %v", err)
	}
	if diff := cmp.Diff(legacy, got.Spec, protocmp.Transform()); diff != "" {
		t.Errorf("legacy Spec mismatch (-want +got):\n%s", diff)
	}
	if err := UnmarshalConfigProto([]byte{0xff}, &store.ComponentRecord{}); err == nil {
		t.Errorf("UnmarshalConfigProto(corrupt) succeeded, want error")
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

func TestFinalizeComponentRecord(t *testing.T) {
	FinalizeComponentRecord(nil)

	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	active := &store.ComponentRecord{ComponentID: 1, Name: "app", State: componentspb.ComponentState_COMPONENT_STATE_ENABLED, UpdateTime: ts}
	FinalizeComponentRecord(active)
	if active.DeleteTime != nil || active.Etag == "" {
		t.Errorf("FinalizeComponentRecord(active) = (DeleteTime=%v, Etag=%q), want nil DeleteTime and non-empty Etag", active.DeleteTime, active.Etag)
	}

	deleted := &store.ComponentRecord{ComponentID: 2, Name: "old", State: componentspb.ComponentState_COMPONENT_STATE_DELETED, UpdateTime: ts}
	FinalizeComponentRecord(deleted)
	if deleted.DeleteTime == nil || !deleted.DeleteTime.Equal(ts) || deleted.Etag == "" {
		t.Errorf("FinalizeComponentRecord(deleted) = (DeleteTime=%v, Etag=%q), want %v and non-empty Etag", deleted.DeleteTime, deleted.Etag, ts)
	}
}
