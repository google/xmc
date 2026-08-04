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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	metricspb "xmanager_cloud/api/protos/metrics"
	ingestiongateway "xmanager_cloud/cmd/ingestion_gateway"
	"xmanager_cloud/test/metrics/infra"
)

// generateCertKeyPair generates a self-signed RSA certificate and private key written to PEM files.
func generateCertKeyPair(tb testing.TB, certPath, keyPath string, commonName string) {
	tb.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		tb.Fatalf("failed to generate RSA key: %v", err)
	}

	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"XMC Test"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		tb.Fatalf("failed to create certificate: %v", err)
	}

	certFile, err := os.Create(certPath)
	if err != nil {
		tb.Fatalf("failed to create cert file: %v", err)
	}
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		tb.Fatalf("failed to encode cert PEM: %v", err)
	}

	keyFile, err := os.Create(keyPath)
	if err != nil {
		tb.Fatalf("failed to create key file: %v", err)
	}
	defer keyFile.Close()

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		tb.Fatalf("failed to marshal private key: %v", err)
	}

	if err := pem.Encode(keyFile, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		tb.Fatalf("failed to encode key PEM: %v", err)
	}
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

	data, err := json.Marshal(m)
	if err != nil {
		tb.Fatalf("failed to marshal test metric: %v", err)
	}

	return data
}

// TestGatewayRedpanda_MTLSKeyReloader verifies dynamic cert rotation using keyreloader.
func TestGatewayRedpanda_MTLSKeyReloader(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")

	generateCertKeyPair(t, certPath, keyPath, "v1.xmc.local")

	cfg := &ingestiongateway.Config{
		TLSEnabled:  true,
		TLSCertFile: certPath,
		TLSKeyFile:  keyPath,
	}

	tlsConfig, reloader, err := ingestiongateway.BuildTLSConfig(cfg)
	if err != nil {
		t.Fatalf("BuildTLSConfig failed: %v", err)
	}
	if tlsConfig == nil || reloader == nil {
		t.Fatalf("expected non-nil tlsConfig and reloader")
	}

	cert1, err := reloader.GetClientCertificate(nil)
	if err != nil {
		t.Fatalf("initial GetClientCertificate failed: %v", err)
	}
	if cert1 == nil || len(cert1.Certificate) == 0 {
		t.Fatalf("expected initial certificate payload")
	}

	time.Sleep(10 * time.Millisecond)
	generateCertKeyPair(t, certPath, keyPath, "v2.xmc.local")

	cert2, err := reloader.GetClientCertificate(nil)
	if err != nil {
		t.Fatalf("rotated GetClientCertificate failed: %v", err)
	}
	if cert2 == nil || len(cert2.Certificate) == 0 {
		t.Fatalf("expected rotated certificate payload")
	}

	if bytes.Equal(cert1.Certificate[0], cert2.Certificate[0]) {
		t.Errorf("expected rotated certificate to differ from initial certificate")
	}
}

