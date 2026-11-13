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

package server

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	msgpb "xmanager_cloud/api/protos/experiment"
	taskpb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/logging"
)

// studyReconcilePublishTimeout bounds the publish of a study reconcile tick, so that a hung Redis
// delays CreateExperiment by at most this long.
const studyReconcilePublishTimeout = 2 * time.Second

var studyReconcileEnqueueFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "xmc_ess_study_reconcile_enqueue_failures_total",
	Help: "Number of created STUDY experiments whose initial study reconcile tick could not be enqueued.",
})

// StudyReconcilePublisher publishes study reconcile ticks to the Study Controller's Redis stream.
type StudyReconcilePublisher interface {
	AddMessage(ctx context.Context, data string, failedAttempts string) error
}

// enqueueStudyReconcile publishes the initial reconcile tick of exp when it is a STUDY. It runs
// after exp is committed, so a failure is logged and counted but not returned: failing the RPC
// would make the client retry and create a duplicate study. Such a study stays without a tick
// until a future sweeper recovers it. The publish is not cancelled with the request, since the
// study exists either way, and is bounded by studyReconcilePublishTimeout.
func (h *ExperimentHandler) enqueueStudyReconcile(ctx context.Context, exp *msgpb.Experiment) {
	if exp.GetExperimentType() != msgpb.Experiment_STUDY {
		return
	}
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), studyReconcilePublishTimeout)
	defer cancel()
	if err := h.publishStudyReconcile(publishCtx, exp.GetName()); err != nil {
		studyReconcileEnqueueFailuresTotal.Inc()
		logging.Error(ctx, "Failed to enqueue the initial study reconcile tick; the study was created but is not reconciled",
			"experiment_name", exp.GetName(), "error", err)
	}
}

// publishStudyReconcile publishes a protojson StudyReconcilePayload naming the experiment name.
func (h *ExperimentHandler) publishStudyReconcile(ctx context.Context, name string) error {
	data, err := protojson.Marshal(&taskpb.StudyReconcilePayload{Experiment: proto.String(name)})
	if err != nil {
		return fmt.Errorf("failed to encode study reconcile payload: %w", err)
	}
	if err := h.studyReconcilePublisher.AddMessage(ctx, string(data), "0"); err != nil {
		return fmt.Errorf("failed to publish study reconcile tick: %w", err)
	}
	return nil
}
