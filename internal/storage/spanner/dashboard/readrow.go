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

package dashboard

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"xmanager_cloud/internal/grpcerr"
)

// readRowInTransaction reads a single row by primary key inside a ReadWriteTransaction, reporting a
// missing row as grpcerr.ErrNotFound so that callers can map it to a NOT_FOUND response.
//
// resource names the kind of row being read and appears in the returned error messages.
func readRowInTransaction(ctx context.Context, txn *spanner.ReadWriteTransaction, table, resource, id string, columns []string) (*spanner.Row, error) {
	row, err := txn.ReadRow(ctx, table, spanner.Key{id}, columns)
	if err != nil {
		if err == spanner.ErrRowNotFound || status.Code(err) == codes.NotFound {
			return nil, fmt.Errorf("%s %s not found: %w", resource, id, grpcerr.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to read %s %s: %w", resource, id, err)
	}
	return row, nil
}
