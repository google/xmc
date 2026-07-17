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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"task/go/status"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/xidschema"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// FetchXidEntry retrieves a single XidEntry from storage by its unique numerical ID.
func (s *Storage) FetchXidEntry(ctx context.Context, xid int64) (*xidpb.XidEntry, error) {
	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(xid))
	return s.fetchXidEntryTransaction(ctx, xid, s.client.Single())
}

func (s *Storage) fetchXidEntryTransaction(ctx context.Context, xid int64, txn spannerTransaction) (*xidpb.XidEntry, error) {
	logging.Info(ctx, "Fetch XID entry row")
	row, err := txn.ReadRow(ctx, xidschema.XIDTableName, spanner.Key{reverseBits(xid)}, xidschema.FetchedXIDColumns)
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			return nil, fmt.Errorf("XID %d not found: %w", xid, status.ErrNotFound)
		}

		logging.Error(ctx, "Failed to read Xid row", "error", err)
		return nil, fmt.Errorf("failed to read Xid row: %w", err)
	}

	xidEntry, err := spannerdata.XIDRowToProto(row)
	if err != nil {
		logging.Error(ctx, "Failed to extract XidEntry", "error", err)
		return nil, fmt.Errorf("failed to extract XidEntry: %w", err)
	}

	logging.Info(ctx, "Fetched XID entry from storage", "xid_entry", xidEntry)
	return xidEntry, nil
}
