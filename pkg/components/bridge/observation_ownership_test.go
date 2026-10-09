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
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	cmsserver "xmanager_cloud/pkg/components/cms"
	"xmanager_cloud/pkg/components/store"
)

// observationCountingCMS counts the UpdateObservedGeneration RPCs sent to the wrapped client.
type observationCountingCMS struct {
	CMSClient
	observations int
}

func (c *observationCountingCMS) UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error) {
	c.observations++
	return c.CMSClient.UpdateObservedGeneration(ctx, in, opts...)
}

// recordSnapshot is the part of a CMS record an observed-state report may change.
type recordSnapshot struct {
	State              componentspb.ComponentState
	Generation         int64
	ObservedGeneration *int64
	Source             *store.IntentSource
	Etag               string
}

func snapshotRecord(ctx context.Context, t *testing.T, ms *store.MemoryStore, name string) recordSnapshot {
	t.Helper()
	rec, err := ms.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("GetComponent(%q) error = %v", name, err)
	}
	return recordSnapshot{rec.State, rec.Generation, rec.ObservedGeneration, rec.Source, rec.Etag}
}

// TestReconcileComponent_StaleObservationReleasesOwnership verifies against the real CMS server and
// MemoryStore that a custom resource gated on a dependency cycle cannot report observed state for a
// record another custom resource owns. The resource under test is newer than the owner but has not
// adopted the record yet. Pass 1 reports StaleIntent naming the owner, drops componentGeneration,
// keeps componentId and leaves the record unchanged. Pass 2 shows the real gate reason without
// another UpdateObservedGeneration RPC. Pass 3, with the cycle removed, adopts the record: it
// restores componentGeneration and reports observed state with exactly one RPC.
func TestReconcileComponent_StaleObservationReleasesOwnership(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemoryStore()
	cms := &observationCountingCMS{CMSClient: NewCMSClientAdapter(cmsserver.NewServer(ms))}
	r, err := NewReconciler(cms, nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rec := seedOwnedRecord(ctx, t, ms, created)
	before := snapshotRecord(ctx, t, ms, "redpanda")

	in := sourcedRedpandaInput("uid-newest", created.Add(2*time.Hour), 2)
	in.ComponentGeneration = rec.Generation
	in.ComponentID = strconv.FormatInt(rec.ComponentID, 10)
	in.Spec.Dependencies = []string{"ingestion"}
	in.Dependencies = NewDependencySnapshot([]unstructured.Unstructured{
		readyComponent("ingestion", withDependencies("redpanda")),
		readyComponent("redpanda", withDependencies("ingestion")),
	})

	res := reconcileOwnershipPass(ctx, t, r, cms, in, 1, ReasonStaleIntent)
	if got := findCondition(res.Conditions, ConditionTypeReady); !strings.Contains(got.Message, `is owned by source "uid-new"`) {
		t.Errorf("pass 1: Ready message = %q, want it to name the owner %q", got.Message, "uid-new")
	}
	if got := findCondition(res.Conditions, ConditionTypeDegraded); got == nil || got.Status != ConditionStatusTrue {
		t.Errorf("pass 1: Degraded condition = %+v, want status %q", got, ConditionStatusTrue)
	}
	if diff := cmp.Diff(before, snapshotRecord(ctx, t, ms, "redpanda")); diff != "" {
		t.Errorf("pass 1: CMS record changed after a stale observation (-before +after):\n%s", diff)
	}

	in = nextOwnershipPass(in, res, cms)
	res = reconcileOwnershipPass(ctx, t, r, cms, in, 0, ReasonDependencyCycle)

	in = nextOwnershipPass(in, res, cms)
	in.Spec.Dependencies = nil
	in.Dependencies = NewDependencySnapshot(nil)
	checkOwnershipRestored(ctx, t, r, cms, ms, in)
}

// nextOwnershipPass returns the input of the pass after the one that produced res, carrying over
// its status, and resets the RPC count.
func nextOwnershipPass(in ReconcileComponentInput, res *ReconcileResult, cms *observationCountingCMS) ReconcileComponentInput {
	in.ComponentGeneration = res.ComponentGeneration
	in.ComponentID = res.ComponentID
	in.ExistingConditions = res.Conditions
	cms.observations = 0
	return in
}

// checkOwnershipRestored runs one pass for in and checks that its intent adopted the record: the
// result reports the record's generation and ComponentId, exactly one UpdateObservedGeneration RPC
// was sent, and the record's observed generation and source come from in.
func checkOwnershipRestored(ctx context.Context, t *testing.T, r *Reconciler, cms *observationCountingCMS, ms *store.MemoryStore, in ReconcileComponentInput) {
	t.Helper()
	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent(re-own) error = %v, want nil", err)
	}
	rec, err := ms.GetComponent(ctx, "redpanda")
	if err != nil {
		t.Fatalf("GetComponent() error = %v", err)
	}
	if res.ComponentGeneration <= 0 || res.ComponentGeneration != rec.Generation || res.ComponentID != in.ComponentID {
		t.Errorf("re-own: (ComponentGeneration, ComponentID) = (%d, %q), want (%d, %q)", res.ComponentGeneration, res.ComponentID, rec.Generation, in.ComponentID)
	}
	if cms.observations != 1 {
		t.Errorf("re-own: UpdateObservedGeneration calls = %d, want 1", cms.observations)
	}
	var sourceUID string
	if rec.Source != nil {
		sourceUID = rec.Source.UID
	}
	if rec.ObservedGeneration == nil || *rec.ObservedGeneration != rec.Generation || sourceUID != in.UID {
		t.Errorf("re-own: CMS (ObservedGeneration, source UID) = (%v, %q), want (%d, %q)", rec.ObservedGeneration, sourceUID, rec.Generation, in.UID)
	}
}

