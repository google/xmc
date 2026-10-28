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

package xid

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	xidpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// createXidSQL inserts an Xids row and returns the Xid that the IDENTITY column assigned to it.
// Endpoint is a plain column in PostgreSQL, so it is written from OriginatingSystem.endpoint here.
var createXidSQL = safesql.New(`INSERT INTO Xids (ParentXid, Creator, OriginatingSystem, Endpoint, Acls, CreateTime, UpdateTime)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING Xid`)

// CreateXidEntry creates a new XidEntry in storage. Like the Spanner implementation, it sets the
// create and update times of entry to the current time, assigns the new name to entry, and returns
// entry. The times are truncated to the microsecond precision of TIMESTAMPTZ, so the returned
// entry reads back identically. A nil OriginatingSystem or Acls is stored as an empty message.
func (r *Repository) CreateXidEntry(ctx context.Context, entry *xidpb.XidEntry) (*xidpb.XidEntry, error) {
	if entry == nil {
		return nil, pgdb.WrapStatusError("entry must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	if r.db == nil {
		return nil, errNilPool()
	}
	parent, err := parentXID(entry)
	if err != nil {
		return nil, err
	}
	origin, err := marshalProto(entry.GetOrigin())
	if err != nil {
		return nil, fmt.Errorf("failed to marshal OriginatingSystem: %w", err)
	}
	acls, err := marshalProto(entry.GetAcls())
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Acls: %w", err)
	}

	now := time.Now().Truncate(time.Microsecond)
	entry.CreateTime = timestamppb.New(now)
	entry.UpdateTime = timestamppb.New(now)

	var xid int64
	err = r.db.QueryRow(ctx, createXidSQL.String(),
		parent, entry.GetCreator(), origin, entry.GetOrigin().GetEndpoint(), acls, now, now).Scan(&xid)
	if err != nil {
		logging.Error(ctx, "failed to execute create XID statement", "error", err)
		return nil, pgdb.TranslateError(err, entityName)
	}
	entry.Name = proto.String(validator.ConstructXIDName(xid))
	logging.Info(logging.WithXIDName(ctx, entry.GetName()), "Created XID entry row")
	return entry, nil
}
