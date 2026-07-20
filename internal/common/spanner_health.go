// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package health

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
)

// SpannerHealthClient defines the subset of Spanner functionality needed for health checks.
type SpannerHealthClient interface {
	QuerySchemaVersion(ctx context.Context) (version int64, dirty bool, err error)
}

type realSpannerHealthClient struct {
	client *spanner.Client
}

// NewSpannerHealthClient creates a new SpannerHealthClient wrapping a concrete spanner.Client.
func NewSpannerHealthClient(client *spanner.Client) SpannerHealthClient {
	return &realSpannerHealthClient{client: client}
}

// QuerySchemaVersion implements SpannerHealthClient by querying the SchemaMigrations table.
func (c *realSpannerHealthClient) QuerySchemaVersion(ctx context.Context) (version int64, dirty bool, err error) {
	if c.client == nil {
		return 0, false, ErrNilClient
	}

	stmt := spanner.Statement{
		SQL: "SELECT Version, Dirty FROM SchemaMigrations LIMIT 1",
	}
	iter := c.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err == iterator.Done {
		return 0, false, fmt.Errorf("no migration records found in SchemaMigrations table")
	}
	if err != nil {
		return 0, false, fmt.Errorf("failed to query SchemaMigrations: %w", err)
	}

	var versionVal spanner.NullInt64
	var dirtyVal spanner.NullBool
	if err := row.Columns(&versionVal, &dirtyVal); err != nil {
		return 0, false, fmt.Errorf("failed to parse SchemaMigrations columns: %w", err)
	}

	return versionVal.Int64, !dirtyVal.Valid || dirtyVal.Bool, nil
}
