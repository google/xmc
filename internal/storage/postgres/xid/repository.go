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

// Package xid contains the concrete PostgreSQL repository implementation for XID entries.
package xid

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/storage"
)

var (
	_ storage.XidEntryStore   = (*Repository)(nil)
	_ storage.XidTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for XID entry entities in PostgreSQL.
type Repository struct{}

// TxRepository manages transactional persistence for XID entry entities in PostgreSQL.
type TxRepository struct {
	*Repository
}

// NewRepository creates a new PostgreSQL Xid repository instance.
func NewRepository() *Repository {
	return &Repository{}
}

// WithTx returns a wrapper bound to a read-write transaction.
func (r *Repository) WithTx() *TxRepository {
	return &TxRepository{Repository: r}
}

// CreateXidEntry creates a new XidEntry.
func (r *Repository) CreateXidEntry(ctx context.Context, entry *xidmsgpb.XidEntry) (*xidmsgpb.XidEntry, error) {
	return nil, status.Error(codes.Unimplemented, "postgres xid store: CreateXidEntry is not implemented")
}

// FetchXidEntry retrieves an XidEntry by its unique ID.
func (r *Repository) FetchXidEntry(ctx context.Context, xid int64) (*xidmsgpb.XidEntry, error) {
	return nil, status.Error(codes.Unimplemented, "postgres xid store: FetchXidEntry is not implemented")
}

// ListXidEntries retrieves a paginated and filtered list of XidEntries.
func (r *Repository) ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*xidmsgpb.XidEntry, string, error) {
	return nil, "", status.Error(codes.Unimplemented, "postgres xid store: ListXidEntries is not implemented")
}

// FetchXidEntry retrieves an XidEntry by its unique ID within the transaction.
func (r *TxRepository) FetchXidEntry(ctx context.Context, xid int64) (*xidmsgpb.XidEntry, error) {
	return nil, status.Error(codes.Unimplemented, "postgres xid tx repository: FetchXidEntry is not implemented")
}

// UpdateXidEntry persists an updated XidEntry within the transaction.
func (r *TxRepository) UpdateXidEntry(ctx context.Context, xid int64, entry *xidmsgpb.XidEntry) error {
	return status.Error(codes.Unimplemented, "postgres xid tx repository: UpdateXidEntry is not implemented")
}
