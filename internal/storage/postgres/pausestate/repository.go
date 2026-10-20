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

// Package pausestate provides the PostgreSQL implementation for persisting global scheduling pause state.
package pausestate

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/storage"
)

var (
	_ storage.PauseStateRepository   = (*Repository)(nil)
	_ storage.PauseStateTxRepository = (*TxRepository)(nil)
)

// Repository persists the scheduling pause state in PostgreSQL.
type Repository struct{}

// TxRepository persists the scheduling pause state within a bound read-write transaction.
type TxRepository struct {
	*Repository
}

// NewRepository creates a PostgreSQL-backed pause state Repository.
func NewRepository() *Repository {
	return &Repository{}
}

// WithTx returns a repository bound to a read-write transaction.
func (r *Repository) WithTx() *TxRepository {
	return &TxRepository{Repository: r}
}

// Get retrieves the current global scheduling pause state.
func (r *Repository) Get(ctx context.Context) (*storage.PauseSchedulingState, error) {
	return nil, status.Error(codes.Unimplemented, "postgres pausestate repository: Get is not implemented")
}

// Get retrieves the current global scheduling pause state within the transaction.
func (r *TxRepository) Get(ctx context.Context) (*storage.PauseSchedulingState, error) {
	return nil, status.Error(codes.Unimplemented, "postgres pausestate tx repository: Get is not implemented")
}

// Set updates or initializes the global scheduling pause state within the transaction.
func (r *TxRepository) Set(ctx context.Context, state *storage.PauseSchedulingState) error {
	return status.Error(codes.Unimplemented, "postgres pausestate tx repository: Set is not implemented")
}
