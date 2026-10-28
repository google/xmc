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

package artifactlifecycle

import (
	"context"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
)

// isExecutionSuperseded implements Shared-Artifact Execution Fencing: it reports
// whether the work unit's current execution is no longer eventEID, in which case
// shared-artifact writes driven by the (stale) event must be skipped. Callers must
// read wu inside the read-write transaction that performs the guarded writes.
func isExecutionSuperseded(ctx context.Context, wu *essmsgpb.WorkUnit, eventEID int64) bool {
	cur := wu.GetCurrentWorkUnitExecution()
	if cur == nil {
		return false
	}
	_, _, curEID, err := validator.ValidateWorkUnitExecutionName(cur.GetName())
	if err != nil || curEID == eventEID {
		return false
	}
	logging.Info(ctx, "Skipping artifact write; execution is superseded", "eventEID", eventEID, "curEID", curEID)
	return true
}
