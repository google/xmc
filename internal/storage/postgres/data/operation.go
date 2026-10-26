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

// Package postgresdata provides row structs and protobuf conversion helpers for PostgreSQL storage repositories.
package postgresdata

import (
	"fmt"
	"time"

	longrunningpb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/jackc/pgx/v5"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/validator"
)

// OperationRow represents a row in the PostgreSQL Operations table.
type OperationRow struct {
	Xid               int64     `db:"xid"`
	WorkUnitID        int64     `db:"workunitid"`
	OperationID       string    `db:"operationid"`
	Done              bool      `db:"done"`
	Status            []byte    `db:"status"`
	Response          []byte    `db:"response"`
	OperationMetadata []byte    `db:"operationmetadata"`
	CreateTime        time.Time `db:"createtime"`
	UpdateTime        time.Time `db:"updatetime"`
}

// InsertArgs returns the strict named arguments required by the Operations INSERT statement.
func (r OperationRow) InsertArgs() pgx.StrictNamedArgs {
	args := r.UpdateArgs()
	args["createtime"] = r.CreateTime
	return args
}

// UpdateArgs returns the strict named arguments required by the Operations UPDATE statement.
func (r OperationRow) UpdateArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid": r.Xid, "workunitid": r.WorkUnitID, "operationid": r.OperationID,
		"done": r.Done, "status": r.Status, "response": r.Response,
		"operationmetadata": r.OperationMetadata, "updatetime": r.UpdateTime,
	}
}

// OperationRowToProto converts a scanned PostgreSQL OperationRow into a longrunningpb.Operation proto.
func OperationRowToProto(row OperationRow) (*longrunningpb.Operation, error) {
	anyMetadata, err := UnmarshalToAny[essmsgpb.OperationMetadata](row.OperationMetadata)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal OperationMetadata: %w", err)
	}
	op := &longrunningpb.Operation{Name: validator.ConstructOperationName(row.Xid, row.WorkUnitID, row.OperationID), Done: row.Done, Metadata: anyMetadata}
	return op, setOperationResult(op, row.Status, row.Response)
}

// OperationProtoToRow converts a longrunningpb.Operation proto and timestamp into an OperationRow for persistence.
func OperationProtoToRow(op *longrunningpb.Operation, t time.Time) (OperationRow, error) {
	xid, workUnitID, operationID, err := validator.ValidateOperationName(op.GetName())
	if err != nil {
		return OperationRow{}, fmt.Errorf("failed to parse operation name: %w", err)
	}
	metadataBytes, err := MarshalAnyPayload[essmsgpb.OperationMetadata](op.GetMetadata(), false)
	if err != nil {
		return OperationRow{}, fmt.Errorf("failed to encode metadata: %w", err)
	}
	statusBytes, err := MarshalNullableProto(op.GetError())
	if err != nil {
		return OperationRow{}, fmt.Errorf("failed to marshal status: %w", err)
	}
	responseBytes, err := MarshalAnyPayload[essmsgpb.WorkUnit](op.GetResponse(), true)
	if err != nil {
		return OperationRow{}, fmt.Errorf("failed to encode response: %w", err)
	}
	return OperationRow{
		Xid: xid, WorkUnitID: workUnitID, OperationID: operationID, Done: op.GetDone(),
		Status: statusBytes, Response: responseBytes, OperationMetadata: metadataBytes, CreateTime: t, UpdateTime: t,
	}, nil
}

func setOperationResult(op *longrunningpb.Operation, statusBytes, responseBytes []byte) error {
	if responseBytes != nil && statusBytes != nil {
		return fmt.Errorf("operation cannot have both Response and Status set")
	}
	if responseBytes != nil {
		anyResponse, err := UnmarshalToAny[essmsgpb.WorkUnit](responseBytes)
		if err != nil {
			return fmt.Errorf("failed to unmarshal Response: %w", err)
		}
		op.Result = &longrunningpb.Operation_Response{Response: anyResponse}
		return nil
	}
	if statusBytes != nil {
		st, err := UnmarshalProto[statuspb.Status](statusBytes)
		if err != nil {
			return fmt.Errorf("failed to unmarshal Status: %w", err)
		}
		op.Result = &longrunningpb.Operation_Error{Error: st}
	}
	return nil
}
