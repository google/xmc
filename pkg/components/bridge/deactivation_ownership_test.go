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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// TestReconcileComponent_StaleDeactivationReleasesOwnership checks that a disabled custom resource
// sends itself as the source of both deactivation steps and, when CMS rejects either because
// another custom resource owns the record, reports StaleIntent, drops componentGeneration, keeps
// componentId, writes nothing more to CMS and does not fail the pass. A rejected
// PrepareDeactivation also keeps the workload.
func TestReconcileComponent_StaleDeactivationReleasesOwnership(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	stale := status.Error(codes.FailedPrecondition, `stale deactivation: component "redpanda" is owned by source "uid-new"`)
	wantSource := &componentspb.IntentSource{Uid: "uid-old", CreateTime: timestamppb.New(created), Generation: 2}
	tests := []struct {
		name              string
		cms               *mockCMS
		wantCompleteCalls int
		wantTornDown      bool
	}{
		{name: "PrepareDeactivation rejected", cms: &mockCMS{componentGen: 3, componentID: 9, prepareErr: stale}},
		{name: "CompleteDeactivation rejected", cms: &mockCMS{componentGen: 3, componentID: 9, completeErr: stale}, wantCompleteCalls: 1, wantTornDown: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helm := &mockHelm{active: true}
			r, err := NewReconciler(tt.cms, nil, helm)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			in := sourcedRedpandaInput("uid-old", created, 2)
			in.Enabled, in.ComponentGeneration, in.ComponentID = false, 3, "9"
			res, err := r.ReconcileComponent(context.Background(), in)
			if err != nil {
				t.Fatalf("ReconcileComponent() error = %v, want nil", err)
			}
			if got := findCondition(res.Conditions, ConditionTypeReady); got == nil || got.Reason != ReasonStaleIntent {
				t.Errorf("Ready condition = %+v, want reason %q", got, ReasonStaleIntent)
			}
			if res.ComponentGeneration != 0 || res.ComponentID != "9" {
				t.Errorf("(ComponentGeneration, ComponentID) = (%d, %q), want (0, %q)", res.ComponentGeneration, res.ComponentID, "9")
			}
			if got := tt.cms.lastPrepareReq.GetSource(); !proto.Equal(got, wantSource) {
				t.Errorf("PrepareDeactivation source = %v, want %v", got, wantSource)
			}
			if tt.cms.completeCalls != tt.wantCompleteCalls || tt.cms.lastObsReq != nil {
				t.Errorf("CompleteDeactivation calls = %d, observed request = %v; want %d calls and no observed request", tt.cms.completeCalls, tt.cms.lastObsReq, tt.wantCompleteCalls)
			}
			if tt.wantCompleteCalls > 0 && !proto.Equal(tt.cms.lastCompleteReq.GetSource(), wantSource) {
				t.Errorf("CompleteDeactivation source = %v, want %v", tt.cms.lastCompleteReq.GetSource(), wantSource)
			}
			if helm.deleteCalled != tt.wantTornDown {
				t.Errorf("workload deleted = %t, want %t", helm.deleteCalled, tt.wantTornDown)
			}
		})
	}
}
