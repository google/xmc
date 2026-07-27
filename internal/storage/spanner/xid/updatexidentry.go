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
	"errors"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"googlesql/safesql/go/safesql"
	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/xidschema"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

var getXidSQL = safesql.New(`
SELECT
  *
FROM
  Xids
WHERE
  BitReversedXid = @bitReversedXid
LIMIT 1;
`)

// UpdateXidEntry updates an XidEntry with the given update mask.
func (r *Repository) UpdateXidEntry(ctx context.Context, update *storage.XidEntryUpdate) (*xidmsgpb.XidEntry, error) {
	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(update.XID))
	logging.Info(ctx, "UpdateXidEntry", "update", update.XidEntry)

	var originalEntry *xidmsgpb.XidEntry
	var updatedEntry *xidmsgpb.XidEntry
	var mutated bool
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		mutated = false

		row, err := txn.ReadRow(ctx, xidschema.XIDTableName, spanner.Key{reverseBits(update.XID)}, xidschema.FetchedXIDColumns)
		if err != nil {
			if status.ExtractCanonicalCode(err) == status.NotFound {
				return fmt.Errorf("XID %d not found: %w", update.XID, status.ErrNotFound)
			}

			logging.Error(ctx, "failed to read Xid row", "error", err)
			return fmt.Errorf("failed to read Xid row: %w", err)
		}

		xidEntry, err := spannerdata.XIDRowToProto(row)
		if err != nil {
			logging.Error(ctx, "failed to extract XidEntry", "error", err)
			return fmt.Errorf("failed to extract XidEntry: %w", err)
		}
		originalEntry = proto.Clone(xidEntry).(*xidmsgpb.XidEntry)

		updatedEntry, err = data.MergeXIDEntries(xidEntry, update.XidEntry, update.UpdateMask)
		if err != nil {
			return fmt.Errorf("failed to merge XidEntry: %w", err)
		}

		hasOwners, err := r.xidEntryHasOwners(ctx, txn, updatedEntry)
		if err != nil {
			return err
		}

		if !hasOwners {
			return fmt.Errorf("XidEntry %s does not have owners after update: %w", updatedEntry.GetName(), status.ErrInvalidArgument)
		}

		now := time.Now()
		updatedEntry.UpdateTime = timestamppb.New(now)

		m := spannerdata.XIDEntryToMutation(update.XID, updatedEntry)
		mutated = true
		txn.BufferWrite([]*spanner.Mutation{m})

		return nil
	})
	if err != nil {
		return nil, err
	}

	if mutated {
		protoDiff := cmp.Diff(originalEntry, updatedEntry, protocmp.Transform())
		logging.Info(ctx, fmt.Sprintf("XidEntry updated successfully: %s", protoDiff), "updatedXidEntry", updatedEntry)
	} else {
		logging.Info(ctx, "XidEntry not updated; no changes detected", "updatedXidEntry", updatedEntry)
	}

	return updatedEntry, nil
}

func (r *Repository) xidEntryHasOwners(ctx context.Context, txn spannerTransaction, xidEntry *xidmsgpb.XidEntry) (bool, error) {
	hasOwners := len(xidEntry.GetAcls().GetOwners()) > 0

	visited := make(map[string]bool)
	visited[xidEntry.GetName()] = true
	parentXIDName := xidEntry.GetParentXid()
	for !hasOwners {
		if parentXIDName == "" {
			break
		}

		if visited[parentXIDName] {
			logging.Warn(ctx, "Loop detected in XID parent chain", "xidEntryName", xidEntry.GetName())
			return false, fmt.Errorf("loop detected in XID parent chain: %w", status.ErrInternal)
		}
		visited[parentXIDName] = true

		parentXID, err := validator.ValidateXIDName(parentXIDName)
		if err != nil {
			logging.Warn(ctx, "Invalid parent XID", "parentXIDName", parentXIDName, "error", err)
			return false, fmt.Errorf("invalid parent XID %q: %w", parentXIDName, status.ErrInvalidArgument)
		}

		parentXIDEntry, err := r.FetchXidEntryTransaction(ctx, parentXID, txn)
		if err != nil {
			if errors.Is(err, status.ErrNotFound) {
				logging.Warn(ctx, "Parent XID not found", "parentXID", parentXID, "error", err)
				return false, fmt.Errorf("parent XID %d not found: %w", parentXID, status.ErrNotFound)
			}

			logging.Error(ctx, "Storage layer returned a non-gRPC error for GetXid()", "parentXID", parentXID, "error", err)
			return false, fmt.Errorf("an internal error occurred while retrieving the XID: %w", status.ErrInternal)
		}

		hasOwners = len(parentXIDEntry.GetAcls().GetOwners()) > 0
		parentXIDName = parentXIDEntry.GetParentXid()
	}

	return hasOwners, nil
}
