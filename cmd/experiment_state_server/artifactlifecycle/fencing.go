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
	"fmt"

	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// isExecutionSupersededInTx implements Shared-Artifact Execution Fencing: it reports
// whether the work unit's current execution has advanced past eventEID, in which case
// writes driven by the (stale) event must be skipped.
func isExecutionSupersededInTx(ctx context.Context, tx *storage.Transaction, xid, wid, eventEID int64) (bool, error) {
	wu, err := tx.WorkUnits.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		return false, fmt.Errorf("failed to fetch work unit %d: %w", wid, err)
	}
	cur := wu.GetCurrentWorkUnitExecution()
	if cur == nil {
		return false, nil
	}
	_, _, curEID, err := validator.ValidateWorkUnitExecutionName(cur.GetName())
	if err != nil || curEID == eventEID {
		return false, nil
	}
	logging.Info(ctx, "Skipping artifact write; execution is superseded", "eventEID", eventEID, "curEID", curEID)
	return true, nil
}
