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

// Package xid provides XID ancestor chain traversal and UpdateXidEntry transaction orchestration.
package xid

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// AncestorFetcher retrieves an XidEntry by its numerical ID.
type AncestorFetcher func(ctx context.Context, xid int64) (*xidmsgpb.XidEntry, error)

// AncestorPredicate evaluates an XidEntry in an ancestor chain.
// Returning matched=true stops the walk early and returns true.
type AncestorPredicate func(ctx context.Context, entry *xidmsgpb.XidEntry) (matched bool, err error)

// UpdateXidEntry updates an XidEntry with the given update mask.
func UpdateXidEntry(ctx context.Context, runner storage.TransactionRunner, update *storage.XidEntryUpdate) (*xidmsgpb.XidEntry, error) {
	if runner == nil {
		return nil, fmt.Errorf("transaction runner must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if update == nil {
		return nil, fmt.Errorf("update must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(update.XID))
	logging.Info(ctx, "UpdateXidEntry", "update", update.XidEntry)

	var originalEntry *xidmsgpb.XidEntry
	var updatedEntry *xidmsgpb.XidEntry
	var mutated bool
	err := runner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		originalEntry = nil
		updatedEntry = nil
		mutated = false
		if tx == nil || tx.XIDs == nil {
			return fmt.Errorf("transaction XID repository is not initialized: %w", grpcerr.ErrInternal)
		}

		// 1. Read the existing XidEntry from storage.
		xidEntry, err := tx.XIDs.FetchXidEntry(ctx, update.XID)
		if err != nil {
			return err
		}
		originalEntry = proto.Clone(xidEntry).(*xidmsgpb.XidEntry)

		// 2. Merge the current and new entries based on the update mask.
		updatedEntry, err = data.MergeXIDEntries(xidEntry, update.XidEntry, update.UpdateMask)
		if err != nil {
			return fmt.Errorf("failed to merge XidEntry: %w", err)
		}

		// 2.1. Check if the XidEntry has owners.
		hasOwners, err := xidEntryHasOwners(ctx, tx.XIDs.FetchXidEntry, updatedEntry)
		if err != nil {
			return err
		}

		if !hasOwners {
			return fmt.Errorf("XidEntry %s does not have owners after update: %w", updatedEntry.GetName(), grpcerr.ErrInvalidArgument)
		}

		// 3.1. Update the UpdateTime column to the current time.
		now := time.Now()
		updatedEntry.UpdateTime = timestamppb.New(now)

		// 4. Persist the updated XidEntry in the transaction.
		if err := tx.XIDs.UpdateXidEntry(ctx, update.XID, updatedEntry); err != nil {
			return err
		}
		mutated = true

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

func xidEntryHasOwners(ctx context.Context, fetch AncestorFetcher, xidEntry *xidmsgpb.XidEntry) (bool, error) {
	return WalkAncestors(ctx, xidEntry, fetch, func(_ context.Context, entry *xidmsgpb.XidEntry) (bool, error) {
		return len(entry.GetAcls().GetOwners()) > 0, nil
	})
}

// WalkAncestors walks the XID ancestor chain starting at xidEntry until predicate returns true,
// the root entry (empty parent_xid) is reached, or an error occurs.
func WalkAncestors(ctx context.Context, xidEntry *xidmsgpb.XidEntry, fetch AncestorFetcher, predicate AncestorPredicate) (bool, error) {
	if xidEntry == nil {
		return false, fmt.Errorf("xidEntry must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	hasOwners, err := predicate(ctx, xidEntry)
	if err != nil {
		return false, err
	}

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
		parentXIDEntry, err := fetch(ctx, parentXID)
		if err != nil {
			if errors.Is(err, grpcerr.ErrNotFound) {
				logging.Warn(ctx, "Parent XID not found", "parentXID", parentXID, "error", err)
				return false, fmt.Errorf("parent XID %d not found: %w", parentXID, grpcerr.ErrNotFound)
			}

			logging.Error(ctx, "Storage layer returned a non-gRPC error for FetchXidEntry", "parentXID", parentXID, "error", err)
			return false, fmt.Errorf("an internal error occurred while retrieving the XID: %w", grpcerr.ErrInternal)
		}

		hasOwners, err = predicate(ctx, parentXIDEntry)
		if err != nil {
			return false, err
		}
		parentXIDName = parentXIDEntry.GetParentXid()
	}

	return hasOwners, nil
}
