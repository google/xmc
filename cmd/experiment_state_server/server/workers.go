// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/lease"
	"xmanager_cloud/storage/storage"
)

// Start initializes and runs the background polling and coordinator workers.
func (s *Server) Start(ctx context.Context, redisClient redis.Cmdable, identity string) {
	// Start polling loop
	go s.startPollingLoop(ctx)

	// Setup Redis Leader Coordinator
	coord := lease.NewCoordinator(redisClient, "ess-draining-leader-lock", identity, 30*time.Second, 10*time.Second)

	startWorker := func(wCtx context.Context) {
		go s.startDrainingWorker(wCtx)
		go s.startStuckTaskRecoveryLoop(wCtx)
	}

	stopWorker := func() {}

	go coord.Run(ctx, startWorker, stopWorker)
}

func (s *Server) startPollingLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			paused, err := s.storage.GetSchedulingPause(ctx)
			if err != nil {
				logging.Error(ctx, "Failed to get scheduling pause from Spanner", "error", err)
				continue
			}
			oldPaused := s.localPaused.Swap(paused)
			if oldPaused != paused {
				logging.Info(ctx, "Local scheduling pause status updated", "paused", paused)
				if !paused {
					select {
					case s.drainSignal <- struct{}{}:
					default:
					}
				}
			}
		}
	}
}

func (s *Server) startDrainingWorker(ctx context.Context) {
	logging.Info(ctx, "Starting Resumption Draining worker on leader replica")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Initial check on worker startup
	if !s.localPaused.Load() {
		s.drainPausedTasks(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			logging.Info(ctx, "Stopping Resumption Draining worker")
			return
		case <-s.drainSignal:
			s.drainPausedTasks(ctx)
		case <-ticker.C:
			if !s.localPaused.Load() {
				s.drainPausedTasks(ctx)
			}
		}
	}
}

func (s *Server) drainPausedTasks(ctx context.Context) {
	limit := 100
	limiter := rate.NewLimiter(rate.Limit(10), 10) // 10 tasks/second

	for {
		if ctx.Err() != nil {
			return
		}

		if s.localPaused.Load() {
			return
		}

		// Backpressure Check: Queue Depth > 1000 tasks
		depth, err := s.taskQueueClient.GetQueueDepth(ctx)
		if err != nil {
			logging.Error(ctx, "Draining: failed to check Redis queue depth", "error", err)
			return
		}
		if depth > 1000 {
			logging.Warn(ctx, "Draining: Redis queue depth exceeds backpressure limit, halting draining", "depth", depth)
			return
		}

		tasks, err := s.storage.FetchPausedTasksBatch(ctx, limit)
		if err != nil {
			logging.Error(ctx, "Draining: failed to fetch paused tasks batch", "error", err)
			return
		}
		if len(tasks) == 0 {
			return
		}

		for _, task := range tasks {
			if ctx.Err() != nil {
				return
			}
			if s.localPaused.Load() {
				break
			}

			// Apply rate limiting
			if err := limiter.Wait(ctx); err != nil {
				logging.Error(ctx, "Draining: rate limiter wait error", "error", err)
				return
			}

			xid := reverseBits(task.BitReversedXid)
			wu, err := s.storage.FetchWorkUnit(ctx, xid, task.WorkUnitID)
			if err != nil {
				if errors.Is(err, status.ErrNotFound) {
					logging.Info(ctx, "Draining: parent work unit was deleted, dropping task", "xid", xid, "wid", task.WorkUnitID)
					_ = s.storage.CompletePausedTask(ctx, storage.PausedTaskKey{
						BitReversedXid: task.BitReversedXid,
						WorkUnitID:     task.WorkUnitID,
						TaskID:         task.TaskID,
					}, essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED)
					continue
				}
				logging.Error(ctx, "Draining: failed to fetch parent work unit", "xid", xid, "wid", task.WorkUnitID, "error", err)
				continue
			}

			// Gating Checks: If WorkUnit has transitioned to terminal or stopped state
			curState := wu.GetCurrentWorkUnitExecution().GetStatus().GetState()
			if curState == essmsgpb.WorkUnitStatus_STOPPING ||
				curState == essmsgpb.WorkUnitStatus_STOPPED ||
				curState == essmsgpb.WorkUnitStatus_COMPLETED ||
				curState == essmsgpb.WorkUnitStatus_FAILED {
				logging.Info(ctx, "Draining: parent work unit is in terminal/stopped state, dropping task", "xid", xid, "wid", task.WorkUnitID, "state", curState)
				_ = s.storage.CompletePausedTask(ctx, storage.PausedTaskKey{
					BitReversedXid: task.BitReversedXid,
					WorkUnitID:     task.WorkUnitID,
					TaskID:         task.TaskID,
				}, curState)
				continue
			}

			taskCtx := logging.InjectOperation(ctx, task.TraceID)
			err = s.taskQueueClient.EnqueueWorkUnitTask(taskCtx, task.Target, wu, task.Action, task.OperationName)
			if err != nil {
				logging.Error(ctx, "Draining: failed to enqueue task to Redis", "xid", xid, "wid", task.WorkUnitID, "error", err)
				continue
			}

			// Atomically delete paused task and update parent state to SCHEDULING
			err = s.storage.CompletePausedTask(ctx, storage.PausedTaskKey{
				BitReversedXid: task.BitReversedXid,
				WorkUnitID:     task.WorkUnitID,
				TaskID:         task.TaskID,
			}, essmsgpb.WorkUnitStatus_SCHEDULING)
			if err != nil {
				logging.Error(ctx, "Draining: failed to complete paused task in Spanner", "xid", xid, "wid", task.WorkUnitID, "error", err)
			}
		}

		if len(tasks) < limit {
			break
		}
	}
}

func (s *Server) startStuckTaskRecoveryLoop(ctx context.Context) {
	logging.Info(ctx, "Starting Stuck Task Recovery loop on leader replica")
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logging.Info(ctx, "Stopping Stuck Task Recovery loop")
			return
		case <-ticker.C:
			s.recoverStuckTasks(ctx)
		}
	}
}

func (s *Server) recoverStuckTasks(ctx context.Context) {
	paused := s.localPaused.Load()
	logging.Info(ctx, "Stuck Task Recovery: scanning for stuck tasks", "paused", paused)
	err := s.storage.ScanAndRecoverWorkUnits(ctx, paused)
	if err != nil {
		logging.Error(ctx, "Stuck Task Recovery: failed to scan and recover work units", "error", err)
	}

	if !paused {
		logging.Info(ctx, "Stuck Task Recovery: triggering drain check after scan")
		select {
		case s.drainSignal <- struct{}{}:
		default:
		}
	}
}
