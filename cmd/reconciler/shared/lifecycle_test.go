// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package shared

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
)

func TestPublishLifecycleEvent(t *testing.T) {
	runtimeCtx := &taskqueuepb.JobSetRuntimeContext{JobSetName: proto.String("jobset-1")}
	event := LifecycleEvent{
		XID:            100,
		WID:            2,
		EID:            3,
		OldState:       essmsgpb.WorkUnitStatus_RUNNING,
		NewState:       essmsgpb.WorkUnitStatus_COMPLETED,
		RuntimeContext: runtimeCtx,
	}
	wantEvent := &taskqueuepb.ArtifactLifecycleEvent{
		ExperimentId:        proto.Int64(100),
		WorkUnitId:          proto.Int64(2),
		WorkUnitExecutionId: proto.Int64(3),
		OldState:            essmsgpb.WorkUnitStatus_RUNNING.Enum(),
		NewState:            essmsgpb.WorkUnitStatus_COMPLETED.Enum(),
		Payload:             &taskqueuepb.ArtifactLifecycleEvent_JobSetRuntimeContext{JobSetRuntimeContext: runtimeCtx},
	}
	tests := []struct {
		name          string
		notifyErr     error
		wantPublished float64
		wantFailures  float64
	}{
		{name: "success increments published counter", wantPublished: 1},
		{name: "failure increments failure counter only", notifyErr: errors.New("redis down"), wantFailures: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lifecycleEventsPublishedTotal.Reset()
			lifecycleEventPublishFailuresTotal.Reset()
			notifier := &mockLifecycleNotifier{err: tc.notifyErr}

			PublishLifecycleEvent(t.Context(), notifier, event)

			if diff := cmp.Diff([]*taskqueuepb.ArtifactLifecycleEvent{wantEvent}, notifier.events, protocmp.Transform()); diff != "" {
				t.Errorf("published events mismatch (-want +got):\n%s", diff)
			}
			state := essmsgpb.WorkUnitStatus_COMPLETED.String()
			if got := testutil.ToFloat64(lifecycleEventsPublishedTotal.WithLabelValues(state)); got != tc.wantPublished {
				t.Errorf("lifecycleEventsPublishedTotal = %v, want %v", got, tc.wantPublished)
			}
			if got := testutil.ToFloat64(lifecycleEventPublishFailuresTotal.WithLabelValues(state)); got != tc.wantFailures {
				t.Errorf("lifecycleEventPublishFailuresTotal = %v, want %v", got, tc.wantFailures)
			}
		})
	}
}
