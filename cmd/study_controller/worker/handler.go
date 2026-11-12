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

// Package worker consumes study reconcile ticks from the xmc-study-reconcile-stream Redis stream.
package worker

import (
	"context"

	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
	"xmanager_cloud/internal/validator"
)

// handleTask handles one study reconcile tick. A payload that does not decode to a valid
// experiment name can never succeed, so it is moved to the dead-letter stream without a retry.
// A valid tick is logged with the study's XID and acknowledged; reconciling the study is not
// implemented yet.
func handleTask(ctx context.Context, workerID, data string) redisclient.TaskResult {
	xid, err := parseReconcilePayload(data)
	if err != nil {
		logging.Warn(ctx, "Dropping invalid study reconcile tick", "worker_id", workerID, "payload_bytes", len(data), "error", err)
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: false, Error: err}
	}
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Received study reconcile tick", "worker_id", workerID)
	return redisclient.TaskResult{WasRunSuccessful: true}
}
