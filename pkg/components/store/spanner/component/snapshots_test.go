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

// Unit tests for the snapshot payload decoding in snapshots.go. The SQL reads are covered by the
// Spanner emulator suite in pkg/components/store/spanner.
package component

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

func TestSnapshotRowToRecord(t *testing.T) {
	spec := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.0.0", Generation: 2}
	status := &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED, ObservedGeneration: 2}
	tests := []struct {
		name    string
		row     *snapshotRowData
		want    *store.ComponentRecord
		wantErr bool
	}{
		{
			name: "decodes the payloads",
			row: &snapshotRowData{
				componentID:   7,
				lockedGen:     2,
				configProto:   mustMarshal(t, &componentspb.Component{Name: "components/a", Spec: spec, Labels: map[string]string{"env": "prod"}}),
				accessPayload: mustMarshal(t, status),
			},
			want: &store.ComponentRecord{ComponentID: 7, Generation: 2, Spec: spec, Labels: map[string]string{"env": "prod"}, Status: status},
		},
		{
			name: "empty payloads leave spec and status unset",
			row:  &snapshotRowData{componentID: 7, lockedGen: 1},
			want: &store.ComponentRecord{ComponentID: 7, Generation: 1},
		},
		{name: "malformed config payload fails", row: &snapshotRowData{configProto: []byte{0xff}}, wantErr: true},
		{name: "malformed access payload fails", row: &snapshotRowData{accessPayload: []byte{0xff}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := snapshotRowToRecord(tt.row)
			if (err != nil) != tt.wantErr {
				t.Fatalf("snapshotRowToRecord() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("snapshotRowToRecord() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