// TestGatewayRedpanda_BrokerOutage validates HTTP 503 congestion responses during broker outages.
func TestGatewayRedpanda_BrokerOutage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	rpContainer, brokers, err := infra.StartRedpanda(ctx, nil)
	if err != nil {
		t.Fatalf("failed to start Redpanda container: %v", err)
	}
	defer func() {
		_ = rpContainer.Terminate(context.Background())
	}()

	cfg := &ingestiongateway.Config{
		RedpandaBrokers: brokers,
	}

	producer, err := ingestiongateway.NewKafkaProducer(cfg, nil)
	if err != nil {
		t.Fatalf("failed to initialize producer: %v", err)
	}
	defer producer.Close()

	adminClient := kadm.NewClient(producer)
	if _, err := adminClient.CreateTopics(ctx, 1, 1, nil, "xmc-metrics"); err != nil {
		t.Fatalf("failed to create topic xmc-metrics: %v", err)
	}

	handler := ingestiongateway.NewMetricsIngestionHandler(producer, "xmc-metrics", nil)
	server := httptest.NewServer(handler)
	defer server.Close()

	body := createTestMetricRecord(t, 100, 200)
	resp, err := http.Post(server.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("initial produce request failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected HTTP status 202, got %d", resp.StatusCode)
	}

	containerID := rpContainer.GetContainerID()
	if err := exec.CommandContext(ctx, "docker", "pause", containerID).Run(); err != nil {
		t.Fatalf("failed to pause Redpanda container %s: %v", containerID, err)
	}
	defer func() {
		_ = exec.CommandContext(context.Background(), "docker", "unpause", containerID).Run()
	}()

	outageCtx, outageCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer outageCancel()

	outageReq, err := http.NewRequestWithContext(outageCtx, http.MethodPost, server.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to create outage request: %v", err)
	}
	outageReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	outageResp, err := client.Do(outageReq)
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context deadline exceeded") {
			t.Fatalf("request failed unexpectedly during broker outage: %v", err)
		}
	} else {
		defer outageResp.Body.Close()
		if outageResp.StatusCode != http.StatusServiceUnavailable && outageResp.StatusCode != http.StatusGatewayTimeout {
			t.Errorf("expected HTTP status 503 or 504 during broker outage, got %d", outageResp.StatusCode)
		}
	}

	if err := exec.CommandContext(ctx, "docker", "unpause", containerID).Run(); err != nil {
		t.Fatalf("failed to unpause Redpanda container %s: %v", containerID, err)
	}

	time.Sleep(1 * time.Second)

	recResp, err := http.Post(server.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post-outage produce request failed: %v", err)
	}
	_ = recResp.Body.Close()
	if recResp.StatusCode != http.StatusAccepted {
		t.Errorf("expected recovery HTTP status 202, got %d", recResp.StatusCode)
	}
}

// TestGatewayRedpanda_MurmurHash2Partitioning validates key partition routing.
func TestGatewayRedpanda_MurmurHash2Partitioning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	rpContainer, brokers, err := infra.StartRedpanda(ctx, nil)
	if err != nil {
		t.Fatalf("failed to start Redpanda container: %v", err)
	}
	defer func() {
		_ = rpContainer.Terminate(context.Background())
	}()

	clientOpts := []kgo.Opt{kgo.SeedBrokers(brokers...)}
	client, err := kgo.NewClient(clientOpts...)
	if err != nil {
		t.Fatalf("failed to create kgo client: %v", err)
	}
	defer client.Close()

	adminClient := kadm.NewClient(client)
	topicName := "xmc-partition-test"
	_, err = adminClient.CreateTopics(ctx, 4, 1, nil, topicName)
	if err != nil {
		t.Fatalf("failed to create topic %s: %v", topicName, err)
	}

	cfg := &ingestiongateway.Config{
		RedpandaBrokers: brokers,
	}

	opts, err := ingestiongateway.BuildProducerOptions(cfg, nil)
	if err != nil {
		t.Fatalf("BuildProducerOptions failed: %v", err)
	}

	producer, err := kgo.NewClient(opts...)
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

type blockingProducer struct{}

func (b *blockingProducer) Produce(ctx context.Context, r *kgo.Record, promise func(*kgo.Record, error)) {
	<-ctx.Done()
	promise(r, ctx.Err())
}

func (b *blockingProducer) Close() {}

// TestGatewayRedpanda_ContextCancellation verifies request abort on context cancellation.
func TestGatewayRedpanda_ContextCancellation(t *testing.T) {
	body := createTestMetricRecord(t, 555, 777)

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	req, err := http.NewRequestWithContext(canceledCtx, http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to create context request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler := ingestiongateway.NewMetricsIngestionHandler(&blockingProducer{}, "xmc-metrics", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusGatewayTimeout {
		t.Errorf("expected status 503 or 504 on canceled context, got %d", rec.Code)
	}
}
