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
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/logging"
)

var (
	lifecycleEventsPublishedTotal = MetricsFactory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xmc_actuator_batcher_lifecycle_events_published_total",
			Help: "Total number of artifact lifecycle events successfully published to Redis.",
		},
		[]string{"state"},
	)

	lifecycleEventPublishFailuresTotal = MetricsFactory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xmc_actuator_batcher_lifecycle_event_publish_failures_total",
			Help: "Total number of artifact lifecycle events that failed to publish to Redis.",
		},
		[]string{"state"},
	)
)

// LifecycleNotifier publishes ArtifactLifecycleEvents.
type LifecycleNotifier interface {
	NotifyLifecycleEvent(ctx context.Context, event *taskqueuepb.ArtifactLifecycleEvent) error
}

// LifecycleHints carries the reconciler-side metadata that decides whether and
// how an artifact lifecycle event is published for a WorkUnitExecutionUpdate.
// It travels next to the storage update so storage stays unaware of lifecycle
// notifications.
type LifecycleHints struct {
	// NotifyWhen is the xmc.google.com/notify-when annotation value.
	NotifyWhen string
	// RuntimeContext is the JobSet runtime snapshot attached to published events.
	RuntimeContext *taskqueuepb.JobSetRuntimeContext
	// ForceNotify publishes an event even if the committed state did not change
	// (for example, when notify-when was newly added for the current state).
	ForceNotify bool
}

// LifecycleEvent identifies a committed execution state change to publish.
type LifecycleEvent struct {
	XID, WID, EID  int64
	OldState       essmsgpb.WorkUnitStatus_WorkUnitState
	NewState       essmsgpb.WorkUnitStatus_WorkUnitState
	RuntimeContext *taskqueuepb.JobSetRuntimeContext
}

// PublishLifecycleEvent publishes e through notifier on a best-effort basis:
// failures are logged and counted, never returned, because the execution state
// has already been committed.
func PublishLifecycleEvent(ctx context.Context, notifier LifecycleNotifier, e LifecycleEvent) {
	state := e.NewState.String()
	logArgs := []any{"xid", e.XID, "wid", e.WID, "eid", e.EID, "oldState", e.OldState.String(), "newState", state}
	if err := notifier.NotifyLifecycleEvent(ctx, newArtifactLifecycleEvent(e)); err != nil {
		lifecycleEventPublishFailuresTotal.WithLabelValues(state).Inc()
		logging.Error(ctx, "Failed to publish artifact lifecycle event", append(logArgs, "error", err)...)
		return
	}
	lifecycleEventsPublishedTotal.WithLabelValues(state).Inc()
	logging.Info(ctx, "Published artifact lifecycle event", logArgs...)
}

func newArtifactLifecycleEvent(e LifecycleEvent) *taskqueuepb.ArtifactLifecycleEvent {
	return &taskqueuepb.ArtifactLifecycleEvent{
		ExperimentId:        proto.Int64(e.XID),
		WorkUnitId:          proto.Int64(e.WID),
		WorkUnitExecutionId: proto.Int64(e.EID),
		OldState:            e.OldState.Enum(),
		NewState:            e.NewState.Enum(),
		Payload: &taskqueuepb.ArtifactLifecycleEvent_JobSetRuntimeContext{
			JobSetRuntimeContext: e.RuntimeContext,
		},
	}
}
