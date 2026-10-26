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

package experiment

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// InsertExperiment inserts an Experiment row using the bound read-write transaction. Inserting an
// XID that already exists fails with an AlreadyExists error. The ActiveState and LaunchState
// columns are populated from exp.Status.
func (r *TxRepository) InsertExperiment(ctx context.Context, exp *pb.Experiment) error {
	if exp == nil {
		return errNilExperiment
	}
	if r.tx == nil {
		return errNilTx
	}
	xid, args, err := experimentInsertArgs(exp)
	if err != nil {
		logging.Error(ctx, "failed to create insert statement for experiment", "experiment", exp, "error", err)
		return err
	}
	_, err = r.tx.Exec(ctx, insertExperimentSQL.String(), args...)
	return pgdb.TranslateError(err, experimentEntity(xid))
}

// experimentInsertArgs converts exp into the XID and the positional arguments of
// insertExperimentSQL. Like the Spanner insert mutation, it stores the time fields of exp as
// given, nil repeated fields as SQL NULL (rejected by the NOT NULL columns) and a nil
// CompleteTime as NULL.
func experimentInsertArgs(exp *pb.Experiment) (int64, []any, error) {
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return 0, nil, fmt.Errorf("failed to validate experiment name %q: %w", exp.GetName(), err)
	}
	urls, err := marshalMessages(exp.GetUrls())
	if err != nil {
		return 0, nil, fmt.Errorf("failed to marshal experiment urls: %w", err)
	}
	resources, err := marshalMessages(exp.GetResources())
	if err != nil {
		return 0, nil, fmt.Errorf("failed to marshal experiment resources: %w", err)
	}
	status, err := proto.Marshal(exp.GetStatus())
	if err != nil {
		return 0, nil, fmt.Errorf("failed to marshal experiment status: %w", err)
	}
	return xid, []any{
		xid,                                     // Xid
		exp.GetTitle(),                          // Title
		exp.GetCreator(),                        // Creator
		int64(exp.GetPriority()),                // Priority
		urls,                                    // Urls
		exp.GetTags(),                           // Tags
		exp.GetNotes(),                          // Notes
		resources,                               // Resources
		status,                                  // Status
		int64(exp.GetStatus().GetActiveState()), // ActiveState
		int64(exp.GetStatus().GetLaunchState()), // LaunchState
		exp.GetCreateTime().AsTime(),            // CreateTime
		exp.GetUpdateTime().AsTime(),            // UpdateTime
		nullTimestamptz(exp.GetCompleteTime()),  // CompleteTime
	}, nil
}
