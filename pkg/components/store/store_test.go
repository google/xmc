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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "github.com/google/xmc/api/protos/components/v1alpha1"
)

func TestToProtoComponent(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	deleteTime := now.Add(1 * time.Hour)
	observedGen := int64(3)

	tests := []struct {
		name   string
		record *ComponentRecord
		want   *componentspb.Component
	}{
		{
			name:   "NilRecord",
			record: nil,
			want:   nil,
		},
		{
			name: "FullyPopulatedRecordWithExistingStatus",
			record: &ComponentRecord{
				ComponentID: 10,
				Name:        "comp-a",
				Spec: &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: "v1.2.3",
				},
				Status: &componentspb.ComponentAccessStatus{
					State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
					ObservedGeneration: 3,
					HealthMessage:      "Healthy",
					Ready:              true,
				},
				State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
				Generation:         3,
				ObservedGeneration: &observedGen,
				CreateTime:         now,
				UpdateTime:         now,
				DeleteTime:         &deleteTime,
				Labels:             map[string]string{"env": "prod"},
				Etag:               "etag-123",
			},
			want: &componentspb.Component{
				Name: "components/comp-a",
				Spec: &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: "v1.2.3",
				},
				Status: &componentspb.ComponentAccessStatus{
					State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
					ObservedGeneration: 3,
					HealthMessage:      "Healthy",
					Ready:              true,
				},
				CreateTime: timestamppb.New(now),
				UpdateTime: timestamppb.New(now),
				DeleteTime: timestamppb.New(deleteTime),
				Labels:     map[string]string{"env": "prod"},
				Etag:       "etag-123",
			},
		},
		{
			name: "StatusNilWithTopLevelStateAndObservedGen",
			record: &ComponentRecord{
				Name:               "comp-b",
				State:              componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
				Generation:         1,
				ObservedGeneration: &observedGen,
				CreateTime:         now,
				UpdateTime:         now,
			},
			want: &componentspb.Component{
				Name: "components/comp-b",
				Status: &componentspb.ComponentAccessStatus{
					State:              componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
					ObservedGeneration: 3,
				},
				CreateTime: timestamppb.New(now),
				UpdateTime: timestamppb.New(now),
			},
		},
		{
			name: "StatusNilWithOnlyTopLevelState",
			record: &ComponentRecord{
				Name:  "comp-c",
				State: componentspb.ComponentState_COMPONENT_STATE_DISABLING,
			},
			want: &componentspb.Component{
				Name: "components/comp-c",
				Status: &componentspb.ComponentAccessStatus{
					State: componentspb.ComponentState_COMPONENT_STATE_DISABLING,
				},
			},
		},
		{
			name: "StatusNilAndStateUnspecifiedAndObservedGenNil",
			record: &ComponentRecord{
				Name:  "comp-d",
				State: componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED,
			},
			want: &componentspb.Component{
				Name:   "components/comp-d",
				Status: nil,
			},
		},
		{
			name: "StatusPopulatedWithUnspecifiedStateAndZeroObservedGenMergedFromRecord",
			record: &ComponentRecord{
				Name: "comp-e",
				Status: &componentspb.ComponentAccessStatus{
					HealthMessage: "Reconciling",
				},
				State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
				ObservedGeneration: &observedGen,
			},
			want: &componentspb.Component{
				Name: "components/comp-e",
				Status: &componentspb.ComponentAccessStatus{
					State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
					ObservedGeneration: 3,
					HealthMessage:      "Reconciling",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ToProtoComponent(tc.record)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ToProtoComponent() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestToProtoComponent_Immutability(t *testing.T) {
	spec := &componentspb.ComponentSpec{
		Version: "v1.0.0",
	}
	status := &componentspb.ComponentAccessStatus{
		HealthMessage: "OK",
	}
	labels := map[string]string{"k": "v"}
	rec := &ComponentRecord{
		Name:   "comp-immutability",
		Spec:   spec,
		Status: status,
		Labels: labels,
	}

	protoComp := ToProtoComponent(rec)
	if protoComp == nil {
		t.Fatal("ToProtoComponent returned nil")
	}

	// Mutate proto copy
	protoComp.GetSpec().Version = "v2.0.0"
	protoComp.GetStatus().HealthMessage = "MUTATED"
	protoComp.GetLabels()["k"] = "mutated"

	// Assert original record was unmodified
	if rec.Spec.GetVersion() != "v1.0.0" {
		t.Errorf("Original spec modified: got %q, want 'v1.0.0'", rec.Spec.GetVersion())
	}
	if rec.Status.GetHealthMessage() != "OK" {
		t.Errorf("Original status modified: got %q, want 'OK'", rec.Status.GetHealthMessage())
	}
	if rec.Labels["k"] != "v" {
		t.Errorf("Original labels modified: got %q, want 'v'", rec.Labels["k"])
	}
}
