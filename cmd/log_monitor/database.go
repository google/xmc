// Copyright 2025 Google LLC
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
package main

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"github.com/google/xmc/internal/storage"
	ss "github.com/google/xmc/internal/storage/spanner"
	"google.golang.org/api/option"
)

// StorageComponents holds initialized Spanner and storage clients along with cleanup callback.
type StorageComponents struct {
	Storage storage.Storage
	Client  *spanner.Client
	Cleanup func()
}

func newStorage(ctx context.Context, config *Config) (StorageComponents, error) {
	sc, err := spanner.NewClientWithConfig(
		ctx,
		config.SpannerDatabaseName,
		spanner.ClientConfig{SessionPoolConfig: spanner.SessionPoolConfig{
			TrackSessionHandles: true,
			InactiveTransactionRemovalOptions: spanner.InactiveTransactionRemovalOptions{
				ActionOnInactiveTransaction: spanner.Warn,
			},
		}},
		option.WithQuotaProject(config.QuotaProjectID),
	)
	if err != nil {
		return StorageComponents{}, fmt.Errorf("failed to create spanner client: %w", err)
	}

	stg, err := ss.NewStorage(sc)
	if err != nil {
		sc.Close()
		return StorageComponents{}, fmt.Errorf("failed to create storage: %w", err)
	}

	return StorageComponents{
		Storage: stg,
		Client:  sc,
		Cleanup: func() { sc.Close() },
	}, nil
}
