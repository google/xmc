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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

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
