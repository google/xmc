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

// Package operation contains the concrete Spanner repository implementation for operations.
package operation

import (
	"context"
	"fmt"
	"math/bits"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"

	"xmanager_cloud/internal/storage"
)

var _ storage.OperationStore = (*Repository)(nil)

type spannerTransaction interface {
	ReadRow(ctx context.Context, table string, key spanner.Key, columns []string) (*spanner.Row, error)
	Query(ctx context.Context, stmt spanner.Statement) *spanner.RowIterator
}

// Repository manages persistence for operation entities in Cloud Spanner.
type Repository struct {
	client *spanner.Client
}

// NewRepository creates a new Operation repository instance.
func NewRepository(client *spanner.Client) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil")
	}

	return &Repository{
		client: client,
	}, nil
}

func reverseBits(x int64) int64 {
	return int64(bits.Reverse64(uint64(x)))
}
