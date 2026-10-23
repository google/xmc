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

// Unit tests for the payload decoding in reads.go. The SQL reads are covered by the Spanner
// emulator suite in pkg/components/store/spanner.
package component

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("proto.Marshal(%v) failed: %v", m, err)
	}
	return b
}

func TestUnmarshalConfigProto(t *testing.T) {
	spec := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.0.0"}
	tests := []struct {
		name    string
		payload []byte
		want    *store.ComponentRecord
		wantErr bool
	}{
		{
			name:    "empty payload keeps the record",
			payload: nil,
			want:    &store.ComponentRecord{},
		},
		{
			name:    "component envelope sets spec and labels",
			payload: mustMarshal(t, &componentspb.Component{Name: "components/a", Spec: spec, Labels: map[string]string{"env": "prod"}}),
			want:    &store.ComponentRecord{Spec: spec, Labels: map[string]string{"env": "prod"}},
		},
		{
			name:    "component envelope without labels sets only the spec",
			payload: mustMarshal(t, &componentspb.Component{Name: "components/a", Spec: spec}),
			want:    &store.ComponentRecord{Spec: spec},
		},
		{
			name:    "malformed payload fails",
			payload: []byte{0xff},
			want:    &store.ComponentRecord{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &store.ComponentRecord{}
			err := unmarshalConfigProto(tt.payload, rec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unmarshalConfigProto() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, rec, protocmp.Transform()); diff != "" {
				t.Errorf("unmarshalConfigProto() record mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUnmarshalAccessStatus(t *testing.T) {
	status := &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED, ObservedGeneration: 3}
	tests := []struct {
		name    string
		payload []byte
		want    *componentspb.ComponentAccessStatus
		wantErr bool
	}{
		{name: "empty payload clears the status", payload: nil, want: nil},
		{name: "status payload sets the status", payload: mustMarshal(t, status), want: status},
		{name: "malformed payload fails", payload: []byte{0xff}, want: &componentspb.ComponentAccessStatus{HealthMessage: "previous"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &store.ComponentRecord{Status: &componentspb.ComponentAccessStatus{HealthMessage: "previous"}}
			err := unmarshalAccessStatus(tt.payload, rec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unmarshalAccessStatus() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, rec.Status, protocmp.Transform()); diff != "" {
				t.Errorf("unmarshalAccessStatus() status mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