// seedOwnedRecord commits an intent from an older and then from a newer custom resource, so the
// newer one ("uid-new", created an hour after created) owns the returned record.
func seedOwnedRecord(ctx context.Context, t *testing.T, ms *store.MemoryStore, created time.Time) *store.ComponentRecord {
	t.Helper()
	spec := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	if _, err := ms.SetSourcedComponentIntent(ctx, "redpanda", spec, nil, store.IntentSource{UID: "uid-old", CreateTime: created, Generation: 1}); err != nil {
		t.Fatalf("SetSourcedComponentIntent(uid-old) error = %v", err)
	}
	rec, err := ms.SetSourcedComponentIntent(ctx, "redpanda", spec, nil, store.IntentSource{UID: "uid-new", CreateTime: created.Add(time.Hour), Generation: 1})
	if err != nil {
		t.Fatalf("SetSourcedComponentIntent(uid-new) error = %v", err)
	}
	return rec
}

// reconcileOwnershipPass runs one pass for in and checks that it succeeds with Ready reason
// wantReason, sends wantObservations UpdateObservedGeneration RPCs, reports no component
// generation and keeps in.ComponentID.
func reconcileOwnershipPass(ctx context.Context, t *testing.T, r *Reconciler, cms *observationCountingCMS, in ReconcileComponentInput, wantObservations int, wantReason string) *ReconcileResult {
	t.Helper()
	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent(%s) error = %v, want nil", wantReason, err)
	}
	if got := findCondition(res.Conditions, ConditionTypeReady); got == nil || got.Reason != wantReason {
		t.Fatalf("Ready condition = %+v, want reason %q", got, wantReason)
	}
	if cms.observations != wantObservations {
		t.Errorf("%s: UpdateObservedGeneration calls = %d, want %d", wantReason, cms.observations, wantObservations)
	}
	if res.ComponentGeneration != 0 || res.ComponentID != in.ComponentID {
		t.Errorf("%s: (ComponentGeneration, ComponentID) = (%d, %q), want (0, %q)", wantReason, res.ComponentGeneration, res.ComponentID, in.ComponentID)
	}
	return res
}

// TestUpdateObservedCMS_Rejection verifies that only a FailedPrecondition starting with the
// stale-observation prefix releases CMS ownership (clearing the component generation and access
// and reporting released with a nil error); any other failure, including a FailedPrecondition with
// another prefix, is returned with the generation and access kept.
func TestUpdateObservedCMS_Rejection(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name         string
		obsErr       error
		wantErr      bool
		wantReleased bool
		wantGen      int64
	}{
		{
			name:         "stale observation releases ownership",
			obsErr:       status.Error(codes.FailedPrecondition, `stale observation: component "redpanda" is owned by source "uid-new"`),
			wantReleased: true,
			wantGen:      0,
		},
		{
			name:    "failed precondition with another prefix is returned",
			obsErr:  status.Error(codes.FailedPrecondition, `stale deletion: component "redpanda" is owned by source "uid-new"`),
			wantErr: true,
			wantGen: 4,
		},
		{name: "other failure is returned", obsErr: errors.New("cms unavailable"), wantErr: true, wantGen: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cms := &mockCMS{obsErr: tt.obsErr}
			r, err := NewReconciler(cms, nil, nil)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			in := sourcedRedpandaInput("uid-old", created, 2)
			access := &AccessStatus{EnvVars: map[string]string{"REDPANDA_HOST": "redpanda"}}
			res := &ReconcileResult{ComponentGeneration: 4, ComponentID: "9", State: componentspb.ComponentState_COMPONENT_STATE_ENABLED, Access: access}
			released, err := r.updateObservedCMS(context.Background(), in, "redpanda", res, "Ready=True")
			if (err != nil) != tt.wantErr || released != tt.wantReleased {
				t.Fatalf("updateObservedCMS() = (%t, %v), want (%t, error %t)", released, err, tt.wantReleased, tt.wantErr)
			}
			if res.ComponentGeneration != tt.wantGen || res.ComponentID != "9" || (res.Access == nil) != tt.wantReleased {
				t.Errorf("(ComponentGeneration, ComponentID, Access) = (%d, %q, %v), want (%d, %q, cleared %t)", res.ComponentGeneration, res.ComponentID, res.Access, tt.wantGen, "9", tt.wantReleased)
			}
			wantSource := &componentspb.IntentSource{Uid: "uid-old", CreateTime: timestamppb.New(created), Generation: 2}
			if got := cms.lastObsReq.GetSource(); !proto.Equal(got, wantSource) {
				t.Errorf("UpdateObservedGeneration source = %v, want %v", got, wantSource)
			}
		})
	}
}
