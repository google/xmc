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

package store_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

func TestCanonicalComponentName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain identifier",
			input: "my-component",
			want:  "my-component",
		},
		{
			name:  "with components prefix",
			input: "components/my-component",
			want:  "my-component",
		},
		{
			name:  "with leading and trailing slashes",
			input: "/components/my-component/",
			want:  "my-component",
		},
		{
			name:  "with whitespace",
			input: "  components/my-component/  ",
			want:  "my-component",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  "",
		},
		{
			name:  "slashes only",
			input: "///",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.CanonicalComponentName(tt.input)
			if got != tt.want {
				t.Errorf("CanonicalComponentName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFullComponentName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "canonical identifier",
			input: "jobset-controller",
			want:  "components/jobset-controller",
		},
		{
			name:  "already full name",
			input: "components/jobset-controller",
			want:  "components/jobset-controller",
		},
		{
			name:  "with slashes and whitespace",
			input: " /components/jobset-controller/ ",
			want:  "components/jobset-controller",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.FullComponentName(tt.input)
			if got != tt.want {
				t.Errorf("FullComponentName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsValidComponentName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "valid standard name", input: "kueue-operator", want: true},
		{name: "valid single char", input: "a", want: true},
		{name: "valid single digit", input: "1", want: true},
		{name: "valid alphanumeric with hyphens", input: "comp-1-v2", want: true},
		{name: "valid max 63 chars", input: strings.Repeat("a", 63), want: true},
		{name: "invalid empty string", input: "", want: false},
		{name: "invalid exceeds 63 chars", input: strings.Repeat("a", 64), want: false},
		{name: "invalid leading hyphen", input: "-invalid", want: false},
		{name: "invalid trailing hyphen", input: "invalid-", want: false},
		{name: "invalid uppercase", input: "InvalidName", want: false},
		{name: "invalid underscore", input: "invalid_name", want: false},
		{name: "invalid dot", input: "invalid.name", want: false},
		{name: "invalid slash", input: "invalid/name", want: false},
		{name: "invalid special chars", input: "comp@123", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.IsValidComponentName(tt.input)
			if got != tt.want {
				t.Errorf("IsValidComponentName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestToProto(t *testing.T) {
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	deleteTime := now.Add(time.Hour)
	obsGen := int64(3)

	t.Run("nil record", func(t *testing.T) {
		if got := store.ToProto(nil); got != nil {
			t.Errorf("ToProto(nil) = %v, want nil", got)
		}
	})

	t.Run("full record", func(t *testing.T) {
		record := &store.ComponentRecord{
			ComponentID: 101,
			Name:        "test-component",
			Spec: &componentspb.ComponentSpec{
				Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version:     "1.0.0",
				Generation:  3,
				Description: "Test component spec",
			},
			Status: &componentspb.ComponentAccessStatus{
				State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
				ObservedGeneration: 3,
				Ready:              true,
			},
			State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Generation:         3,
			ObservedGeneration: &obsGen,
			Dependencies:       []string{"dep-a", "dep-b"},
			CreateTime:         now,
			UpdateTime:         now,
			DeleteTime:         &deleteTime,
			Labels:             map[string]string{"env": "prod", "tier": "control-plane"},
			Etag:               "etag-12345",
		}

		want := &componentspb.Component{
			Name: "components/test-component",
			Spec: &componentspb.ComponentSpec{
				Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version:     "1.0.0",
				Generation:  3,
				Description: "Test component spec",
			},
			Status: &componentspb.ComponentAccessStatus{
				State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
				ObservedGeneration: 3,
				Ready:              true,
			},
			CreateTime: timestamppb.New(now),
			UpdateTime: timestamppb.New(now),
			DeleteTime: timestamppb.New(deleteTime),
			Labels:     map[string]string{"env": "prod", "tier": "control-plane"},
			Etag:       "etag-12345",
		}

		got := store.ToProto(record)
		if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
			t.Errorf("ToProto() mismatch (-want +got):\n%s", diff)
		}

		// Ensure deep copies of maps and protos
		record.Labels["env"] = "mutated"
		if got.GetLabels()["env"] != "prod" {
			t.Errorf("ToProto() did not deep-copy labels: got %q, want prod", got.GetLabels()["env"])
		}

		record.Spec.Version = "2.0.0"
		if got.GetSpec().GetVersion() != "1.0.0" {
			t.Errorf("ToProto() did not deep-copy Spec: got %q, want 1.0.0", got.GetSpec().GetVersion())
		}
	})

	t.Run("partial record with zero timestamps and nil delete time", func(t *testing.T) {
		record := &store.ComponentRecord{
			Name: "minimal-comp",
			Etag: "etag-minimal",
		}

		got := store.ToProto(record)
		if got.GetName() != "components/minimal-comp" {
			t.Errorf("ToProto().GetName() = %q, want components/minimal-comp", got.GetName())
		}
		if got.GetCreateTime() != nil {
			t.Errorf("ToProto().GetCreateTime() = %v, want nil", got.GetCreateTime())
		}
		if got.GetDeleteTime() != nil {
			t.Errorf("ToProto().GetDeleteTime() = %v, want nil", got.GetDeleteTime())
		}
	})
}

func TestFromProto(t *testing.T) {
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	deleteTime := now.Add(time.Hour)
	obsGen := int64(2)

	t.Run("nil component", func(t *testing.T) {
		if got := store.FromProto(nil); got != nil {
			t.Errorf("FromProto(nil) = %v, want nil", got)
		}
	})

	t.Run("full component", func(t *testing.T) {
		comp := &componentspb.Component{
			Name: "components/full-comp",
			Spec: &componentspb.ComponentSpec{
				Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				Version:     "2.1.0",
				Generation:  2,
				Description: "Wrapper description",
			},
			Status: &componentspb.ComponentAccessStatus{
				State:              componentspb.ComponentState_COMPONENT_STATE_DISABLING,
				ObservedGeneration: 2,
				Ready:              false,
				HealthMessage:      "Draining",
			},
			CreateTime: timestamppb.New(now),
			UpdateTime: timestamppb.New(now),
			DeleteTime: timestamppb.New(deleteTime),
			Labels:     map[string]string{"system": "xmc"},
			Etag:       "etag-abc",
		}

		want := &store.ComponentRecord{
			Name: "full-comp",
			Spec: &componentspb.ComponentSpec{
				Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				Version:     "2.1.0",
				Generation:  2,
				Description: "Wrapper description",
			},
			Status: &componentspb.ComponentAccessStatus{
				State:              componentspb.ComponentState_COMPONENT_STATE_DISABLING,
				ObservedGeneration: 2,
				Ready:              false,
				HealthMessage:      "Draining",
			},
			State:              componentspb.ComponentState_COMPONENT_STATE_DISABLING,
			Generation:         2,
			ObservedGeneration: &obsGen,
			CreateTime:         now,
			UpdateTime:         now,
			DeleteTime:         &deleteTime,
			Labels:             map[string]string{"system": "xmc"},
			Etag:               "etag-abc",
		}

		got := store.FromProto(comp)
		if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
			t.Errorf("FromProto() mismatch (-want +got):\n%s", diff)
		}

		// Ensure deep copies of maps and protos
		comp.Labels["system"] = "mutated"
		if got.Labels["system"] != "xmc" {
			t.Errorf("FromProto() did not deep-copy labels")
		}

		comp.Spec.Version = "3.0.0"
		if got.Spec.GetVersion() != "2.1.0" {
			t.Errorf("FromProto() did not deep-copy Spec")
		}
	})

	t.Run("partial component with nil fields", func(t *testing.T) {
		comp := &componentspb.Component{
			Name: "simple-comp",
		}

		got := store.FromProto(comp)
		if got.Name != "simple-comp" {
			t.Errorf("FromProto().Name = %q, want simple-comp", got.Name)
		}
		if got.Spec != nil || got.Status != nil || got.DeleteTime != nil {
			t.Errorf("FromProto() unexpected non-nil fields: %+v", got)
		}
	})
}
