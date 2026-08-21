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

package main

import (
	"context"
	"fmt"
	"io"
	"sync"

	health "xmanager_cloud/common/health"

	"cloud.google.com/go/spanner"
)

// SpannerHealthClientFactory defines a factory function to instantiate a SpannerHealthClient.
type SpannerHealthClientFactory func(ctx context.Context, database string) (health.SpannerHealthClient, error)

// DefaultSpannerHealthClientFactory creates a real SpannerHealthClient using spanner.NewClient.
func DefaultSpannerHealthClientFactory(ctx context.Context, database string) (health.SpannerHealthClient, error) {
	client, err := spanner.NewClient(ctx, database)
	if err != nil {
		return nil, err
	}
	return health.NewSpannerHealthClient(client), nil
}

// LazySpannerHealthClient provides thread-safe lazy initialization and automatic reconnection
// for Spanner health checks. If the client fails to connect at startup or encounters connection
// dropouts, it attempts reconnection on subsequent health queries.
type LazySpannerHealthClient struct {
	mu       sync.Mutex
	dbPath   string
	factory  SpannerHealthClientFactory
	healthCl health.SpannerHealthClient
}

// NewLazySpannerHealthClient creates a new LazySpannerHealthClient for the given database path.
func NewLazySpannerHealthClient(dbPath string, factory SpannerHealthClientFactory) *LazySpannerHealthClient {
	if factory == nil {
		factory = DefaultSpannerHealthClientFactory
	}
	return &LazySpannerHealthClient{
		dbPath:  dbPath,
		factory: factory,
	}
}

// Initialize attempts eager initialization of the Spanner client.
func (c *LazySpannerHealthClient) Initialize(ctx context.Context) error {
	if c.dbPath == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ensureClientLocked(ctx)
}

// QuerySchemaVersion queries the SchemaMigrations table, reconnecting lazily if necessary.
func (c *LazySpannerHealthClient) QuerySchemaVersion(ctx context.Context) (int64, bool, error) {
	if c.dbPath == "" {
		return 0, false, fmt.Errorf("spanner database path is not configured")
	}

	c.mu.Lock()
	err := c.ensureClientLocked(ctx)
	healthCl := c.healthCl
	c.mu.Unlock()

	if err != nil {
		return 0, false, fmt.Errorf("spanner client not available: %w", err)
	}

	version, dirty, err := healthCl.QuerySchemaVersion(ctx)
	if err != nil {
		// Reset client on error to ensure clean reconnection on next attempt.
		c.mu.Lock()
		if c.healthCl == healthCl {
			c.healthCl = nil
		}
		c.mu.Unlock()

		if closer, ok := healthCl.(io.Closer); ok {
			_ = closer.Close()
		}
		return 0, false, err
	}

	return version, dirty, nil
}

// Close closes the underlying Spanner client connection.
func (c *LazySpannerHealthClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	if closer, ok := c.healthCl.(io.Closer); ok {
		err = closer.Close()
	}
	c.healthCl = nil
	return err
}

func (c *LazySpannerHealthClient) ensureClientLocked(ctx context.Context) error {
	if c.healthCl != nil {
		return nil
	}
	healthCl, err := c.factory(ctx, c.dbPath)
	if err != nil {
		return fmt.Errorf("failed to create spanner client for %s: %w", c.dbPath, err)
	}
	c.healthCl = healthCl
	return nil
}
