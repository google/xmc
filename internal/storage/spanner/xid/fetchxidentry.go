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
	xidpb "github.com/google/xmc/api/protos/xid"
	"github.com/google/xmc/internal/grpcerr"
	"github.com/google/xmc/internal/logging"
	"github.com/google/xmc/internal/storage/spanner/data"
	"github.com/google/xmc/internal/storage/spanner/schema/xidschema"
	"github.com/google/xmc/internal/storage/spanner/utils"
	"github.com/google/xmc/internal/validator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FetchXidEntry retrieves a single XidEntry from storage by its unique numerical ID.
func (r *Repository) FetchXidEntry(ctx context.Context, xid int64) (*xidpb.XidEntry, error) {
	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(xid))
	return r.FetchXidEntryTransaction(ctx, xid, r.client.Single())
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
