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

package resumption

import (
	"context"
	"fmt"
	"time"

	"golang/uuid/uuid"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/pausedtask"
	"xmanager_cloud/internal/storage/spanner/pausestate"
	"xmanager_cloud/logging/logging"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

// PayloadPublisher defines the interface for enqueuing actuation tasks.
type PayloadPublisher interface {
	EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error
}

// Worker defines the contract for running paused task resumption.
type Worker interface {
	StartResumption(ctx context.Context, req Request) error
}

// ResumptionWorker coordinates the continuous batch replay of paused tasks.
type ResumptionWorker struct {
	pauseStateRepo pausestate.Repository
	pausedTaskRepo pausedtask.Repository
	publisher      PayloadPublisher
}

// NewResumptionWorker creates a new ResumptionWorker with required dependencies.
func NewResumptionWorker(
	pauseStateRepo pausestate.Repository,
	pausedTaskRepo pausedtask.Repository,
	publisher PayloadPublisher,
) *ResumptionWorker {
	return &ResumptionWorker{
		pauseStateRepo: pauseStateRepo,
		pausedTaskRepo: pausedTaskRepo,
		publisher:      publisher,
	}
}

// StartResumption executes the continuous batch replay loop.
func (w *ResumptionWorker) StartResumption(ctx context.Context, req Request) error {
	resumptionWorkerID := uuid.NewString()
	registered, err := w.registerWorker(ctx, req.ResumptionRunID, resumptionWorkerID)
	if err != nil {
		return fmt.Errorf("failed to register resumption worker: %w", err)
	}
	if !registered {
		logging.Info(ctx, "Resumption worker was not registered; exiting gracefully", "resumption_worker_id", resumptionWorkerID, "resumption_run_id", req.ResumptionRunID)
		return nil
	}

	logging.Info(ctx, "Resumption worker registered successfully", "resumption_worker_id", resumptionWorkerID, "resumption_run_id", req.ResumptionRunID)
	return w.runBatchLoop(ctx, req, resumptionWorkerID)
}

func (w *ResumptionWorker) registerWorker(ctx context.Context, runID, resumptionWorkerID string) (bool, error) {
	state, err := w.pauseStateRepo.Get(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get pause state: %w", err)
	}
	if state.SchedulingPaused {
		logging.Info(ctx, "Scheduling is paused; skipping resumption execution",
			"resumption_run_id", runID,
			"resumption_worker_id", resumptionWorkerID)
		return false, nil
	}
	if state.ResumptionRunID != runID {
		logging.Info(ctx, "Resumption run ID mismatch; skipping stale or unauthorized trigger",
			"expected_run_id", state.ResumptionRunID,
			"trigger_run_id", runID,
			"resumption_worker_id", resumptionWorkerID)
		return false, nil
	}

	state.ResumptionWorkerID = resumptionWorkerID
	state.UpdatedAt = time.Now().UTC()

	// TODO(pausescheduling): Execute Get-and-Set atomically inside a Spanner ReadWriteTransaction once transactional repository support is introduced.
	if err := w.pauseStateRepo.Set(ctx, state); err != nil {
		return false, fmt.Errorf("failed to set pause state for worker registration: %w", err)
	}
	return true, nil
}

func (w *ResumptionWorker) runBatchLoop(ctx context.Context, req Request, resumptionWorkerID string) error {
	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		// TODO(pausescheduling): Check on every batch iteration if scheduling was re-paused or if this worker still claims the current resumption process (via ResumptionRunID and ResumptionWorkerID).
		// TODO(pausescheduling): Check downstream Redis backlog against req.MaxBacklogThreshold before fetching next batch.

		batch, err := w.pausedTaskRepo.ListPending(ctx, batchSize)
		if err != nil {
			return fmt.Errorf("failed to list pending paused tasks: %w", err)
		}
		if len(batch) == 0 {
			logging.Info(ctx, "No pending paused tasks remaining; resumption completed",
				"resumption_run_id", req.ResumptionRunID,
				"resumption_worker_id", resumptionWorkerID)
			return nil
		}

		if err := w.replayBatch(ctx, req.ResumptionRunID, batch); err != nil {
			return err
		}

		// TODO(pausescheduling): Extend message lease via heartbeat during batch processing.
	}
}

func (w *ResumptionWorker) replayBatch(ctx context.Context, runID string, batch []*pausedtask.PausedTask) error {
	resolutions := make([]pausedtask.TaskResolution, 0, len(batch))
	now := time.Now().UTC()

	for _, task := range batch {
		taskCtx := ctx
		if task.TraceID != "" {
			taskCtx = logging.InjectOperation(ctx, task.TraceID)
		}
		// TODO(pausescheduling): Fetch live WorkUnit from storage for filtering and payload reconstruction.
		// TODO(pausescheduling): Inject resumption_run_id and resumption_time into task metadata once proto fields are added.
		if err := w.publisher.EnqueueWorkUnitTask(taskCtx, task.Target, nil, task.Action, task.OperationName); err != nil {
			// TODO(pausescheduling): Propagate Redis publish errors from task_queue.Client.
			return fmt.Errorf("failed to enqueue work unit task %s: %w", task.TaskID, err)
		}

		resolutions = append(resolutions, pausedtask.TaskResolution{
			XID:              task.XID,
			WID:              task.WID,
			TaskID:           task.TaskID,
			ResolutionStatus: pausedtask.ResolutionStatusReplayed,
			ResumptionRunID:  runID,
			ProcessedTime:    now,
		})
	}

	if err := w.pausedTaskRepo.MarkProcessed(ctx, resolutions); err != nil {
		return fmt.Errorf("failed to mark paused tasks as processed: %w", err)
	}
	return nil
}
