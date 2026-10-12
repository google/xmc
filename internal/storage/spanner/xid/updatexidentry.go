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

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/schema/xidschema"
	"xmanager_cloud/internal/storage/spanner/utils"
	"xmanager_cloud/internal/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// UpdateXidEntry updates an XidEntry with the given update mask.
// TODO(b/517479143): Refactor UpdateXidEntry to move parent iteration and entry merging out of the repository layer.
func (r *Repository) UpdateXidEntry(ctx context.Context, update *storage.XidEntryUpdate) (*xidmsgpb.XidEntry, error) {
	if update == nil {
		return nil, fmt.Errorf("update must not be nil")
	}
	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(update.XID))
	logging.Info(ctx, "UpdateXidEntry", "update", update.XidEntry)

	var originalEntry *xidmsgpb.XidEntry
	var updatedEntry *xidmsgpb.XidEntry
	var mutated bool
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		mutated = false

		// 1. Read the existing XidEntry from storage.
		row, err := txn.ReadRow(ctx, xidschema.XIDTableName, spanner.Key{spannerutils.ReverseBits(update.XID)}, xidschema.FetchedXIDColumns)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return fmt.Errorf("XID %d not found: %w", update.XID, grpcerr.ErrNotFound)
			}

			logging.Error(ctx, "failed to read Xid row", "error", err)
			return fmt.Errorf("failed to read Xid row: %w", err)
		}

		// 1.1. Convert the Spanner row to an XidEntry proto message.
		xidEntry, err := spannerdata.XIDRowToProto(row)
		if err != nil {
			logging.Error(ctx, "failed to extract XidEntry", "error", err)
			return fmt.Errorf("failed to extract XidEntry: %w", err)
		}
		originalEntry = proto.Clone(xidEntry).(*xidmsgpb.XidEntry)

		// 2. Merge the current and new entries based on the update mask.
		updatedEntry, err = data.MergeXIDEntries(xidEntry, update.XidEntry, update.UpdateMask)
		if err != nil {
			return fmt.Errorf("failed to merge XidEntry: %w", err)
		}

		// 2.1. Check if the XidEntry has owners.
		hasOwners, err := r.xidEntryHasOwners(ctx, txn, updatedEntry)
		if err != nil {
			return err
		}

		if !hasOwners {
			return fmt.Errorf("XidEntry %s does not have owners after update: %w", updatedEntry.GetName(), grpcerr.ErrInvalidArgument)
		}

		// 3.1. Update the UpdateTime column to the current time.
		now := time.Now()
		updatedEntry.UpdateTime = timestamppb.New(now)

		// 4. Construct the Spanner mutation for the update.
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
		// 1. If the parent XID is empty, we're done.
		if parentXIDName == "" {
			break
		}

		// 1.1. Check if we've already visited this parent XID.
		if visited[parentXIDName] {
			logging.Warn(ctx, "Loop detected in XID parent chain", "xidEntryName", xidEntry.GetName())
			return false, fmt.Errorf("loop detected in XID parent chain: %w", grpcerr.ErrInternal)
		}
		visited[parentXIDName] = true

		// 2.1. Check if the parent XID is valid.
		parentXID, err := validator.ValidateXIDName(parentXIDName)
		if err != nil {
			logging.Warn(ctx, "Invalid parent XID", "parentXIDName", parentXIDName, "error", err)
			return false, fmt.Errorf("invalid parent XID %q: %w", parentXIDName, grpcerr.ErrInvalidArgument)
		}

		// 2.2. Get the parent XID from the storage layer.
		parentXIDEntry, err := r.FetchXidEntryTransaction(ctx, parentXID, txn)
		if err != nil {
			if errors.Is(err, grpcerr.ErrNotFound) {
				logging.Warn(ctx, "Parent XID not found", "parentXID", parentXID, "error", err)
				return false, fmt.Errorf("parent XID %d not found: %w", parentXID, grpcerr.ErrNotFound)
			}

			logging.Error(ctx, "Storage layer returned a non-gRPC error for GetXid()", "parentXID", parentXID, "error", err)
			return false, fmt.Errorf("an internal error occurred while retrieving the XID: %w", grpcerr.ErrInternal)
		}

		hasOwners = len(parentXIDEntry.GetAcls().GetOwners()) > 0
		parentXIDName = parentXIDEntry.GetParentXid()
	}

	return hasOwners, nil
}
