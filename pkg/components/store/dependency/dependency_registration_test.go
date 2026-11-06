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

package dependency

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

const (
	notRegisteredMissing = `rpc error: code = FailedPrecondition desc = dependency not found: component "ingestion-gateway" depends on "missing", which is not registered`
	notRegisteredGone    = `rpc error: code = FailedPrecondition desc = dependency not found: component "ingestion-gateway" depends on "gone", which is not registered`
)

// registrationCatalog returns a catalog with two registered components, a soft-deleted one, a nil
// entry, and a "redpanda" -> "ingestion-gateway" edge so that the reverse edge would close a cycle.
func registrationCatalog() map[string]*store.ComponentRecord {
	now := time.Now().UTC()
	return map[string]*store.ComponentRecord{
		"redpanda": {ComponentID: 11, Name: "redpanda", Dependencies: []string{"ingestion-gateway"}},
		"auth":     {ComponentID: 22, Name: "auth"},
		"gone":     {ComponentID: 33, Name: "gone", DeleteTime: &now},
		"nil-rec":  nil,
	}
}

func TestResolveDependencyIDs(t *testing.T) {
	tests := []struct {
		name    string
		deps    []string
		want    map[string]int64
		wantErr string
	}{
		{name: "no dependencies", deps: nil, want: map[string]int64{}},
		{name: "all registered", deps: []string{"auth", "redpanda"}, want: map[string]int64{"auth": 22, "redpanda": 11}},
		{name: "unregistered", deps: []string{"redpanda", "missing"}, wantErr: notRegisteredMissing},
		{name: "soft-deleted", deps: []string{"gone"}, wantErr: notRegisteredGone},
		{name: "nil record", deps: []string{"nil-rec"}, wantErr: `rpc error: code = FailedPrecondition desc = dependency not found: component "ingestion-gateway" depends on "nil-rec", which is not registered`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveDependencyIDs(registrationCatalog(), "ingestion-gateway", tt.deps)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("ResolveDependencyIDs() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveDependencyIDs() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ResolveDependencyIDs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateIntentDependencies(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		deps    []string
		want    map[string]int64
		wantErr string
	}{
		{name: "registered dependencies resolve to IDs", target: "ingestion-gateway", deps: []string{"auth"}, want: map[string]int64{"auth": 22}},
		{name: "unregistered dependency", target: "ingestion-gateway", deps: []string{"auth", "missing"}, wantErr: notRegisteredMissing},
		{name: "soft-deleted dependency", target: "ingestion-gateway", deps: []string{"gone"}, wantErr: notRegisteredGone},
		{name: "cycle is reported before registration", target: "ingestion-gateway", deps: []string{"missing", "redpanda"}, wantErr: "rpc error: code = FailedPrecondition desc = dependency cycle detected: ingestion-gateway -> redpanda -> ingestion-gateway"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateIntentDependencies(registrationCatalog(), tt.target, tt.deps)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("ValidateIntentDependencies() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateIntentDependencies() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ValidateIntentDependencies() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDependencyNotRegisteredError(t *testing.T) {
	st := status.Convert(dependencyNotRegisteredError("ingestion", "redpanda"))

	if want := DependencyNotFoundMessagePrefix + ` component "ingestion" depends on "redpanda", which is not registered`; st.Code() != codes.FailedPrecondition || st.Message() != want {
		t.Errorf("status = %v %q, want %v %q", st.Code(), st.Message(), codes.FailedPrecondition, want)
	}
	want := []any{&errdetails.ErrorInfo{Reason: "DEPENDENCY_NOT_REGISTERED", Domain: store.ErrorInfoDomain, Metadata: map[string]string{"operation": "SetComponentIntent", "component": "ingestion", "dependency": "redpanda"}}}
	if diff := cmp.Diff(want, st.Details(), protocmp.Transform()); diff != "" {
		t.Errorf("details mismatch (-want +got):\n%s", diff)
	}
}

func TestValidateIntentStability(t *testing.T) {
	now := time.Now().UTC()
	catalog := func() map[string]*store.ComponentRecord {
		return map[string]*store.ComponentRecord{
			"stable-dep": {
				ComponentID: 1, Name: "stable-dep",
				Spec: &componentspb.ComponentSpec{Stability: componentspb.ComponentStability_COMPONENT_STABILITY_STABLE},
			},
			"preview-dep": {
				ComponentID: 2, Name: "preview-dep",
				Spec: &componentspb.ComponentSpec{Stability: componentspb.ComponentStability_COMPONENT_STABILITY_PREVIEW},
			},
			"exp-dep": {
				ComponentID: 3, Name: "exp-dep",
				Spec: &componentspb.ComponentSpec{Stability: componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL},
			},
			"stable-consumer": {
				ComponentID: 4, Name: "stable-consumer",
				Spec:         &componentspb.ComponentSpec{Stability: componentspb.ComponentStability_COMPONENT_STABILITY_STABLE},
				Dependencies: []string{"stable-dep"},
			},
			"exp-consumer": {
				ComponentID: 5, Name: "exp-consumer",
				Spec:         &componentspb.ComponentSpec{Stability: componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL},
				Dependencies: []string{"stable-dep"},
			},
			"deleted-consumer": {
				ComponentID: 6, Name: "deleted-consumer", DeleteTime: &now,
				Spec:         &componentspb.ComponentSpec{Stability: componentspb.ComponentStability_COMPONENT_STABILITY_STABLE},
				Dependencies: []string{"preview-dep"},
			},
		}
	}

	tests := []struct {
		name           string
		target         string
		stability      componentspb.ComponentStability
		deps           []string
		wantComponent  string
		wantDependency string
	}{
		{
			name:      "STABLE depending on STABLE and PREVIEW is allowed",
			target:    "gateway",
			stability: componentspb.ComponentStability_COMPONENT_STABILITY_STABLE,
			deps:      []string{"stable-dep", "preview-dep"},
		},
		{
			name:      "EXPERIMENTAL depending on EXPERIMENTAL, PREVIEW, and STABLE is allowed",
			target:    "gateway",
			stability: componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL,
			deps:      []string{"exp-dep", "preview-dep", "stable-dep"},
		},
		{
			name:      "flipping component to EXPERIMENTAL when only EXPERIMENTAL or deleted dependents exist is allowed",
			target:    "preview-dep",
			stability: componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL,
		},
		{
			name:           "UNSPECIFIED (default STABLE) depending on EXPERIMENTAL is rejected",
			target:         "gateway",
			stability:      componentspb.ComponentStability_COMPONENT_STABILITY_UNSPECIFIED,
			deps:           []string{"exp-dep"},
			wantComponent:  "gateway",
			wantDependency: "exp-dep",
		},
		{
			name:           "STABLE depending on EXPERIMENTAL is rejected",
			target:         "gateway",
			stability:      componentspb.ComponentStability_COMPONENT_STABILITY_STABLE,
			deps:           []string{"exp-dep"},
			wantComponent:  "gateway",
			wantDependency: "exp-dep",
		},
		{
			name:           "PREVIEW depending on EXPERIMENTAL is rejected",
			target:         "gateway",
			stability:      componentspb.ComponentStability_COMPONENT_STABILITY_PREVIEW,
			deps:           []string{"exp-dep"},
			wantComponent:  "gateway",
			wantDependency: "exp-dep",
		},
		{
			name:           "downgrading a dependency to EXPERIMENTAL while an active STABLE dependent exists is rejected",
			target:         "stable-dep",
			stability:      componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL,
			wantComponent:  "stable-consumer",
			wantDependency: "stable-dep",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIntentStability(catalog(), tt.target, tt.stability, tt.deps)
			if tt.wantComponent == "" {
				if err != nil {
					t.Fatalf("ValidateIntentStability() unexpected error = %v", err)
				}
				return
			}
			st := status.Convert(err)
			if st.Code() != codes.FailedPrecondition || store.ErrorInfoReason(err) != store.ReasonStabilityInversion {
				t.Fatalf("ValidateIntentStability() error = %v, want FailedPrecondition with reason %s", err, store.ReasonStabilityInversion)
			}
			wantDetails := []any{&errdetails.ErrorInfo{
				Reason: store.ReasonStabilityInversion,
				Domain: store.ErrorInfoDomain,
				Metadata: map[string]string{
					store.MetadataKeyOperation:  store.OperationSetComponentIntent,
					store.MetadataKeyComponent:  tt.wantComponent,
					store.MetadataKeyDependency: tt.wantDependency,
				},
			}}
			if diff := cmp.Diff(wantDetails, st.Details(), protocmp.Transform()); diff != "" {
				t.Errorf("ValidateIntentStability() details mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
