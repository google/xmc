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
	"testing"
	"time"

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

func TestPlanIntent(t *testing.T) {
	spec := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.0.0"}
	changedSpec := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.1.0"}
	rec := &ComponentRecord{Generation: 5, Spec: spec}
	tests := []struct {
		name       string
		spec       *componentspb.ComponentSpec
		labels     map[string]string
		deps       []string
		desiredGen int64
		want       intentPlan
	}{
		{name: "unchanged intent writes nothing", spec: spec, desiredGen: 5, want: intentPlan{write: writeNothing, generation: 5}},
		{name: "older desired generation with an equal spec writes nothing", spec: spec, desiredGen: 3, want: intentPlan{write: writeNothing, generation: 5}},
		{name: "newer desired generation jumps to it", spec: spec, desiredGen: 9, want: intentPlan{write: writeSpec, generation: 9}},
		{name: "changed spec at an older desired generation bumps by one", spec: changedSpec, desiredGen: 3, want: intentPlan{write: writeSpec, generation: 6}},
		{name: "labels-only change writes the spec", spec: spec, labels: map[string]string{"tier": "backend"}, desiredGen: 5, want: intentPlan{write: writeSpec, generation: 6}},
		{name: "dependencies-only change writes the spec", spec: spec, deps: []string{"redpanda"}, desiredGen: 5, want: intentPlan{write: writeSpec, generation: 6}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planIntent(rec, tt.spec, tt.labels, tt.deps, tt.desiredGen); got != tt.want {
				t.Errorf("planIntent() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestPlanLiveIntent checks the create-versus-update dispatch both stores rely on: a missing or
// soft-deleted record is planned as a create at newIntentGeneration, and an active record is
// returned with the planIntent outcome.
func TestPlanLiveIntent(t *testing.T) {
	spec := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.0.0"}
	active := &ComponentRecord{Name: "redpanda", Generation: 5, Spec: spec}
	deleteTime := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	softDeleted := &ComponentRecord{Name: "redpanda", Generation: 5, Spec: spec, DeleteTime: &deleteTime}
	tests := []struct {
		name       string
		catalog    map[string]*ComponentRecord
		desiredGen int64
		wantRec    *ComponentRecord
		wantPlan   intentPlan
	}{
		{name: "absent name is a create", catalog: map[string]*ComponentRecord{}, desiredGen: 0, wantPlan: intentPlan{write: writeSpec, generation: 1}},
		{name: "soft-deleted record is a create at the desired generation", catalog: map[string]*ComponentRecord{"redpanda": softDeleted}, desiredGen: 3, wantPlan: intentPlan{write: writeSpec, generation: 3}},
		{name: "active record delegates to planIntent", catalog: map[string]*ComponentRecord{"redpanda": active}, desiredGen: 5, wantRec: active, wantPlan: intentPlan{write: writeNothing, generation: 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, plan := planLiveIntent(tt.catalog, "redpanda", spec, nil, nil, tt.desiredGen)
			if rec != tt.wantRec {
				t.Errorf("planLiveIntent() record = %p, want %p", rec, tt.wantRec)
			}
			if plan != tt.wantPlan {
				t.Errorf("planLiveIntent() plan = %+v, want %+v", plan, tt.wantPlan)
			}
		})
	}
}

func TestNewIntentGeneration(t *testing.T) {
	tests := []struct {
		name       string
		desiredGen int64
		want       int64
	}{
		{name: "zero desired generation", desiredGen: 0, want: 1},
		{name: "generation one", desiredGen: 1, want: 1},
		{name: "generation seven", desiredGen: 7, want: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newIntentGeneration(tt.desiredGen); got != tt.want {
				t.Errorf("newIntentGeneration(%d) = %d, want %d", tt.desiredGen, got, tt.want)
			}
		})
	}
}
