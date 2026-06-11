// Copyright 2025 Google LLC
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
package spannerdata

import (
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/protobuf/v2/proto/proto"
	lropb "longrunning/operations_go_proto"
	anypb "protobuf/any_go_proto"
	statuspb "rpc/status_go_proto"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/schema/operationschema"
	"xmanager_cloud/validator/validator"
)

// OperationRowToProto converts a Spanner Row containing Operations table data into an Operation proto message.
func OperationRowToProto(row *spanner.Row) (*lropb.Operation, error) {
	var xid int64
	if err := row.ColumnByName("Xid", &xid); err != nil {
		return nil, fmt.Errorf("failed to read column Xid: %w", err)
	}

	var workUnitID int64
	if err := row.ColumnByName("WorkUnitId", &workUnitID); err != nil {
		return nil, fmt.Errorf("failed to read column WorkUnitId: %w", err)
	}

	var operationID string
	if err := row.ColumnByName("OperationId", &operationID); err != nil {
		return nil, fmt.Errorf("failed to read column OperationId: %w", err)
	}

	var done bool
	if err := row.ColumnByName("Done", &done); err != nil {
		return nil, fmt.Errorf("failed to read column Done: %w", err)
	}

	statusMsg := spanner.NullProtoMessage{ProtoMessageVal: &statuspb.Status{}}
	if err := row.ColumnByName("Status", &statusMsg); err != nil {
		return nil, fmt.Errorf("failed to read column Status: %w", err)
	}
	var status *statuspb.Status
	if statusMsg.Valid {
		status = statusMsg.ProtoMessageVal.(*statuspb.Status)
	}

	var responseBytes []byte
	if err := row.ColumnByName("Response", &responseBytes); err != nil {
		return nil, fmt.Errorf("failed to read column Response: %w", err)
	}
	var response *pb.WorkUnit
	if responseBytes != nil {
		response = &pb.WorkUnit{}
		if err := proto.Unmarshal(responseBytes, response); err != nil {
			return nil, fmt.Errorf("failed to unmarshal Response: %w", err)
		}
	}

	metadata := &pb.OperationMetadata{}
	if err := row.ColumnByName("OperationMetadata", metadata); err != nil {
		return nil, fmt.Errorf("failed to read column OperationMetadata: %w", err)
	}
	anyMetadata, err := anypb.New(metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal OperationMetadata to Any proto: %w", err)
	}

	op := &lropb.Operation{
		Name:     fmt.Sprintf("experiments/%d/workUnits/%d/operations/%s", xid, workUnitID, operationID),
		Done:     done,
		Metadata: anyMetadata,
	}

	if response != nil && status != nil {
		return nil, fmt.Errorf("operation cannot have both Response and Status set")
	}

	if response != nil {
		anyResponse, err := anypb.New(response)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Response to Any proto: %w", err)
		}
		op.Result = &lropb.Operation_Response{Response: anyResponse}
	} else if status != nil {
		op.Result = &lropb.Operation_Error{Error: status}
	}

	return op, nil
}

// OperationProtoToInsertMutation creates a Spanner mutation to insert an Operation.
func OperationProtoToInsertMutation(op *lropb.Operation, t time.Time) (*spanner.Mutation, error) {
	xid, workUnitID, operationID, err := validator.ValidateOperationName(op.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to parse operation name: %w", err)
	}

	metadata := &pb.OperationMetadata{}
	if op.GetMetadata() != nil {
		if err := op.GetMetadata().UnmarshalTo(metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	var workUnitResponse *pb.WorkUnit
	if op.GetResponse() != nil {
		workUnitResponse = &pb.WorkUnit{}
		if err := op.GetResponse().UnmarshalTo(workUnitResponse); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return spanner.Insert(
		operationschema.OperationsTable,
		operationschema.OperationColumns,
		[]any{
			xid,              // XID
			workUnitID,       // WorkUnitId
			operationID,      // OperationId
			op.Done,          // Done
			op.GetError(),    // Status
			workUnitResponse, // Response
			metadata,         // OperationMetadata
			t,                // CreateTime
			t,                // UpdateTime
		},
	), nil
}

// OperationProtoToUpdateMutation creates a Spanner mutation to update an Operation's fields.
func OperationProtoToUpdateMutation(op *lropb.Operation, t time.Time) (*spanner.Mutation, error) {
	xid, workUnitID, operationID, err := validator.ValidateOperationName(op.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to parse operation name: %w", err)
	}

	metadata := &pb.OperationMetadata{}
	if op.GetMetadata() != nil {
		if err := op.GetMetadata().UnmarshalTo(metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	var status *statuspb.Status
	if op.GetError() != nil {
		status = op.GetError()
	}

	var workUnitResponse *pb.WorkUnit
	if op.GetResponse() != nil {
		workUnitResponse = &pb.WorkUnit{}
		if err := op.GetResponse().UnmarshalTo(workUnitResponse); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return spanner.Update(
		operationschema.OperationsTable,
		operationschema.OperationUpdateColumns,
		[]any{
			data.ReverseBits(xid), // BitReversedXID
			workUnitID,            // WorkUnitId
			operationID,           // OperationId
			op.Done,               // Done
			status,                // Status
			workUnitResponse,      // Response
			metadata,              // OperationMetadata
			t,                     // UpdateTime
		},
	), nil
}
