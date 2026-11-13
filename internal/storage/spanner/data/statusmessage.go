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

	"cloud.google.com/go/spanner"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/validator"
)

// StatusMessageRowToProto converts a Spanner Row containing StatusMessages table data into a StatusMessage proto message.
func StatusMessageRowToProto(row *spanner.Row) (*essmsgpb.StatusMessage, error) {
	msg := &essmsgpb.StructuredMessage{}

	var xid, severity, source, messageID int64
	var wid, executionID spanner.NullInt64
	err := readColumns(row,
		colDest{"Xid", &xid},
		colDest{"WorkUnitId", &wid},
		colDest{"Severity", &severity},
		colDest{"Source", &source},
		colDest{"UserFacingText", &msg.UserFacingText},
		colDest{"MessageId", &messageID},
		colDest{"ExecutionId", &executionID},
	)
	if err != nil {
		return nil, err
	}
	if msg.UserFacingUrl, err = readNullProto(row, "UserFacingUrl", &essmsgpb.Url{}); err != nil {
		return nil, err
	}
	if msg.AuxiliaryInfo, err = readNullProto(row, "AuxiliaryInfo", &essmsgpb.AuxiliaryInfo{}); err != nil {
		return nil, err
	}
	if msg.CreateTime, err = readNullTimestamp(row, "CreateTime"); err != nil {
		return nil, err
	}
	if msg.CreateTime == nil {
		return nil, fmt.Errorf("column CreateTime is not valid")
	}

	msg.Severity = essmsgpb.StructuredMessage_Severity(severity)
	msg.Source = essmsgpb.StructuredMessage_Source(source)
	name := fmt.Sprintf("experiments/%d/statusMessages/%d", xid, messageID)
	msg.Parent = validator.ConstructExperimentName(xid)
	if wid.Valid {
		msg.Parent = validator.ConstructWorkUnitName(xid, wid.Int64)
		name = fmt.Sprintf("experiments/%d/workUnits/%d/statusMessages/%d", xid, wid.Int64, messageID)
	}

	res := &essmsgpb.StatusMessage{Name: name, Content: msg}
	if executionID.Valid {
		res.ExecutionId = executionID.Int64
	}
	return res, nil
}

// StatusMessageProtoToInsertMutation creates a Spanner mutation to insert a StatusMessage.
func StatusMessageProtoToInsertMutation(msg *essmsgpb.StructuredMessage, messageID int64, eid *int64, previousMsg *essmsgpb.StructuredMessage) (*spanner.Mutation, error) {
	xid, wid, err := validator.ValidateStatusMessageParent(msg.GetParent())
	if err != nil {
		return nil, fmt.Errorf("failed to validate status message parent %q: %w", msg.GetParent(), err)
	}
	workUnitID := spanner.NullInt64{Int64: wid, Valid: wid != 0}

	var executionID spanner.NullInt64
	if eid != nil {
		executionID = spanner.NullInt64{Valid: true, Int64: *eid}
	}

	createTime := time.Now()
	if msg.GetCreateTime() != nil {
		createTime = msg.GetCreateTime().AsTime()
	}

	return spanner.Insert(
		"StatusMessages",
		[]string{"Xid", "WorkUnitId", "ExecutionId", "MessageId", "Severity", "Source", "UserFacingText", "UserFacingUrl", "CreateTime", "PreviousMessage", "AuxiliaryInfo"},
		[]any{
			xid,                      // Xid
			workUnitID,               // WorkUnitId
			executionID,              // ExecutionId
			messageID,                // MessageId
			int64(msg.GetSeverity()), // Severity
			int64(msg.GetSource()),   // Source
			msg.GetUserFacingText(),  // UserFacingText
			msg.GetUserFacingUrl(),   // UserFacingUrl
			createTime,               // CreateTime
			previousMsg,              // PreviousMessage
			msg.GetAuxiliaryInfo(),   // AuxiliaryInfo
		},
	), nil
}
