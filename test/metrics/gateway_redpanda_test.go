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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	metricspb "xmanager_cloud/api/protos/metrics"
	ingestiongateway "xmanager_cloud/cmd/ingestion_gateway/ingestiongateway"
	"xmanager_cloud/test/metrics/infra"
)

var (
	sharedRedpanda *redpanda.Container
	sharedBrokers  []string
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	rp, brokers, err := infra.StartRedpanda(ctx, nil)
	if err != nil {
		fmt.Printf("failed to start shared Redpanda container: %v\n", err)
		os.Exit(1)
	}
	sharedRedpanda = rp
	sharedBrokers = brokers

	code := m.Run()

	_ = rp.Terminate(context.Background())
	os.Exit(code)
}

// createTestMetricRecord marshals a single Metric record into JSON payload for ingestion.
func createTestMetricRecord(tb testing.TB, xid, workUnitID int64) []byte {
	tb.Helper()

	m := &metricspb.Metric{
		Xid:         xid,
		WorkUnitId:  workUnitID,
		ExecutionId: 1,
		ReplicaId:   "replica-0",
		MetricName:  "gpu_utilization",
		MetricValue: 95.5,
		Step:        100,
		Timestamp:   time.Now().Unix(),
	}

	data, err := protojson.Marshal(m)
	if err != nil {
		tb.Fatalf("failed to marshal test metric: %v", err)
	}

	return data
}

// postJSONWithContext executes an HTTP POST request with context cancellation and application/json header.
func postJSONWithContext(ctx context.Context, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

// TestGatewayRedpanda_EndToEndHappyPath validates the complete happy path flow:
// HTTP POST -> Ingestion Gateway -> Kafka Producer -> Redpanda Broker -> Kafka Consumer.
func TestGatewayRedpanda_EndToEndHappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg := &ingestiongateway.Config{
		RedpandaBrokers: sharedBrokers,
	}

	producer, err := ingestiongateway.NewKafkaProducer(cfg, nil)
	if err != nil {
		t.Fatalf("failed to initialize producer: %v", err)
	}
	defer producer.Close()

	topicName := fmt.Sprintf("xmc-metrics-happypath-%d", time.Now().UnixNano())
	adminClient := kadm.NewClient(producer)
	if _, err := adminClient.CreateTopics(ctx, 1, 1, nil, topicName); err != nil {
		t.Fatalf("failed to create topic %s: %v", topicName, err)
	}

	// Create a dedicated consumer to consume published messages from Redpanda.
	consumerOpts := []kgo.Opt{
		kgo.SeedBrokers(sharedBrokers...),
		kgo.ConsumeTopics(topicName),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	}
	consumer, err := kgo.NewClient(consumerOpts...)
	if err != nil {
		t.Fatalf("failed to initialize consumer: %v", err)
	}
	defer consumer.Close()

	handler := ingestiongateway.NewMetricsIngestionHandler(producer, topicName, nil)
	server := httptest.NewServer(handler)
	defer server.Close()

	xid := int64(12345)
	workUnitID := int64(67890)
	body := createTestMetricRecord(t, xid, workUnitID)

	resp, err := postJSONWithContext(ctx, server.URL, body)
	if err != nil {
		t.Fatalf("HTTP POST produce request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected HTTP status 202 Accepted, got %d", resp.StatusCode)
	}

	var respPayload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&respPayload); err != nil {
		t.Fatalf("failed to decode response payload: %v", err)
	}
	if respPayload["status"] != "accepted" {
		t.Errorf("expected status 'accepted' in response, got %v", respPayload["status"])
	}

	// Poll Redpanda to verify message was persisted and readable.
	pollCtx, pollCancel := context.WithTimeout(ctx, 10*time.Second)
	defer pollCancel()

	fetches := consumer.PollFetches(pollCtx)
	if errs := fetches.Errors(); len(errs) > 0 {
		t.Fatalf("consumer poll failed: %v", errs[0].Err)
	}

	records := fetches.Records()
	if len(records) == 0 {
		t.Fatalf("expected at least 1 record consumed from Redpanda, got 0")
	}

	expectedKey := ingestiongateway.FormatCompositeKey(xid, workUnitID)
	rec := records[0]
	if !bytes.Equal(rec.Key, expectedKey) {
		t.Errorf("consumed record key = %s, expected %s", string(rec.Key), string(expectedKey))
	}

	var consumedMetric metricspb.Metric
	if err := proto.Unmarshal(rec.Value, &consumedMetric); err != nil {
		t.Fatalf("failed to unmarshal consumed binary metric proto: %v", err)
	}
	if consumedMetric.Xid != xid || consumedMetric.WorkUnitId != workUnitID {
		t.Errorf("consumed metric mismatch: got xid=%d, workUnitID=%d", consumedMetric.Xid, consumedMetric.WorkUnitId)
	}
}

