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

package metricsingester

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/bigquery/storage/managedwriter"
	"google.golang.org/api/option"
	"xmanager_cloud/pkg/clickhouse"
)

// RuntimeStore owns the live metrics storage client handles, writer, readiness pinger, and LIFO
// teardown stack for a configured backend (ClickHouse or BigQuery).
type RuntimeStore struct {
	writer    MetricWriter
	pinger    Pinger
	cleanups  []func() error
	closeOnce sync.Once
	closeErr  error
}

// Writer returns the MetricWriter for the configured storage backend.
func (s *RuntimeStore) Writer() MetricWriter {
	return s.writer
}

// Pinger returns the readiness Pinger for the /readyz health check.
func (s *RuntimeStore) Pinger() Pinger {
	return s.pinger
}

// Close releases all underlying storage handles in LIFO order. Safe to call multiple times.
func (s *RuntimeStore) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = runCleanupsLIFO(s.cleanups)
	})
	return s.closeErr
}

func runCleanupsLIFO(cleanups []func() error) error {
	var errs []error
	for i := len(cleanups) - 1; i >= 0; i-- {
		if err := cleanups[i](); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// OpenRuntimeStore connects and verifies the storage backend selected by cfg.MetricsStore.
func OpenRuntimeStore(ctx context.Context, cfg *Config) (*RuntimeStore, error) {
	switch cfg.MetricsStore {
	case StoreClickHouse:
		return openClickHouseStore(ctx, func(ctx context.Context) (clickHouseConn, error) {
			return clickhouse.Connect(ctx)
		})
	case StoreBigQuery:
		return openBigQueryStoreWithOptions(ctx, cfg, nil, nil)
	default:
		return nil, fmt.Errorf("unsupported metrics_store %q", cfg.MetricsStore)
	}
}

type clickHouseConn interface {
	BatchPreparer
	Pinger
	Close() error
}

func openClickHouseStore(ctx context.Context, connect func(context.Context) (clickHouseConn, error)) (*RuntimeStore, error) {
	conn, err := connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to clickhouse: %w", err)
	}
	return &RuntimeStore{
		writer:   NewClickHouseWriter(conn),
		pinger:   conn,
		cleanups: []func() error{conn.Close},
	}, nil
}

func openBigQueryStoreWithOptions(ctx context.Context, cfg *Config, bqOpts, mwOpts []option.ClientOption) (store *RuntimeStore, retErr error) {
	var cleanups []func() error
	defer func() {
		if retErr != nil {
			_ = runCleanupsLIFO(cleanups)
		}
	}()

	bqClient, err := bigquery.NewClient(ctx, cfg.BigQueryProjectID, bqOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create bigquery client: %w", err)
	}
	cleanups = append(cleanups, bqClient.Close)

	pinger := &tableMetadataPinger{
		table: bqClient.DatasetInProject(cfg.BigQueryProjectID, cfg.BigQueryDataset).Table(cfg.BigQueryTable),
	}
	checkCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := pinger.Ping(checkCtx); err != nil {
		return nil, fmt.Errorf("failed to verify bigquery table %s.%s.%s: %w",
			cfg.BigQueryProjectID, cfg.BigQueryDataset, cfg.BigQueryTable, err)
	}

	mwClient, err := managedwriter.NewClient(ctx, cfg.BigQueryProjectID, mwOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create bigquery storage write client: %w", err)
	}
	cleanups = append(cleanups, mwClient.Close)

	appender, closeStream, err := OpenBigQueryStream(ctx, mwClient, cfg.BigQueryProjectID, cfg.BigQueryDataset, cfg.BigQueryTable)
	if err != nil {
		return nil, err
	}
	cleanups = append(cleanups, closeStream)

	return &RuntimeStore{
		writer:   NewBigQueryWriter(appender),
		pinger:   pinger,
		cleanups: cleanups,
	}, nil
}

// OpenBigQueryStream opens a committed Storage Write API default stream for projectID.datasetID.tableID.
// The returned close function filters io.EOF because ManagedStream.Close returns io.EOF on normal teardown.
func OpenBigQueryStream(ctx context.Context, client *managedwriter.Client, projectID, datasetID, tableID string) (RowAppender, func() error, error) {
	tableName := managedwriter.TableParentFromParts(projectID, datasetID, tableID)
	stream, err := client.NewManagedStream(
		ctx,
		managedwriter.WithDestinationTable(tableName),
		managedwriter.WithType(managedwriter.DefaultStream),
		managedwriter.WithSchemaDescriptor(MetricRowDescriptor()),
		managedwriter.EnableWriteRetries(true),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open bigquery write stream for %s: %w", tableName, err)
	}
	closeFn := func() error {
		if err := stream.Close(); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	}
	return &managedStreamAppender{stream: stream}, closeFn, nil
}

type managedStreamAppender struct {
	stream *managedwriter.ManagedStream
}

var _ RowAppender = (*managedStreamAppender)(nil)

func (a *managedStreamAppender) AppendRows(ctx context.Context, rows [][]byte) error {
	res, err := a.stream.AppendRows(ctx, rows)
	if err != nil {
		return err
	}
	_, err = res.GetResult(ctx)
	return err
}

type tableMetadataPinger struct {
	table *bigquery.Table
}

var _ Pinger = (*tableMetadataPinger)(nil)

func (p *tableMetadataPinger) Ping(ctx context.Context) error {
	if _, err := p.table.Metadata(ctx); err != nil {
		return fmt.Errorf("bigquery table metadata check failed: %w", err)
	}
	return nil
}
