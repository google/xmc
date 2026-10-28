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
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/validator"
)

// XidRow represents a row in the PostgreSQL Xids table.
type XidRow struct {
	Xid               int64     `db:"xid"`
	ParentXid         int64     `db:"parentxid"`
	Creator           string    `db:"creator"`
	OriginatingSystem []byte    `db:"originatingsystem"`
	Endpoint          string    `db:"endpoint"`
	Acls              []byte    `db:"acls"`
	CreateTime        time.Time `db:"createtime"`
	UpdateTime        time.Time `db:"updatetime"`
}

// GetInsertArgs returns the strict named arguments required by the Xids INSERT statement.
func (r XidRow) GetInsertArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"parentxid": r.ParentXid, "creator": r.Creator, "originatingsystem": r.OriginatingSystem,
		"endpoint": r.Endpoint, "acls": r.Acls, "createtime": r.CreateTime, "updatetime": r.UpdateTime,
	}
}

// GetUpdateArgs returns the strict named arguments required by the Xids UPDATE statement.
func (r XidRow) GetUpdateArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{"xid": r.Xid, "acls": r.Acls, "updatetime": r.UpdateTime}
}

// XidRowToProto converts a scanned PostgreSQL XidRow into an xidmsgpb.XidEntry proto.
func XidRowToProto(row XidRow) (*xidmsgpb.XidEntry, error) {
	origin, err := UnmarshalProto[xidmsgpb.OriginatingSystem](row.OriginatingSystem)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal OriginatingSystem: %w", err)
	}
	acls, err := UnmarshalProto[xidmsgpb.XidAcls](row.Acls)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Acls: %w", err)
	}
	entry := &xidmsgpb.XidEntry{
		Name:       proto.String(validator.ConstructXIDName(row.Xid)),
		Creator:    proto.String(row.Creator),
		Origin:     origin,
		Acls:       acls,
		CreateTime: timestamppb.New(row.CreateTime),
		UpdateTime: timestamppb.New(row.UpdateTime),
	}
	if row.ParentXid != 0 {
		entry.ParentXid = proto.String(validator.ConstructXIDName(row.ParentXid))
	}
	return entry, nil
}

// XidProtoToRow converts an xidmsgpb.XidEntry proto into an XidRow for persistence.
func XidProtoToRow(entry *xidmsgpb.XidEntry) (XidRow, error) {
	xid, err := parseOptionalXID(entry.GetName())
	if err != nil {
		return XidRow{}, fmt.Errorf("failed to parse XID: %w", err)
	}
	parentXID, err := parseOptionalXID(entry.GetParentXid())
	if err != nil {
		return XidRow{}, fmt.Errorf("failed to parse parent XID: %w", err)
	}
	originBytes, err := MarshalProto(entry.GetOrigin())
	if err != nil {
		return XidRow{}, fmt.Errorf("failed to marshal OriginatingSystem: %w", err)
	}
	aclsBytes, err := MarshalProto(entry.GetAcls())
	if err != nil {
		return XidRow{}, fmt.Errorf("failed to marshal Acls: %w", err)
	}
	return XidRow{
		Xid: xid, ParentXid: parentXID, Creator: entry.GetCreator(),
		OriginatingSystem: originBytes, Endpoint: entry.GetOrigin().GetEndpoint(), Acls: aclsBytes,
		CreateTime: entry.GetCreateTime().AsTime(), UpdateTime: entry.GetUpdateTime().AsTime(),
	}, nil
}

func parseOptionalXID(name string) (int64, error) {
	if name == "" {
		return 0, nil
	}
	return validator.ValidateXIDName(name)
}
