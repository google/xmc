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
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/hypercomputeclient"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// recordExecutionMLRunURLInTx records mlRunURL on metadata.mlrun_url of cur, the
// work unit's current execution read inside tx, whose id is eid. It only rewrites
// the MLRun URL and the update time; every other field is written back unchanged
// from the same transaction's read. Archived executions are never rewritten here:
// the restart path archives the current execution row, including its mlrun_url.
func recordExecutionMLRunURLInTx(ctx context.Context, tx *storage.Transaction, xid, wid, eid int64, cur *essmsgpb.WorkUnitExecution, mlRunURL string) error {
	if cur == nil || cur.GetMetadata().GetMlrunUrl() == mlRunURL {
		return nil
	}
	updated := proto.Clone(cur).(*essmsgpb.WorkUnitExecution)
	if updated.Metadata == nil {
		updated.Metadata = &essmsgpb.WorkUnitExecutionMetadata{}
	}
	updated.Metadata.MlrunUrl = mlRunURL
	updated.UpdateTime = timestamppb.New(time.Now())
	if err := tx.WorkUnits.UpdateCurrentWorkUnitExecution(ctx, xid, wid, eid, updated); err != nil {
		return fmt.Errorf("record mlrun_url on execution %d of work unit %d: %w", eid, wid, err)
	}
	return nil
}

// executionMLRunID returns the MLRun id recorded on metadata.mlrun_url of execution
// eid (current or archived), or "" when that execution has no recorded MLRun.
func executionMLRunID(ctx context.Context, store storage.Storage, xid, wid, eid int64) (string, error) {
	execs, err := store.ListWorkUnitExecutions(ctx, xid, wid)
	if err != nil {
		return "", fmt.Errorf("list executions of work unit %d: %w", wid, err)
	}
	for _, exec := range execs {
		_, _, execEID, err := validator.ValidateWorkUnitExecutionName(exec.GetName())
		if err != nil || execEID != eid {
			continue
		}
		return hypercomputeclient.ParseMLRunIDFromURL(exec.GetMetadata().GetMlrunUrl()), nil
	}
	return "", nil
}
