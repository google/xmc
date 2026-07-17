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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"google.golang.org/api/option"
	ss "xmanager_cloud/storage/spanner/spanner"
	"xmanager_cloud/storage/storage"
)

func newStorage(ctx context.Context, config *Config) (stg storage.Storage, sc *spanner.Client, cleanup func(), err error) {
	sc, err = spanner.NewClientWithConfig(
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
		return nil, nil, nil, fmt.Errorf("failed to create spanner client: %w", err)
	}

	stg, err = ss.NewStorageWithConfig(sc, ss.StorageConfig{StatusMessageHistoryEnabled: config.StatusMessageHistoryEnabled})
	if err != nil {
		sc.Close()
		return nil, nil, nil, fmt.Errorf("failed to create storage: %w", err)
	}

	return stg, sc, func() { sc.Close() }, nil
}
