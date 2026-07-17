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
package spanner

import (
	"context"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"googlesql/safesql/go/safesql"
	timestamppb "protobuf/timestamp_go_proto"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// We cannot use Mutations here, because we need to return the XID. Mutations do not support THEN
// RETURN.
var createXidSQL = safesql.New(`
INSERT INTO Xids (
  ParentXid,
	Creator,
	OriginatingSystem,
	Acls,
  CreateTime,
  UpdateTime
)
VALUES (
	@ParentXid,
	@Creator,
	@OriginatingSystem,
	@Acls,
	@CreateTime,
	@UpdateTime
)
THEN RETURN Xid
`)

// CreateXidEntry creates a new XidEntry in storage.
func (s *Storage) CreateXidEntry(ctx context.Context, entry *xidpb.XidEntry) (*xidpb.XidEntry, error) {
	// This needs to be in a transaction so we can ensure the time is consistent.
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		now := timestamppb.New(time.Now())
		entry.CreateTime = now
		entry.UpdateTime = now

		params, err := spannerdata.XIDProtoToParamsMap(entry)
		if err != nil {
			return err
		}

		stmt := spanner.Statement{
			SQL:    createXidSQL.String(),
			Params: params,
		}

		iter := tx.Query(ctx, stmt)
		if err := iter.Do(func(r *spanner.Row) error {
			var xid int64
			if err := r.ColumnByName("Xid", &xid); err != nil {
				return fmt.Errorf("failed to get XID from row: %v", r)
			}

			name := validator.ConstructXIDName(xid)
			entry.Name = &name

			return nil
		}); err != nil {
			logging.Error(ctx, "failed to execute create XID statement", "error", err)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return entry, nil
}
