package main

import (
	"context"
	"fmt"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/google_api/option/option"
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
