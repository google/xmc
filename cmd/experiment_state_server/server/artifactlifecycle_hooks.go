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

package server

import (
	"context"

	"xmanager_cloud/cmd/experiment_state_server/artifactlifecycle"
	"xmanager_cloud/internal/logging"
	client "xmanager_cloud/internal/task_queue"
	"xmanager_cloud/internal/validator"
)

// requiredNotifyWhen returns the notify-when annotation value required by the
// artifact lifecycle rules matching the work unit's artifacts, or "" when none apply.
// Listing failures are logged and yield "" so work unit scheduling is never blocked.
func (h *baseHandler) requiredNotifyWhen(ctx context.Context, xid, wid int64) string {
	if len(h.artifactLifecycle.Rules()) == 0 {
		return ""
	}
	arts, err := artifactlifecycle.ListWorkUnitArtifacts(ctx, h.storage, xid, wid)
	if err != nil {
		logging.Warn(ctx, "Failed to list artifacts for notify-when computation", "xid", xid, "wid", wid, "error", err)
		return ""
	}
	return h.artifactLifecycle.RequiredNotifyWhenAnnotation(arts)
}

// syncWorkUnitNotifyWhen computes the required notify-when annotation for the work unit
// and asynchronously dispatches an UpdateAction to the Actuator to stamp it on the live JobSet.
func (h *ArtifactHandler) syncWorkUnitNotifyWhen(ctx context.Context, xid, wid int64) {
	notifyWhen := h.requiredNotifyWhen(ctx, xid, wid)
	if notifyWhen == "" {
		return
	}
	wu, err := h.storage.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		logging.Warn(ctx, "Failed to fetch work unit for notify-when synchronization", "xid", xid, "wid", wid, "error", err)
		return
	}
	cur := wu.GetCurrentWorkUnitExecution()
	if cur == nil {
		return
	}
	if err := h.actuator.EnqueueWorkUnitTask(ctx, cur.GetName(), wu, client.UpdateNotifyWhenAction(notifyWhen), ""); err != nil {
		logging.Warn(ctx, "Failed to enqueue notify-when update task", "xid", xid, "wid", wid, "error", err)
	}
}

// finalizeSupersededExecution runs the artifact lifecycle cleanup for the execution
// replaced by a restart. Failures are logged; they never fail the restart.
func (h *WorkUnitHandler) finalizeSupersededExecution(ctx context.Context, xid, wid int64, oldExecutionName string) {
	if len(h.artifactLifecycle.Rules()) == 0 {
		return
	}
	_, _, oldEID, err := validator.ValidateWorkUnitExecutionName(oldExecutionName)
	if err != nil {
		logging.Warn(ctx, "Skipping superseded execution finalization; invalid execution name", "execution", oldExecutionName, "error", err)
		return
	}
	if err := h.artifactLifecycle.HandleExecutionSuperseded(ctx, h.storage, xid, wid, oldEID); err != nil {
		logging.Warn(ctx, "Failed to finalize superseded execution artifact lifecycle", "xid", xid, "wid", wid, "oldEID", oldEID, "error", err)
	}
}
