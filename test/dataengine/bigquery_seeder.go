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

package dataengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	xmcbigquery "xmanager_cloud/pkg/bigquery"
)

const (
	bigQueryInsertChunkSize   = 500
	ephemeralBigQueryTableTTL = 2 * time.Hour
)

type bigQuerySeedRow struct {
	Xid         int64     `bigquery:"xid"`
	WorkUnitID  int64     `bigquery:"work_unit_id"`
	ExecutionID int64     `bigquery:"execution_id"`
	JobName     string    `bigquery:"job_name"`
	ReplicaID   string    `bigquery:"replica_id"`
	MetricName  string    `bigquery:"metric_name"`
	MetricValue float64   `bigquery:"metric_value"`
	Step        int64     `bigquery:"step"`
	Timestamp   time.Time `bigquery:"timestamp"`
}

// EnsureBigQuerySchema ensures the target partitioned and clustered BigQuery metrics table exists.
func EnsureBigQuerySchema(ctx context.Context, opts *xmcbigquery.Options, clientOpts ...option.ClientOption) error {
	return ensureBigQueryTable(ctx, opts, false, clientOpts...)
}

func ensureBigQueryTable(ctx context.Context, opts *xmcbigquery.Options, ephemeral bool, clientOpts ...option.ClientOption) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	client, err := bigquery.NewClient(ctx, opts.ProjectID, clientOpts...)
	if err != nil {
		return fmt.Errorf("failed to create bigquery client: %w", err)
	}
	defer client.Close()

	table := client.DatasetInProject(opts.ProjectID, opts.Dataset).Table(opts.Table)
	if _, err = table.Metadata(ctx); err == nil {
		return nil
	}
	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusNotFound {
		return fmt.Errorf("failed to check bigquery table metadata: %w", err)
	}
	meta := bigQueryMetricsTableMetadata()
	if ephemeral {
		meta.ExpirationTime = time.Now().UTC().Add(ephemeralBigQueryTableTTL)
	}
	if err := table.Create(ctx, meta); err != nil {
		return fmt.Errorf("failed to create bigquery table %s.%s.%s: %w", opts.ProjectID, opts.Dataset, opts.Table, err)
	}
	return nil
}

// DropBigQueryTable deletes an ephemeral BigQuery test table if it exists.
func DropBigQueryTable(ctx context.Context, opts *xmcbigquery.Options, clientOpts ...option.ClientOption) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	client, err := bigquery.NewClient(ctx, opts.ProjectID, clientOpts...)
	if err != nil {
		return fmt.Errorf("failed to create bigquery client: %w", err)
	}
	defer client.Close()

	err = client.DatasetInProject(opts.ProjectID, opts.Dataset).Table(opts.Table).Delete(ctx)
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete bigquery table %s.%s.%s: %w", opts.ProjectID, opts.Dataset, opts.Table, err)
	}
	return nil
}

func bigQueryMetricsTableMetadata() *bigquery.TableMetadata {
	reqField := func(name string, fieldType bigquery.FieldType) *bigquery.FieldSchema {
		return &bigquery.FieldSchema{Name: name, Type: fieldType, Required: true}
	}
	return &bigquery.TableMetadata{
		Schema: bigquery.Schema{
			reqField("xid", bigquery.IntegerFieldType),
			reqField("work_unit_id", bigquery.IntegerFieldType),
			reqField("execution_id", bigquery.IntegerFieldType),
			reqField("job_name", bigquery.StringFieldType),
			reqField("replica_id", bigquery.StringFieldType),
			reqField("metric_name", bigquery.StringFieldType),
			reqField("metric_value", bigquery.FloatFieldType),
			reqField("step", bigquery.IntegerFieldType),
			reqField("timestamp", bigquery.TimestampFieldType),
			reqField("created_at", bigquery.TimestampFieldType),
		},
		TimePartitioning: &bigquery.TimePartitioning{
			Type:  bigquery.DayPartitioningType,
			Field: "timestamp",
		},
		Clustering: &bigquery.Clustering{
			Fields: []string{"xid", "metric_name", "work_unit_id"},
		},
	}
}

// SeedBigQueryDataset loads seed records from JSON and inserts them via parameterized DML batches.
func SeedBigQueryDataset(ctx context.Context, conn xmcbigquery.Conn, seedFilePath string) (int, error) {
	records, err := loadSeedRecords(seedFilePath)
	if err != nil || len(records) == 0 {
		return 0, err
	}
	rows, err := toBigQuerySeedRows(records)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`
		INSERT INTO %s (xid, work_unit_id, execution_id, job_name, replica_id, metric_name, metric_value, step, timestamp, created_at)
		SELECT r.xid, r.work_unit_id, r.execution_id, r.job_name, r.replica_id, r.metric_name, r.metric_value, r.step, r.timestamp, CURRENT_TIMESTAMP()
		FROM UNNEST(?) AS r
	`, conn.TableRef())

	for start := 0; start < len(rows); start += bigQueryInsertChunkSize {
		end := min(start+bigQueryInsertChunkSize, len(rows))
		res, err := conn.Query(ctx, query, rows[start:end])
		if err != nil {
			return 0, fmt.Errorf("failed to insert bigquery seed batch [%d:%d]: %w", start, end, err)
		}
		_ = res.Close()
	}
	return len(rows), nil
}

// CleanupBigQueryTestMetrics purges seeded BigQuery test metrics matching xids via DML DELETE.
func CleanupBigQueryTestMetrics(ctx context.Context, conn xmcbigquery.Conn, xids ...int64) error {
	if len(xids) == 0 {
		return nil
	}
	res, err := conn.Query(ctx, fmt.Sprintf("DELETE FROM %s WHERE xid IN UNNEST(?)", conn.TableRef()), xids)
	if err != nil {
		return fmt.Errorf("failed to delete bigquery test metrics: %w", err)
	}
	return res.Close()
}

func loadSeedRecords(seedFilePath string) ([]MetricRecord, error) {
	resolvedPath, err := FindFile(seedFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to locate seed file: %w", err)
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read seed file %q: %w", resolvedPath, err)
	}
	var records []MetricRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("failed to parse seed JSON: %w", err)
	}
	return records, nil
}

func toBigQuerySeedRows(records []MetricRecord) ([]bigQuerySeedRow, error) {
	out := make([]bigQuerySeedRow, len(records))
	for i, r := range records {
		ts, err := parseSeedTimestamp(r.TimestampStr)
		if err != nil {
			return nil, fmt.Errorf("seed record %d: %w", i, err)
		}
		replicaID := r.ReplicaId
		if replicaID == "" {
			replicaID = "0"
		}
		out[i] = bigQuerySeedRow{
			Xid:         r.Xid,
			WorkUnitID:  r.WorkUnitId,
			ExecutionID: r.ExecutionId,
			JobName:     r.JobName,
			ReplicaID:   replicaID,
			MetricName:  r.MetricName,
			MetricValue: r.MetricValue,
			Step:        r.Step,
			Timestamp:   ts,
		}
	}
	return out, nil
}

func parseSeedTimestamp(raw string) (time.Time, error) {
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts.UTC(), nil
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("invalid RFC3339 seed timestamp %q", raw)
}
