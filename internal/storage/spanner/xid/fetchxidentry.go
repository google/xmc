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

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/schema/xidschema"
	spannerutils "xmanager_cloud/internal/storage/spanner/utils"
	"xmanager_cloud/internal/validator"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// FetchXidEntry retrieves a single XidEntry from storage by its unique numerical ID.
func (r *Repository) FetchXidEntry(ctx context.Context, xid int64) (*xidpb.XidEntry, error) {
	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(xid))
	return r.FetchXidEntryTransaction(ctx, xid, r.client.Single())
}

// FetchXidEntry retrieves a single XidEntry from storage within the active read-write transaction.
func (r *TxRepository) FetchXidEntry(ctx context.Context, xid int64) (*xidpb.XidEntry, error) {
	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(xid))
	return r.FetchXidEntryTransaction(ctx, xid, r.tx)
}

// UpdateXidEntry buffers an XidEntry update mutation in the active read-write transaction.
func (r *TxRepository) UpdateXidEntry(_ context.Context, xid int64, entry *xidpb.XidEntry) error {
	if r.tx == nil {
		return fmt.Errorf("spanner transaction must not be nil: %w", grpcerr.ErrFailedPrecondition)
	}
	if entry == nil {
		return fmt.Errorf("entry must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	m := spannerdata.XIDEntryToMutation(xid, entry)
	return r.tx.BufferWrite([]*spanner.Mutation{m})
}

// FetchXidEntryTransaction retrieves a single XidEntry from storage in a transaction.
func (r *Repository) FetchXidEntryTransaction(ctx context.Context, xid int64, txn spannerTransaction) (*xidpb.XidEntry, error) {
	logging.Info(ctx, "Fetch XID entry row")
	row, err := txn.ReadRow(ctx, xidschema.XIDTableName, spanner.Key{spannerutils.ReverseBits(xid)}, xidschema.FetchedXIDColumns)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, fmt.Errorf("XID %d not found: %w", xid, grpcerr.ErrNotFound)
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
