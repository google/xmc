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

package operation

import (
	"context"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/godebug/diff/diff"
	"golang/protobuf/v2/encoding/prototext/prototext"
	"golang/protobuf/v2/proto/proto"
	longrunningpb "longrunning/operations_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
)

// UpdateWorkUnitOperation updates the status of an operation.
// TODO(b/517479143): Move operation update merging logic out of the repository layer into a domain service layer.
func (r *Repository) UpdateWorkUnitOperation(ctx context.Context, operationUpdate *storage.OperationUpdate) (*longrunningpb.Operation, error) {
	if operationUpdate == nil {
		return nil, fmt.Errorf("operationUpdate must not be nil")
	}
	ctx = logging.WithOperationName(ctx, validator.ConstructOperationName(operationUpdate.XID, operationUpdate.WID, operationUpdate.OID))
	logging.Info(ctx, "Update work unit operation", "update", operationUpdate)

	var originalOperation *longrunningpb.Operation
	var updatedOperation *longrunningpb.Operation
	var mutated bool
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		mutated = false

		operation, err := r.FetchWorkUnitOperationTransaction(ctx, operationUpdate.XID, operationUpdate.WID, operationUpdate.OID, txn)
		if err != nil {
			return err
		}
		originalOperation = proto.Clone(operation).(*longrunningpb.Operation)

		updatedOperation, err = data.MergeOperations(operation, operationUpdate.Operation, operationUpdate.UpdateMask)
		if err != nil {
			return fmt.Errorf("failed to merge Operation: %w", err)
		}

		if proto.Equal(operation, updatedOperation) {
			return nil
		}

		mutation, err := spannerdata.OperationProtoToUpdateMutation(updatedOperation, time.Now())
		if err != nil {
			return fmt.Errorf("failed to create Spanner mutation for Operation: %w", err)
		}

		mutated = true
		return txn.BufferWrite([]*spanner.Mutation{mutation})
	})
	if err != nil {
		return nil, err
	}

	if mutated {
		protoDiff := diff.Diff(prototext.Format(originalOperation), prototext.Format(updatedOperation))
		logging.Info(ctx, fmt.Sprintf("Updated work unit operation: %s", protoDiff), "updatedOperation", updatedOperation)
	} else {
		logging.Debug(ctx, "Work unit operation not updated; no changes detected", "updatedOperation", updatedOperation)
	}

	return updatedOperation, nil
}
