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
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/validator"
)

// StatusMessageRowToProto converts a Spanner Row containing StatusMessages table data into a StatusMessage proto message.
func StatusMessageRowToProto(row *spanner.Row) (*essmsgpb.StatusMessage, error) {
	msg := &essmsgpb.StructuredMessage{}

	var xid int64
	if err := row.ColumnByName("Xid", &xid); err != nil {
		return nil, fmt.Errorf("failed to read column Xid: %w", err)
	}

	var wid spanner.NullInt64
	if err := row.ColumnByName("WorkUnitId", &wid); err != nil {
		return nil, fmt.Errorf("failed to read column WorkUnitId: %w", err)
	}

	if wid.Valid {
		msg.Parent = validator.ConstructWorkUnitName(xid, wid.Int64)
	} else {
		msg.Parent = validator.ConstructExperimentName(xid)
	}

	var severity int64
	if err := row.ColumnByName("Severity", &severity); err != nil {
		return nil, fmt.Errorf("failed to read column Severity: %w", err)
	}
	msg.Severity = essmsgpb.StructuredMessage_Severity(severity)

	var source int64
	if err := row.ColumnByName("Source", &source); err != nil {
		return nil, fmt.Errorf("failed to read column Source: %w", err)
	}
	msg.Source = essmsgpb.StructuredMessage_Source(source)

	if err := row.ColumnByName("UserFacingText", &msg.UserFacingText); err != nil {
		return nil, fmt.Errorf("failed to read column UserFacingText: %w", err)
	}

	urlMsg := spanner.NullProtoMessage{ProtoMessageVal: &essmsgpb.Url{}}
	if err := row.ColumnByName("UserFacingUrl", &urlMsg); err != nil {
		return nil, fmt.Errorf("failed to read column UserFacingUrl: %w", err)
	}
	if urlMsg.Valid {
		msg.UserFacingUrl = urlMsg.ProtoMessageVal.(*essmsgpb.Url)
	}

	auxMsg := spanner.NullProtoMessage{ProtoMessageVal: &essmsgpb.AuxiliaryInfo{}}
	if err := row.ColumnByName("AuxiliaryInfo", &auxMsg); err != nil {
		return nil, fmt.Errorf("failed to read column AuxiliaryInfo: %w", err)
	}
	if auxMsg.Valid {
		msg.AuxiliaryInfo = auxMsg.ProtoMessageVal.(*essmsgpb.AuxiliaryInfo)
	}

	var createTime spanner.NullTime
	if err := row.ColumnByName("CreateTime", &createTime); err != nil {
		return nil, fmt.Errorf("failed to read column CreateTime: %w", err)
	}
	if !createTime.Valid {
		return nil, fmt.Errorf("column CreateTime is not valid")
	}
	msg.CreateTime = timestamppb.New(createTime.Time)

	var messageID int64
	if err := row.ColumnByName("MessageId", &messageID); err != nil {
		return nil, fmt.Errorf("failed to read column MessageId: %w", err)
	}

	var name string
	if wid.Valid {
		name = fmt.Sprintf("experiments/%d/workUnits/%d/statusMessages/%d", xid, wid.Int64, messageID)
	} else {
		name = fmt.Sprintf("experiments/%d/statusMessages/%d", xid, messageID)
	}

	var executionID spanner.NullInt64
	if err := row.ColumnByName("ExecutionId", &executionID); err != nil {
		return nil, fmt.Errorf("failed to read column ExecutionId: %w", err)
	}

	res := &essmsgpb.StatusMessage{
		Name:    name,
		Content: msg,
	}
	if executionID.Valid {
		res.ExecutionId = executionID.Int64
	}
	return res, nil
}

// StatusMessageProtoToInsertMutation creates a Spanner mutation to insert a StatusMessage.
func StatusMessageProtoToInsertMutation(msg *essmsgpb.StructuredMessage, messageID int64, eid *int64, previousMsg *essmsgpb.StructuredMessage) (*spanner.Mutation, error) {
	xid, wid, err := data.StatusMessageParentIDs(msg.GetParent())
	if err != nil {
		return nil, err
	}
	workUnitID := spanner.NullInt64{Int64: wid, Valid: wid != 0}

	var executionID spanner.NullInt64
	if eid != nil {
		executionID = spanner.NullInt64{Valid: true, Int64: *eid}
	}

	createTime := data.StatusMessageCreateTime(msg.GetCreateTime(), time.Now())

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
