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

// Package resumption implements the ESS resumption stream handling and stub worker.
package resumption

import (
	"context"
	"fmt"

	"xmanager_cloud/logging/logging"
	"xmanager_cloud/task_queue_redis/redisclient"
)

// Handler processes resumption triggers from the Redis resume stream.
type Handler struct {
	worker Worker
}

// NewHandler creates a new resumption task handler.
func NewHandler(worker Worker) (*Handler, error) {
	if worker == nil {
		return nil, fmt.Errorf("resumption worker cannot be nil")
	}
	return &Handler{worker: worker}, nil
}

// HandleTask parses the trigger payload and dispatches it to the resumption worker.
func (h *Handler) HandleTask(ctx context.Context, workerID, data string) redisclient.TaskResult {
	req, err := ParseTriggerPayload(data)
	if err != nil {
		logging.Error(ctx, "Failed to parse resumption trigger payload", "error", err, "data", data)
		return redisclient.TaskResult{
			WasRunSuccessful: false,
			ShouldBeRetried:  false,
			Error:            err,
		}
	}

	if err := h.worker.Resume(ctx, req); err != nil {
		logging.Error(ctx, "Resumption worker execution failed", "error", err, "resumption_run_id", req.ResumptionRunID)
		return redisclient.TaskResult{
			WasRunSuccessful: false,
			ShouldBeRetried:  true,
			Error:            err,
		}
	}

	logging.Info(ctx, "Resumption trigger processed successfully", "resumption_run_id", req.ResumptionRunID)
	return redisclient.TaskResult{
		WasRunSuccessful: true,
		ShouldBeRetried:  false,
		Error:            nil,
	}
}
