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

package metrics_test

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/pubsub/pstest"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	metricspb "xmanager_cloud/api/protos/metrics"
	"xmanager_cloud/cmd/metrics_ingester/metricsingester"
	"xmanager_cloud/test/metrics/infra"
)

const (
	pubsubProjectID      = "test-project"
	pubsubSubscriptionID = "xmc-metrics-ingester"
	pubsubTopicName      = "projects/" + pubsubProjectID + "/topics/" + metricsTopic
)

// startFakeMetricsSubscription starts a pstest server holding the metrics topic and a subscription.
func startFakeMetricsSubscription(ctx context.Context, t *testing.T) (*pstest.Server, *pubsub.Subscription) {
	t.Helper()
	srv := pstest.NewServer()
	t.Cleanup(func() { srv.Close() })

	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial the pub/sub fake: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client, err := pubsub.NewClient(ctx, pubsubProjectID, option.WithGRPCConn(conn))
	if err != nil {
		t.Fatalf("failed to create pub/sub client: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	topic, err := client.CreateTopic(ctx, metricsTopic)
	if err != nil {
		t.Fatalf("failed to create topic: %v", err)
	}
	sub, err := client.CreateSubscription(ctx, pubsubSubscriptionID, pubsub.SubscriptionConfig{Topic: topic})
	if err != nil {
		t.Fatalf("failed to create subscription: %v", err)
	}
	return srv, sub
}

func publishProtoMetrics(t *testing.T, srv *pstest.Server, metrics []*metricspb.Metric) []string {
	t.Helper()
	ids := make([]string, 0, len(metrics))
	for _, m := range metrics {
		data, err := proto.Marshal(m)
		if err != nil {
			t.Fatalf("proto.Marshal() error = %v", err)
		}
		ids = append(ids, srv.Publish(pubsubTopicName, data, nil))
	}
	return ids
}

func awaitAllAcked(ctx context.Context, t *testing.T, srv *pstest.Server, ids []string) {
	t.Helper()
	for _, id := range ids {
		for srv.Message(id).Acks == 0 {
			if ctx.Err() != nil {
				t.Fatalf("timed out waiting for message %s to be acked", id)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// TestPubSubPipelineIngestion drives the Pub/Sub message bus end to end: messages published to a
// fake topic are delivered by the metrics ingester into a real ClickHouse through
// xmc_metrics_pubsub_queue, and xmc_metrics_pubsub_mv applies the same filtering and sanitization
// as the Redpanda path.
func TestPubSubPipelineIngestion(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	harness, err := infra.StartHarness(ctx, locateSchemaFile(t))
	if err != nil {
		t.Fatalf("StartHarness failed: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = harness.Teardown(cleanupCtx)
	}()

	srv, sub := startFakeMetricsSubscription(ctx, t)
	cfg := &metricsingester.Config{BatchSize: 10, BatchInterval: 100 * time.Millisecond}
	ingester := metricsingester.NewIngester(sub, metricsingester.NewClickHouseWriter(harness.ClickHouseConn), cfg)
	runCtx, stopIngester := context.WithCancel(ctx)
	runErr := make(chan error, 1)
	go func() { runErr <- ingester.Run(runCtx) }()

	nowMicro := time.Now().UnixMicro()
	valid := &metricspb.Metric{Xid: 20001, WorkUnitId: 1, ExecutionId: 10, JobName: "pubsub_job", ReplicaId: "worker-0", MetricName: "train_loss", MetricValue: 0.25, Step: 1, Timestamp: nowMicro}
	needsDefaults := &metricspb.Metric{Xid: 20001, WorkUnitId: 1, ExecutionId: 10, JobName: "pubsub_job", MetricName: "train_loss", MetricValue: 0.5, Step: 2, Timestamp: 5}
	filtered := &metricspb.Metric{Xid: 0, WorkUnitId: 1, ExecutionId: 10, JobName: "pubsub_filtered", MetricName: "train_loss", Step: 3, Timestamp: nowMicro}
	ids := publishProtoMetrics(t, srv, []*metricspb.Metric{valid, needsDefaults, filtered})
	ids = append(ids, srv.Publish(pubsubTopicName, []byte{0xff}, nil))

	count, err := pollRecordCount(ctx, harness, "SELECT count() FROM xmc_metrics WHERE xid = 20001 AND job_name = 'pubsub_job'", 2)
	if err != nil || count != 2 {
		dumpClickHouseDiagnostics(ctx, t, harness)
		t.Fatalf("expected 2 ingested records, got %d (err: %v)", count, err)
	}
	awaitAllAcked(ctx, t, srv, ids)

	assertStoredMetric(ctx, t, harness, 1, "worker-0", func(ts time.Time) bool { return ts.UnixMicro() == nowMicro })
	// An empty replica_id defaults to '0' and an out-of-range timestamp is replaced by ingest time.
	assertStoredMetric(ctx, t, harness, 2, "0", func(ts time.Time) bool { return time.Since(ts) < time.Hour })

	var filteredCount uint64
	if err := harness.ClickHouseConn.QueryRow(ctx, "SELECT count() FROM xmc_metrics WHERE job_name = 'pubsub_filtered'").Scan(&filteredCount); err != nil {
		t.Fatalf("failed to count filtered records: %v", err)
	}
	if filteredCount != 0 {
		t.Errorf("expected the xid=0 record to be filtered by xmc_metrics_pubsub_mv, found %d", filteredCount)
	}

	stopIngester()
	if err := <-runErr; err != nil {
		t.Errorf("Run() error = %v, want nil", err)
	}
}

// assertStoredMetric checks the replica_id and timestamp stored for the pubsub_job row at step.
func assertStoredMetric(ctx context.Context, t *testing.T, harness *infra.Harness, step int64, wantReplica string, validTimestamp func(time.Time) bool) {
	t.Helper()
	var replicaID string
	var timestamp time.Time
	row := harness.ClickHouseConn.QueryRow(ctx, "SELECT replica_id, timestamp FROM xmc_metrics WHERE xid = 20001 AND job_name = 'pubsub_job' AND step = ?", step)
	if err := row.Scan(&replicaID, &timestamp); err != nil {
		t.Fatalf("failed to read step %d: %v", step, err)
	}
	if replicaID != wantReplica {
		t.Errorf("step %d replica_id = %q, want %q", step, replicaID, wantReplica)
	}
	if !validTimestamp(timestamp) {
		t.Errorf("step %d has unexpected timestamp %v", step, timestamp)
	}
}
