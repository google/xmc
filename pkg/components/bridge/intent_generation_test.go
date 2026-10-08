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

package bridge

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	cmsserver "xmanager_cloud/pkg/components/cms"
	"xmanager_cloud/pkg/components/store"
)

func TestReconcileComponent_IntentCarriesCRGeneration(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "enabled", enabled: true},
		{name: "disabled", enabled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cms := &mockCMS{}
			r, err := NewReconciler(cms, nil, nil)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			in := ReconcileComponentInput{
				Namespace: "xmc", Name: "redpanda", Generation: 7, HasFinalizer: true, Enabled: tt.enabled,
				Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
			}
			if _, err := r.ReconcileComponent(context.Background(), in); err != nil {
				t.Fatalf("ReconcileComponent() error = %v", err)
			}
			if got := cms.lastSetReq.GetSpec().GetGeneration(); got != in.Generation {
				t.Errorf("SetComponentIntent spec.generation = %d, want CR generation %d", got, in.Generation)
			}
		})
	}
}

// TestReconcileComponent_UnchangedIntentKeepsCMSRecord verifies against the real CMS server and
// MemoryStore that repeated passes over an unchanged Component neither bump the CMS generation past
// the CR generation nor reset the observed ENABLED state.
func TestReconcileComponent_UnchangedIntentKeepsCMSRecord(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	r, err := NewReconciler(NewCMSClientAdapter(cmsserver.NewServer(ms)), nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	in := ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 3, HasFinalizer: true, Enabled: true,
		Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
	}
	for pass := 1; pass <= 3; pass++ {
		res, err := r.ReconcileComponent(ctx, in)
		if err != nil {
			t.Fatalf("pass %d: ReconcileComponent() error = %v", pass, err)
		}
		rec, err := ms.GetComponent(ctx, "redpanda")
		if err != nil {
			t.Fatalf("pass %d: GetComponent() error = %v", pass, err)
		}
		if rec.Generation != in.Generation || rec.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
			t.Errorf("pass %d: CMS generation = %d, state = %v; want %d, COMPONENT_STATE_ENABLED", pass, rec.Generation, rec.State, in.Generation)
		}
		in.ExistingConditions = res.Conditions
	}
}

func TestReconcileComponent_IntentSourceAndComponentGeneration(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name         string
		uid          string
		componentGen int64
		wantSource   *componentspb.IntentSource
		wantGen      int64
	}{
		{
			name: "custom resource with UID is the intent source", uid: "uid-a", componentGen: 1,
			wantSource: &componentspb.IntentSource{Uid: "uid-a", CreateTime: timestamppb.New(created), Generation: 7},
			wantGen:    1,
		},
		{name: "input without UID sends no source", wantGen: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cms := &mockCMS{componentGen: tt.componentGen}
			r, err := NewReconciler(cms, nil, nil)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			in := sourcedRedpandaInput(tt.uid, created, 7)
			res, err := r.ReconcileComponent(context.Background(), in)
			if err != nil {
				t.Fatalf("ReconcileComponent() error = %v", err)
			}
			if got := cms.lastSetReq.GetSource(); !proto.Equal(got, tt.wantSource) {
				t.Errorf("SetComponentIntent source = %v, want %v", got, tt.wantSource)
			}
			if res.ComponentGeneration != tt.wantGen || res.ObservedGeneration != in.Generation {
				t.Errorf("ComponentGeneration, ObservedGeneration = %d, %d; want %d, %d", res.ComponentGeneration, res.ObservedGeneration, tt.wantGen, in.Generation)
			}
			if got := cms.lastObsReq.GetObservedGeneration(); got != tt.wantGen {
				t.Errorf("UpdateObservedGeneration generation = %d, want %d", got, tt.wantGen)
			}
		})
	}
}

func TestUpdateObservedCMS_UnknownGenerationSkipsCMS(t *testing.T) {
	cms := &mockCMS{}
	r, err := NewReconciler(cms, nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	if err := r.updateObservedCMS(context.Background(), "redpanda", 0, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Ready=True"); err != nil {
		t.Fatalf("updateObservedCMS() error = %v", err)
	}
	if cms.lastObsReq != nil {
		t.Errorf("UpdateObservedGeneration request = %v, want no call", cms.lastObsReq)
	}
}

// TestReconcileComponent_RecreatedCustomResource verifies against the real CMS server and
// MemoryStore that the CMS generation is independent of the custom resource generation, that a
// recreated custom resource adopts the existing record, and that the older object is then stale.
func TestReconcileComponent_RecreatedCustomResource(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	r, err := NewReconciler(NewCMSClientAdapter(cmsserver.NewServer(ms)), nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	original := sourcedRedpandaInput("uid-old", created, 3)
	first := reconcileAndGetRecord(ctx, t, r, ms, original)
	second := reconcileAndGetRecord(ctx, t, r, ms, sourcedRedpandaInput("uid-new", created.Add(time.Hour), 1))
	if first.Generation != 1 || second.Generation != 1 || second.ComponentID != first.ComponentID {
		t.Errorf("CMS (generation, ComponentID) = (%d, %d) then (%d, %d); want generation 1 and the same ComponentID", first.Generation, first.ComponentID, second.Generation, second.ComponentID)
	}
	if got := second.Source.UID; got != "uid-new" {
		t.Errorf("CMS source UID = %q, want %q", got, "uid-new")
	}
	original.Generation = 4
	original.ComponentGeneration = first.Generation
	original.Spec.Version = "1.2.3"
	res, err := r.ReconcileComponent(ctx, original)
	if err == nil {
		t.Fatal("ReconcileComponent(older object) error = nil, want stale intent")
	}
	if got := findCondition(res.Conditions, ConditionTypeReady); got == nil || got.Reason != ReasonStaleIntent {
		t.Errorf("Ready condition = %+v, want reason %q", got, ReasonStaleIntent)
	}
	if res.ComponentGeneration != first.Generation {
		t.Errorf("ComponentGeneration after stale intent = %d, want unchanged %d", res.ComponentGeneration, first.Generation)
	}
}

func sourcedRedpandaInput(uid string, created time.Time, gen int64) ReconcileComponentInput {
	return ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: gen, UID: uid, CreationTimestamp: created,
		HasFinalizer: true, Enabled: true,
		Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
	}
}

func reconcileAndGetRecord(ctx context.Context, t *testing.T, r *Reconciler, ms *store.MemoryStore, in ReconcileComponentInput) *store.ComponentRecord {
	t.Helper()
	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent(%s) error = %v", in.UID, err)
	}
	rec, err := ms.GetComponent(ctx, "redpanda")
	if err != nil {
		t.Fatalf("GetComponent() error = %v", err)
	}
	if res.ComponentGeneration != rec.Generation {
		t.Errorf("ComponentGeneration = %d, want CMS generation %d", res.ComponentGeneration, rec.Generation)
	}
	return rec
}
