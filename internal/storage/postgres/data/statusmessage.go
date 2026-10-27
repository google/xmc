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

package postgresdata

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/validator"
)

// StatusMessageRow represents a row in the PostgreSQL StatusMessages table.
type StatusMessageRow struct {
	Xid             int64       `db:"xid"`
	WorkUnitID      pgtype.Int8 `db:"workunitid"`
	MessageID       int64       `db:"messageid"`
	ExecutionID     pgtype.Int8 `db:"executionid"`
	Severity        int64       `db:"severity"`
	Source          int64       `db:"source"`
	UserFacingText  string      `db:"userfacingtext"`
	UserFacingURL   []byte      `db:"userfacingurl"`
	CreateTime      time.Time   `db:"createtime"`
	PreviousMessage []byte      `db:"previousmessage"`
	AuxiliaryInfo   []byte      `db:"auxiliaryinfo"`
}

// GetInsertArgs returns the strict named arguments required by the StatusMessages INSERT statement.
func (r StatusMessageRow) GetInsertArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"xid":             r.Xid,
		"workunitid":      r.WorkUnitID,
		"messageid":       r.MessageID,
		"executionid":     r.ExecutionID,
		"severity":        r.Severity,
		"source":          r.Source,
		"userfacingtext":  r.UserFacingText,
		"userfacingurl":   r.UserFacingURL,
		"createtime":      r.CreateTime,
		"previousmessage": r.PreviousMessage,
		"auxiliaryinfo":   r.AuxiliaryInfo,
	}
}

// StatusMessageRowToProto converts a scanned PostgreSQL StatusMessageRow into an essmsgpb.StatusMessage proto.
func StatusMessageRowToProto(row StatusMessageRow) (*essmsgpb.StatusMessage, error) {
	content := &essmsgpb.StructuredMessage{
		Parent:         constructStatusMessageParent(row.Xid, row.WorkUnitID),
		Severity:       essmsgpb.StructuredMessage_Severity(row.Severity),
		Source:         essmsgpb.StructuredMessage_Source(row.Source),
		UserFacingText: row.UserFacingText,
		CreateTime:     timestamppb.New(row.CreateTime),
	}
	if err := unmarshalStatusMessagePayloads(content, row); err != nil {
		return nil, err
	}
	res := &essmsgpb.StatusMessage{
		Name:    constructStatusMessageName(row.Xid, row.WorkUnitID, row.MessageID),
		Content: content,
	}
	if row.ExecutionID.Valid {
		res.ExecutionId = row.ExecutionID.Int64
	}
	return res, nil
}

// StatusMessageProtoToRow converts a StructuredMessage proto and history metadata into a StatusMessageRow for persistence.
func StatusMessageProtoToRow(msg *essmsgpb.StructuredMessage, messageID int64, eid *int64, previousMsg *essmsgpb.StructuredMessage) (StatusMessageRow, error) {
	xid, wid, err := validator.ValidateStatusMessageParent(msg.GetParent())
	if err != nil {
		return StatusMessageRow{}, fmt.Errorf("failed to validate status message parent %q: %w", msg.GetParent(), err)
	}
	createTime := time.Now()
	if msg.GetCreateTime() != nil {
		createTime = msg.GetCreateTime().AsTime()
	}
	row := StatusMessageRow{
		Xid:            xid,
		WorkUnitID:     pgtype.Int8{Int64: wid, Valid: wid != 0},
		MessageID:      messageID,
		ExecutionID:    nullableInt8(eid),
		Severity:       int64(msg.GetSeverity()),
		Source:         int64(msg.GetSource()),
		UserFacingText: msg.GetUserFacingText(),
		CreateTime:     createTime,
	}
	if err := setStatusMessagePayloads(&row, msg, previousMsg); err != nil {
		return StatusMessageRow{}, err
	}
	return row, nil
}

func unmarshalStatusMessagePayloads(content *essmsgpb.StructuredMessage, row StatusMessageRow) error {
	if row.UserFacingURL != nil {
		urlMsg, err := UnmarshalProto[essmsgpb.Url](row.UserFacingURL)
		if err != nil {
			return fmt.Errorf("failed to unmarshal UserFacingUrl: %w", err)
		}
		content.UserFacingUrl = urlMsg
	}
	if row.AuxiliaryInfo != nil {
		auxMsg, err := UnmarshalProto[essmsgpb.AuxiliaryInfo](row.AuxiliaryInfo)
		if err != nil {
			return fmt.Errorf("failed to unmarshal AuxiliaryInfo: %w", err)
		}
		content.AuxiliaryInfo = auxMsg
	}
	return nil
}

func setStatusMessagePayloads(row *StatusMessageRow, msg, previousMsg *essmsgpb.StructuredMessage) (err error) {
	if row.UserFacingURL, err = MarshalNullableProto(msg.GetUserFacingUrl()); err != nil {
		return fmt.Errorf("failed to marshal UserFacingUrl: %w", err)
	}
	if row.PreviousMessage, err = MarshalNullableProto(previousMsg); err != nil {
		return fmt.Errorf("failed to marshal PreviousMessage: %w", err)
	}
	if row.AuxiliaryInfo, err = MarshalNullableProto(msg.GetAuxiliaryInfo()); err != nil {
		return fmt.Errorf("failed to marshal AuxiliaryInfo: %w", err)
	}
	return nil
}

func constructStatusMessageParent(xid int64, wid pgtype.Int8) string {
	if wid.Valid {
		return validator.ConstructWorkUnitName(xid, wid.Int64)
	}
	return validator.ConstructExperimentName(xid)
}

func constructStatusMessageName(xid int64, wid pgtype.Int8, messageID int64) string {
	if wid.Valid {
		return fmt.Sprintf("experiments/%d/workUnits/%d/statusMessages/%d", xid, wid.Int64, messageID)
	}
	return fmt.Sprintf("experiments/%d/statusMessages/%d", xid, messageID)
}

func nullableInt8(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}
