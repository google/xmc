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
	"errors"
	"fmt"
	"time"

	"golang/uuid/uuid"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/pausedtask"
	"xmanager_cloud/internal/storage/spanner/pausestate"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

const (
	// defaultThrottlePauseDuration is the pause interval when downstream queue backlog exceeds threshold.
	defaultThrottlePauseDuration = 10 * time.Second
)

// TaskQueue defines the task queue operations used by the resumption worker:
// enqueuing actuation tasks and checking the downstream backlog.
type TaskQueue interface {
	EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error
	// CalculateBacklogLength returns the number of tasks in the downstream task queue not yet
	// processed by the given consumer group. The count is exact below maxThreshold and capped
	// just above it.
	CalculateBacklogLength(ctx context.Context, group string, maxThreshold int) (int64, error)
}

// Worker defines the contract for running paused task resumption.
type Worker interface {
	StartResumption(ctx context.Context, req Request) error
}

// ResumptionWorker coordinates the continuous batch replay of paused tasks.
type ResumptionWorker struct {
	pauseStateRepo        pausestate.Repository
	pausedTaskRepo        pausedtask.Repository
	taskQueue             TaskQueue
	throttlePauseDuration time.Duration
}

// NewResumptionWorker creates a new ResumptionWorker with required dependencies.
func NewResumptionWorker(
	pauseStateRepo pausestate.Repository,
	pausedTaskRepo pausedtask.Repository,
	taskQueue TaskQueue,
) *ResumptionWorker {
	return &ResumptionWorker{
		pauseStateRepo:        pauseStateRepo,
		pausedTaskRepo:        pausedTaskRepo,
		taskQueue:             taskQueue,
		throttlePauseDuration: defaultThrottlePauseDuration,
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

		stop, err := w.shouldStopReplay(ctx, req.ResumptionRunID, resumptionWorkerID)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}

		throttled, err := w.throttleIfBusy(ctx, req)
		if err != nil {
			return err
		}
		if throttled {
			continue
		}

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

func (w *ResumptionWorker) throttleIfBusy(ctx context.Context, req Request) (bool, error) {
	maxThreshold := req.MaxBacklogThreshold
	if maxThreshold <= 0 {
		maxThreshold = defaultMaxBacklog
	}

	backlog, err := w.taskQueue.CalculateBacklogLength(ctx, constants.RedisGroup, maxThreshold)
	if err != nil {
		return false, fmt.Errorf("failed to calculate task queue backlog: %w", err)
	}

	if backlog < int64(maxThreshold) {
		return false, nil
	}

	logging.Info(ctx, "Tasks queue backlog exceeded threshold; pausing replay",
		"backlog", backlog,
		"max_backlog_threshold", maxThreshold,
		"pause_duration", w.pauseDuration(),
		"resumption_run_id", req.ResumptionRunID)

	// TODO(pausescheduling): Call ExtendClaim once lease extension is implemented.

	if err := w.waitOrCancel(ctx); err != nil {
		return false, err
	}

	return true, nil
}

// pauseDuration returns the configured throttle pause, falling back to the default.
func (w *ResumptionWorker) pauseDuration() time.Duration {
	if w.throttlePauseDuration <= 0 {
		return defaultThrottlePauseDuration
	}
	return w.throttlePauseDuration
}

// waitOrCancel blocks the calling goroutine for the worker's pause duration,
// returning early with ctx.Err() if the context is cancelled first.
func (w *ResumptionWorker) waitOrCancel(ctx context.Context) error {
	timer := time.NewTimer(w.pauseDuration())
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (w *ResumptionWorker) shouldStopReplay(ctx context.Context, runID, resumptionWorkerID string) (bool, error) {
	state, err := w.pauseStateRepo.Get(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get pause state during batch iteration: %w", err)
	}
	if state.SchedulingPaused {
		logging.Info(ctx, "Scheduling re-paused mid-resumption; halting replay loop",
			"resumption_run_id", runID,
			"resumption_worker_id", resumptionWorkerID)
		return true, nil
	}
	if state.ResumptionRunID != runID {
		logging.Info(ctx, "Resumption run ID changed mid-resumption; halting replay loop",
			"expected_run_id", runID,
			"current_run_id", state.ResumptionRunID,
			"resumption_worker_id", resumptionWorkerID)
		return true, nil
	}
	if state.ResumptionWorkerID != resumptionWorkerID {
		logging.Info(ctx, "Resumption worker fenced; another worker claimed the resumption process",
			"resumption_run_id", runID,
			"current_worker_id", state.ResumptionWorkerID,
			"resumption_worker_id", resumptionWorkerID)
		return true, nil
	}
	return false, nil
}

func (w *ResumptionWorker) replayBatch(ctx context.Context, runID string, batch []*pausedtask.PausedTask) error {
	resolutions := make([]pausedtask.TaskResolution, 0, len(batch))
	now := time.Now().UTC()

	var publishErr error
	for _, task := range batch {
		if task == nil {
			publishErr = errors.New("encountered nil task in batch")
			break
		}
		if task.WorkUnit == nil || task.WorkUnit.GetCurrentWorkUnitExecution() == nil {
			publishErr = fmt.Errorf("task %s missing required parent work unit or execution", task.TaskID)
			break
		}

		taskCtx := ctx
		if task.TraceID != "" {
			taskCtx = logging.InjectOperation(ctx, task.TraceID)
		}

		if skip, reason := ShouldSkipTask(task); skip {
			logging.Info(taskCtx, "Skipping obsolete or terminal paused task during resumption replay",
				"task_id", task.TaskID,
				"xid", task.XID,
				"wid", task.WID,
				"reason", string(reason))
			resolutions = append(resolutions, pausedtask.TaskResolution{
				XID:              task.XID,
				WID:              task.WID,
				TaskID:           task.TaskID,
				ResolutionStatus: pausedtask.ResolutionStatusSkipped,
				ResumptionRunID:  runID,
				ProcessedTime:    now,
			})
			continue
		}

		// TODO(pausescheduling): Inject resumption_run_id and resumption_time into task metadata once proto fields are added.
		if err := w.taskQueue.EnqueueWorkUnitTask(taskCtx, task.Target, task.WorkUnit, task.Action, task.OperationName); err != nil {
			publishErr = fmt.Errorf("failed to enqueue work unit task %s: %w", task.TaskID, err)
			break
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

	if len(resolutions) > 0 {
		if err := w.pausedTaskRepo.MarkProcessed(ctx, resolutions); err != nil {
			return errors.Join(publishErr, fmt.Errorf("failed to mark paused tasks as processed: %w", err))
		}
	}
	return publishErr
}
