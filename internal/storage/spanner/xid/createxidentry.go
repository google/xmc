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

package xid

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	xidpb "github.com/google/xmc/api/protos/xid"
	"github.com/google/xmc/internal/logging"
	"github.com/google/xmc/internal/safesql"
	spannerdata "github.com/google/xmc/internal/storage/spanner/data"
	"github.com/google/xmc/internal/validator"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
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
func (r *Repository) CreateXidEntry(ctx context.Context, entry *xidpb.XidEntry) (*xidpb.XidEntry, error) {
	if entry == nil {
		return nil, fmt.Errorf("entry must not be nil")
	}
	// This needs to be in a transaction so we can ensure the time is consistent.
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
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
		if err := iter.Do(func(row *spanner.Row) error {
			var xid int64
			if err := row.ColumnByName("Xid", &xid); err != nil {
				return fmt.Errorf("failed to get XID from row: %v", row)
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
