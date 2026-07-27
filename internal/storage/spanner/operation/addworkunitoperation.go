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
	"golang/uuid/uuid"
	longrunningpb "longrunning/operations_go_proto"
	anypb "protobuf/any_go_proto"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/validator/validator"
)

// AddWorkUnitOperation adds an operation to a work unit.
func (r *Repository) AddWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationMetadata *essmsgpb.OperationMetadata) (*longrunningpb.Operation, error) {
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	logging.Info(ctx, "Add work unit operation to storage", "operationMetadata", operationMetadata)

	var operation *longrunningpb.Operation
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		now := time.Now()
		operationMetadata.CreateTime = timestamppb.New(now)
		anyMetadata, err := anypb.New(operationMetadata)
		if err != nil {
			return fmt.Errorf("failed to marshal operation metadata to Any: %w", err)
		}

		operation = &longrunningpb.Operation{
			Name:     validator.ConstructOperationName(xid, wid, uuid.New()),
			Metadata: anyMetadata,
			Done:     false,
		}

		mutation, err := spannerdata.OperationProtoToInsertMutation(operation, now)
		if err != nil {
			return err
		}

		err = tx.BufferWrite([]*spanner.Mutation{mutation})
		if err != nil {
			logging.Error(ctx, "Failed to buffer write mutation for add work unit operation", "error", err)
			return fmt.Errorf("failed to buffer write for add work unit operation: %w", err)
		}

		return nil
	})
	if err != nil {
		logging.Error(ctx, "Failed to add work unit operation", "error", err)
		return nil, fmt.Errorf("failed to add work unit operation: %w", err)
	}

	logging.Info(ctx, "Added work unit operation to storage", "operation", operation)

	return operation, nil
}