// TestGatewayRedpanda_BrokerOutage validates HTTP 503 congestion responses during broker outages.
func TestGatewayRedpanda_BrokerOutage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg := &ingestiongateway.Config{
		RedpandaBrokers: sharedBrokers,
	}

	// Configure 0 retries so produce attempts fail fast during broker outage.
	producer, err := ingestiongateway.NewKafkaProducer(cfg, nil, kgo.RecordRetries(0))
	if err != nil {
		t.Fatalf("failed to initialize producer: %v", err)
	}
	defer producer.Close()

	topicName := fmt.Sprintf("xmc-metrics-outage-%d", time.Now().UnixNano())
	adminClient := kadm.NewClient(producer)
	if _, err := adminClient.CreateTopics(ctx, 1, 1, nil, topicName); err != nil {
		t.Fatalf("failed to create topic %s: %v", topicName, err)
	}

	handler := ingestiongateway.NewMetricsIngestionHandler(producer, topicName, nil)
	server := httptest.NewServer(handler)
	defer server.Close()

	body := createTestMetricRecord(t, 100, 200)
	resp, err := postJSONWithContext(ctx, server.URL, body)
	if err != nil {
		t.Fatalf("initial produce request failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected HTTP status 202, got %d", resp.StatusCode)
	}

	if err := infra.PauseContainer(ctx, sharedRedpanda); err != nil {
		t.Fatalf("failed to pause Redpanda container: %v", err)
	}
	defer func() {
		_ = infra.UnpauseContainer(context.Background(), sharedRedpanda)
	}()

	outageCtx, outageCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer outageCancel()

	outageReq, err := http.NewRequestWithContext(outageCtx, http.MethodPost, server.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to create outage request: %v", err)
	}
	outageReq.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{}
	outageResp, err := httpClient.Do(outageReq)
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context deadline exceeded") {
			t.Fatalf("outage produce request failed unexpectedly: %v", err)
		}
		t.Logf("produce request timed out during broker outage: %v", err)
	} else {
		defer outageResp.Body.Close()
		if outageResp.StatusCode != http.StatusServiceUnavailable && outageResp.StatusCode != http.StatusGatewayTimeout {
			t.Errorf("expected HTTP status 503 or 504 during broker outage, got %d", outageResp.StatusCode)
		}
	}

	if err := infra.UnpauseContainer(ctx, sharedRedpanda); err != nil {
		t.Fatalf("failed to unpause Redpanda container: %v", err)
	}

	// Poll until Redpanda recovers TCP connectivity and handler accepts request.
	var recResp *http.Response
	var lastStatus int
	recovered := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		recResp, err = postJSONWithContext(ctx, server.URL, body)
		if err == nil {
			lastStatus = recResp.StatusCode
			if recResp.StatusCode == http.StatusAccepted {
				_ = recResp.Body.Close()
				recovered = true
				break
			}
		}
		if recResp != nil {
			_ = recResp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !recovered {
		t.Fatalf("post-outage produce request failed to recover within deadline, last status: %d, last error: %v", lastStatus, err)
	}
}

// TestGatewayRedpanda_MurmurHash2Partitioning validates key partition routing.
func TestGatewayRedpanda_MurmurHash2Partitioning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	clientOpts := []kgo.Opt{kgo.SeedBrokers(sharedBrokers...)}
	client, err := kgo.NewClient(clientOpts...)
	if err != nil {
		t.Fatalf("failed to create kgo client: %v", err)
	}
	defer client.Close()

	adminClient := kadm.NewClient(client)
	topicName := fmt.Sprintf("xmc-partition-test-%d", time.Now().UnixNano())
	_, err = adminClient.CreateTopics(ctx, 4, 1, nil, topicName)
	if err != nil {
		t.Fatalf("failed to create topic %s: %v", topicName, err)
	}

	cfg := &ingestiongateway.Config{
		RedpandaBrokers: sharedBrokers,
	}

	producer, err := ingestiongateway.NewKafkaProducer(cfg, nil)
	if err != nil {
		t.Fatalf("failed to create partition producer client: %v", err)
	}
	defer producer.Close()

	xid := int64(100201)
	workUnitID := int64(900100)
	key := ingestiongateway.FormatCompositeKey(xid, workUnitID)

	var (
		wg         sync.WaitGroup
		partitions []int32
		mu         sync.Mutex
	)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		rec := &kgo.Record{
			Topic: topicName,
			Key:   key,
			Value: []byte("test_value"),
		}
		producer.Produce(ctx, rec, func(r *kgo.Record, err error) {
			defer wg.Done()
			if err != nil {
				t.Errorf("produce error: %v", err)
				return
			}
			mu.Lock()
			partitions = append(partitions, r.Partition)
			mu.Unlock()
		})
	}

	wg.Wait()

	if len(partitions) < 10 {
		t.Fatalf("expected 10 produced record callbacks, got %d", len(partitions))
	}

	expectedPartition := partitions[0]
	for i, p := range partitions {
		if p != expectedPartition {
			t.Errorf("record %d routed to partition %d, expected partition %d for key %s", i, p, expectedPartition, string(key))
		}
	}
}
