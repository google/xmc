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
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func TestIsUnchangedIntent(t *testing.T) {
	spec := func(version string, gen int64) *componentspb.ComponentSpec {
		return &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: version, Generation: gen}
	}
	rec := &ComponentRecord{
		Generation:   3,
		Spec:         spec("v1.0.0", 3),
		Labels:       map[string]string{"tier": "backend"},
		Dependencies: []string{"redpanda"},
	}
	bareRec := &ComponentRecord{Generation: 3, Spec: spec("v1.0.0", 3)}
	tests := []struct {
		name       string
		rec        *ComponentRecord
		spec       *componentspb.ComponentSpec
		labels     map[string]string
		deps       []string
		desiredGen int64
		want       bool
	}{
		{name: "same generation and inputs", rec: rec, spec: spec("v1.0.0", 3), labels: rec.Labels, deps: rec.Dependencies, desiredGen: 3, want: true},
		{name: "identical spec including generation and unspecified desired generation", rec: rec, spec: spec("v1.0.0", 3), labels: rec.Labels, deps: rec.Dependencies, desiredGen: 0, want: true},
		{name: "spec generation field is ignored", rec: rec, spec: spec("v1.0.0", 0), labels: rec.Labels, deps: rec.Dependencies, desiredGen: 3, want: true},
		{name: "unspecified generation", rec: rec, spec: spec("v1.0.0", 0), labels: rec.Labels, deps: rec.Dependencies, desiredGen: 0, want: true},
		{name: "stale generation", rec: rec, spec: spec("v1.0.0", 2), labels: rec.Labels, deps: rec.Dependencies, desiredGen: 2, want: true},
		{name: "nil and empty labels are equal", rec: bareRec, spec: spec("v1.0.0", 3), labels: map[string]string{}, deps: nil, desiredGen: 3, want: true},
		{name: "nil and empty dependencies are equal", rec: bareRec, spec: spec("v1.0.0", 3), labels: nil, deps: []string{}, desiredGen: 3, want: true},
		{name: "newer generation", rec: rec, spec: spec("v1.0.0", 4), labels: rec.Labels, deps: rec.Dependencies, desiredGen: 4, want: false},
		{name: "changed spec", rec: rec, spec: spec("v1.1.0", 3), labels: rec.Labels, deps: rec.Dependencies, desiredGen: 3, want: false},
		{name: "changed labels", rec: rec, spec: spec("v1.0.0", 3), labels: map[string]string{"tier": "frontend"}, deps: rec.Dependencies, desiredGen: 3, want: false},
		{name: "changed dependencies", rec: rec, spec: spec("v1.0.0", 3), labels: rec.Labels, deps: []string{"clickhouse"}, desiredGen: 3, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUnchangedIntent(tt.rec, tt.spec, tt.labels, tt.deps, tt.desiredGen); got != tt.want {
				t.Errorf("isUnchangedIntent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSpecWithoutGeneration(t *testing.T) {
	zeroGen := &componentspb.ComponentSpec{Version: "v1.0.0"}
	if got := specWithoutGeneration(zeroGen); got != zeroGen {
		t.Errorf("specWithoutGeneration(generation 0) = %p, want the input %p without a copy", got, zeroGen)
	}
	if got := specWithoutGeneration(nil); got != nil {
		t.Errorf("specWithoutGeneration(nil) = %v, want nil", got)
	}
	withGen := &componentspb.ComponentSpec{Version: "v1.0.0", Generation: 3}
	got := specWithoutGeneration(withGen)
	if got == withGen || got.GetGeneration() != 0 || got.GetVersion() != "v1.0.0" {
		t.Errorf("specWithoutGeneration(generation 3) = %v, want a separate copy with generation 0", got)
	}
	if withGen.GetGeneration() != 3 {
		t.Errorf("specWithoutGeneration mutated its input generation to %d, want 3", withGen.GetGeneration())
	}
}

func TestRejectStaleIntent(t *testing.T) {
	rec := &ComponentRecord{Generation: 5}
	tests := []struct {
		name       string
		desiredGen int64
		wantStale  bool
	}{
		{name: "unspecified generation is accepted", desiredGen: 0},
		{name: "negative generation is treated as unspecified", desiredGen: -1},
		{name: "older generation is rejected", desiredGen: 4, wantStale: true},
		{name: "oldest generation is rejected", desiredGen: 1, wantStale: true},
		{name: "same generation is accepted", desiredGen: 5},
		{name: "newer generation is accepted", desiredGen: 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectStaleIntent("redpanda", rec, tt.desiredGen)
			if !tt.wantStale {
				if err != nil {
					t.Errorf("rejectStaleIntent(desiredGen %d) error = %v, want nil", tt.desiredGen, err)
				}
				return
			}
			if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), StaleIntentMessagePrefix) {
				t.Errorf("rejectStaleIntent(desiredGen %d) error = %v, want FailedPrecondition starting with %q", tt.desiredGen, err, StaleIntentMessagePrefix)
			}
		})
	}
}

func TestMemoryStore_UnchangedIntentRecordsNoSnapshot(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()
	spec := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.0.0"}
	for range 3 {
		if _, err := ms.SetComponentIntent(ctx, "redpanda", spec, nil, 3); err != nil {
			t.Fatalf("SetComponentIntent() error = %v", err)
		}
	}
	if got := len(ms.snapshots["redpanda"]); got != 1 {
		t.Errorf("snapshots after repeated unchanged intent = %d, want 1", got)
	}
}
